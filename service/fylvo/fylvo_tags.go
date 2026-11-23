package fylvo

import (
	"context"
	"sort"
	"strings"

	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/ent/file"
	"github.com/cloudreve/Cloudreve/v4/ent/metadata"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/gin-gonic/gin"
)

const tagPrefix = "tag:"

type TagResponse struct {
	Name  string `json:"name"`
	Color string `json:"color"`
	Count int    `json:"count"`
}

// ListTags returns the tags on the user's own files outside the trash, sorted by name, with the
// number of files and the color most of them use.
func ListTags(c *gin.Context) ([]TagResponse, error) {
	dep := dependency.FromContext(c)
	u := inventory.UserFromContext(c)

	rows, err := dep.DBClient().Metadata.Query().
		Where(metadata.NameHasPrefix(tagPrefix), metadata.IsPublic(true), metadata.HasFileWith(file.OwnerID(u.ID))).
		Select(metadata.FieldName, metadata.FieldValue, metadata.FieldFileID).
		All(c)
	if err != nil {
		return nil, serializer.NewError(serializer.CodeDBError, "Failed to list tags", err)
	}

	live := newLiveCheck(dep)
	type tally struct {
		count  int
		colors map[string]int
	}
	tags := make(map[string]*tally)
	for _, row := range rows {
		name := strings.TrimPrefix(row.Name, tagPrefix)
		if name == "" {
			continue
		}
		ok, err := live.inLiveTree(c, row.FileID)
		if err != nil {
			return nil, serializer.NewError(serializer.CodeDBError, "Failed to list tags", err)
		}
		if !ok {
			continue
		}
		t, found := tags[name]
		if !found {
			t = &tally{colors: make(map[string]int)}
			tags[name] = t
		}
		t.count++
		if row.Value != "" {
			t.colors[row.Value]++
		}
	}

	res := make([]TagResponse, 0, len(tags))
	for name, t := range tags {
		color, best := "", 0
		for c, n := range t.colors {
			if n > best || (n == best && c < color) {
				color, best = c, n
			}
		}
		res = append(res, TagResponse{Name: name, Color: color, Count: t.count})
	}
	sort.Slice(res, func(i, j int) bool { return strings.ToLower(res[i].Name) < strings.ToLower(res[j].Name) })
	return res, nil
}

// liveCheck tells whether a file hangs below a normal root folder, that is, neither the file nor a
// folder above it is in the trash. It remembers every folder it has read.
type liveCheck struct {
	dep  dependency.Dep
	live map[int]bool
}

func newLiveCheck(dep dependency.Dep) *liveCheck {
	return &liveCheck{dep: dep, live: make(map[int]bool)}
}

func (l *liveCheck) inLiveTree(ctx context.Context, id int) (bool, error) {
	var path []int
	result := false
	for current := id; ; {
		if known, ok := l.live[current]; ok {
			result = known
			break
		}
		path = append(path, current)
		f, err := l.dep.DBClient().File.Query().Where(file.ID(current)).
			Select(file.FieldName, file.FieldFileChildren).Only(ctx)
		if err != nil {
			if ent.IsNotFound(err) {
				break
			}
			return false, err
		}
		if f.FileChildren == 0 {
			result = f.Name == inventory.RootFolderName
			break
		}
		current = f.FileChildren
	}

	for _, id := range path {
		l.live[id] = result
	}
	return result, nil
}
