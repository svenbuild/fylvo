package inventory

import (
	"context"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/ent/entity"
	"github.com/samber/lo"
)

// ImportedSources returns which of the given paths on disk a stored file of the storage policy
// already uses (Fylvo). The import must not register such a path a second time: two files would
// share one file on disk, and deleting one of them for good would delete the content of the other.
// One query for all paths; the caller keeps the list short (a batch of the import).
func (f *fileClient) ImportedSources(ctx context.Context, policyID int, sources []string) (map[string]bool, error) {
	found := make(map[string]bool)
	for _, chunk := range lo.Chunk(sources, capPageSize(f.maxSQlParam, 500, 10)) {
		used, err := f.client.Entity.Query().
			Where(entity.StoragePolicyEntities(policyID), entity.SourceIn(chunk...)).
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
