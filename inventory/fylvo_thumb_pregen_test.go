package inventory

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/cloudreve/Cloudreve/v4/ent/entity"
	"github.com/cloudreve/Cloudreve/v4/ent/enttest"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/boolset"
	"github.com/stretchr/testify/require"
)

func TestThumbCandidates(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", filepath.Join(t.TempDir(), "thumbs.db"))
	t.Cleanup(func() { require.NoError(t, client.Close()) })

	group, err := client.Group.Create().SetName("G").SetPermissions(&boolset.BooleanSet{}).Save(ctx)
	require.NoError(t, err)
	u, err := client.User.Create().SetEmail("a@fylvo.test").SetNick("a").SetGroup(group).SetSettings(&types.UserSetting{}).Save(ctx)
	require.NoError(t, err)
	policy, err := client.StoragePolicy.Create().SetName("local").SetType(types.PolicyTypeLocal).Save(ctx)
	require.NoError(t, err)

	newFile := func(name string, entityTypes ...types.EntityType) int {
		f, err := client.File.Create().SetType(int(types.FileTypeFile)).SetName(name).SetOwner(u).
			SetStoragePolicies(policy).Save(ctx)
		require.NoError(t, err)
		for i, et := range entityTypes {
			e, err := client.Entity.Create().SetType(int(et)).SetSource(name).SetSize(1).
				SetStoragePolicy(policy).Save(ctx)
			require.NoError(t, err)
			upd := client.File.UpdateOne(f).AddEntities(e)
			if i == 0 {
				upd = upd.SetPrimaryEntity(e.ID)
			}
			_, err = upd.Save(ctx)
			require.NoError(t, err)
		}
		return f.ID
	}

	plain := newFile("plain.jpg", types.EntityTypeVersion)
	withThumb := newFile("thumb.jpg", types.EntityTypeVersion, types.EntityTypeThumbnail)
	disabled := newFile("disabled.jpg", types.EntityTypeVersion)
	mark, err := client.Metadata.Create().SetFileID(disabled).SetName("thumb:disabled").SetValue("").Save(ctx)
	require.NoError(t, err)
	uploading := newFile("uploading.jpg") // no version yet

	fc := NewFileClient(client, "sqlite", nil)
	candidates := func(after, before, limit int) []int {
		files, err := fc.ThumbCandidates(ctx, after, before, limit, "thumb:disabled")
		require.NoError(t, err)
		ids := []int{}
		for _, f := range files {
			ids = append(ids, f.ID)
		}
		return ids
	}
	require.Equal(t, []int{plain}, candidates(0, 1<<30, 100))

	// A thumbnail and a mark that were deleted (soft delete) do not count.
	_, err = client.Entity.Update().Where(entity.Type(int(types.EntityTypeThumbnail))).SetDeletedAt(time.Now()).Save(ctx)
	require.NoError(t, err)
	_, err = client.Metadata.UpdateOne(mark).SetDeletedAt(time.Now()).Save(ctx)
	require.NoError(t, err)
	require.Equal(t, []int{disabled, withThumb, plain}, candidates(0, 1<<30, 100))

	// Range and limit.
	require.Empty(t, candidates(plain, withThumb, 100))
	require.Equal(t, []int{disabled}, candidates(0, 1<<30, 1))

	max, err := fc.MaxFileID(ctx)
	require.NoError(t, err)
	require.Equal(t, uploading, max)
}
