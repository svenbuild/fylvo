package dbfs

import (
	"fmt"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/stretchr/testify/require"
)

// Fylvo: a walk through a folder level with more entries than one database page (about 32'700 on
// SQLite) sees every entry exactly once. Delete, copy, folder size and ZIP downloads walk this way.
func TestWalkSeesEachEntryOfABigLevelOnce(t *testing.T) {
	e := newSharedFolderEnv(t)
	owner := e.as(e.owner)
	e.mkdir(owner, "cloudreve://my/Big")
	big, err := owner.Get(e.ctx, e.uri("cloudreve://my/Big"))
	require.NoError(t, err)

	const count = 33000
	for start := 0; start < count; start += 1000 {
		rows := make([]*ent.FileCreate, 0, 1000)
		for i := start; i < start+1000; i++ {
			rows = append(rows, e.client.File.Create().
				SetType(int(types.FileTypeFile)).
				SetName(fmt.Sprintf("IMG_%05d.jpg", i)).
				SetOwnerID(e.owner.ID).
				SetFileChildren(big.ID()).
				SetSize(1))
		}
		require.NoError(t, e.client.File.CreateBulk(rows...).Exec(e.ctx))
	}

	seen := make(map[int]int)
	err = e.as(e.owner).Walk(e.ctx, e.uri("cloudreve://my/Big"), 10, func(f fs.File, level int) error {
		seen[f.ID()]++
		return nil
	})
	require.NoError(t, err)
	require.Len(t, seen, count+1) // the files and the folder itself
	for id, n := range seen {
		require.Equal(t, 1, n, "entry %d seen %d times", id, n)
	}
}
