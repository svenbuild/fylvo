package inventory

import (
	"context"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/ent/entity"
	"github.com/samber/lo"
)

// ImportedSources returns which of the given paths on disk a stored file already uses (Fylvo),
// in any storage policy: two local policies can point at the same folder. The import must not
// register such a path a second time: two files would share one file on disk, and deleting one of
// them for good would delete the content of the other. Files in the trash and files waiting to be
// recycled still count. One query per chunk; the caller keeps the list short (a batch of the
// import). The column has no index, so each query reads the whole table (about 0.1 s per batch
// with 350'000 stored files on a PC).
func (f *fileClient) ImportedSources(ctx context.Context, sources []string) (map[string]bool, error) {
	found := make(map[string]bool)
	for _, chunk := range lo.Chunk(sources, capPageSize(f.maxSQlParam, 500, 10)) {
		used, err := f.client.Entity.Query().
			Where(entity.SourceIn(chunk...)).
			Select(entity.FieldSource).
			Strings(ctx)
		if err != nil && !ent.IsNotFound(err) {
			return nil, err
		}
		for _, source := range used {
			found[source] = true
		}
	}
	return found, nil
}
