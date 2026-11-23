package inventory

import (
	"context"

	"entgo.io/ent/dialect/sql"
	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/ent/entity"
	"github.com/cloudreve/Cloudreve/v4/ent/file"
	"github.com/cloudreve/Cloudreve/v4/ent/metadata"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
)

// ThumbCandidates returns up to limit files that have a current version but no thumbnail and are
// not marked with disabledKey (Fylvo, thumbnails made ahead of time). Only files with an id below
// before and above after; newest first. The two checks are correlated NOT EXISTS lookups by the
// file's id (ent's Not(Has...) builds NOT IN over the whole tables, for every batch).
func (f *fileClient) ThumbCandidates(ctx context.Context, after, before, limit int, disabledKey string) ([]*ent.File, error) {
	return f.client.File.Query().
		Where(
			file.Type(int(types.FileTypeFile)),
			file.IDGT(after),
			file.IDLT(before),
			file.PrimaryEntityNEQ(0),
			noThumbnailEntity(),
			noMetadata(disabledKey),
		).
		Order(ent.Desc(file.FieldID)).
		Limit(limit).
		All(ctx)
}

func noThumbnailEntity() func(*sql.Selector) {
	return func(s *sql.Selector) {
		fe := sql.Table(file.EntitiesTable)
		e := sql.Table(entity.Table)
		s.Where(sql.Not(sql.Exists(
			sql.Select(fe.C(file.EntitiesPrimaryKey[0])).From(fe).
				Join(e).On(fe.C(file.EntitiesPrimaryKey[1]), e.C(entity.FieldID)).
				Where(sql.And(
					sql.ColumnsEQ(fe.C(file.EntitiesPrimaryKey[0]), s.C(file.FieldID)),
					sql.EQ(e.C(entity.FieldType), int(types.EntityTypeThumbnail)),
					sql.IsNull(e.C(entity.FieldDeletedAt)),
				)),
		)))
	}
}

func noMetadata(name string) func(*sql.Selector) {
	return func(s *sql.Selector) {
		m := sql.Table(metadata.Table)
		s.Where(sql.Not(sql.Exists(
			sql.Select(m.C(metadata.FieldFileID)).From(m).
				Where(sql.And(
					sql.ColumnsEQ(m.C(metadata.FieldFileID), s.C(file.FieldID)),
					sql.EQ(m.C(metadata.FieldName), name),
					sql.IsNull(m.C(metadata.FieldDeletedAt)),
				)),
		)))
	}
}

// MaxFileID returns the highest id of a file or folder, 0 if there is none (Fylvo).
func (f *fileClient) MaxFileID(ctx context.Context) (int, error) {
	last, err := f.client.File.Query().Order(ent.Desc(file.FieldID)).First(ctx)
	if ent.IsNotFound(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return last.ID, nil
}
