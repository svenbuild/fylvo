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

// Fylvo: a level with more folders than SQLite takes parameters in one query (32'766). Walking
// into their children and searching below them listed the children of all of them at once.
func TestWalkAndSearchThroughALevelWithManyFolders(t *testing.T) {
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
				SetType(int(types.FileTypeFolder)).
				SetName(fmt.Sprintf("Ordner %05d", i)).
				SetOwnerID(e.owner.ID).
				SetFileChildren(big.ID()))
		}
		require.NoError(t, e.client.File.CreateBulk(rows...).Exec(e.ctx))
	}
	// One file in the first and one in the last folder, and a folder below the last one.
	e.upload(owner, "cloudreve://my/Big/Ordner 00000/zuerst.txt", 1)
	e.upload(owner, fmt.Sprintf("cloudreve://my/Big/Ordner %05d/zuletzt.txt", count-1), 1)
	e.mkdir(owner, fmt.Sprintf("cloudreve://my/Big/Ordner %05d/tiefer", count-1))

	seen := make(map[int]int)
	err = e.as(e.owner).Walk(e.ctx, e.uri("cloudreve://my/Big"), 10, func(f fs.File, level int) error {
		seen[f.ID()]++
		return nil
	})
	require.NoError(t, err)
	require.Len(t, seen, 1+count+3) // Big, its folders, the two files and "tiefer"
	for id, n := range seen {
		require.Equal(t, 1, n, "entry %d seen %d times", id, n)
	}

	// Searching below Big finds files under the first and the last folder, page by page.
	found := map[string]int{}
	token := ""
	for {
		_, res, err := e.as(e.owner).List(e.ctx, e.uri("cloudreve://my/Big?name=zu"),
			fs.WithPageSize(1), WithCursorPagination(token))
		require.NoError(t, err)
		for _, f := range res.Files {
			found[f.Name()]++
		}
		token = res.Pagination.NextPageToken
		if token == "" {
			break
		}
	}
	require.Equal(t, map[string]int{"zuerst.txt": 1, "zuletzt.txt": 1}, found)
}

// Fylvo: a group of folders of the search is fixed by IDs. A folder deleted between two pages
// does not move a folder of the next group into one that was read already.
func TestFolderGroupIsFixedByIDs(t *testing.T) {
	folders := make(map[int]*File)
	for id := 1; id <= 25000; id++ {
		folders[id] = &File{Model: &ent.File{ID: id}}
	}

	first, to := folderGroup(folders, 0, 0)
	require.Len(t, first, maxParentsPerQuery)
	require.Equal(t, 10001, to)
	second, to2 := folderGroup(folders, to, 0)
	require.Len(t, second, maxParentsPerQuery)
	require.Equal(t, 20001, to2)
	last, end := folderGroup(folders, to2, 0)
	require.Len(t, last, 5000)
	require.Zero(t, end)

	delete(folders, 5)
	again, sameTo := folderGroup(folders, 0, to)
	require.Len(t, again, maxParentsPerQuery-1)
	require.Equal(t, to, sameTo)
	require.Equal(t, 10000, again[len(again)-1].ID)
}
