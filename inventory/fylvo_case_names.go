package inventory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"entgo.io/ent/dialect/sql"
	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/ent/file"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/samber/lo"
)

// Fylvo: names that differ only in upper and lower case. Windows (NTFS) cannot hold `a.jpg`
// and `A.jpg` in one folder, so the sync app for Windows cannot either. A new name in a
// folder must therefore not be another spelling of a name that is there. Entries that are
// there already ("twins" from old data or an import from Linux) stay as they are.
//
// The check runs after the write, in the transaction of the write. SQLite runs with a
// single connection (client.go, SetMaxOpenConns(1)), so no other writer can come in between
// the write, the check and the commit; a refused write is rolled back with the transaction.

// ErrNameCaseTaken is the answer to a name that is another spelling of a name in the folder.
// The other spelling is not named: a visitor of a drop box must not learn what is there.
var ErrNameCaseTaken = serializer.NewError(serializer.CodeNameCaseTaken,
	"A file or folder with the same name in other upper and lower case is already there", nil)

// IsNameCaseTaken reports whether err is or wraps ErrNameCaseTaken.
func IsNameCaseTaken(err error) bool {
	var appErr serializer.AppError
	return errors.As(err, &appErr) && appErr.Code == serializer.CodeNameCaseTaken
}

// FoldName returns name as Windows compares file names: each character in upper case, one
// UTF-16 unit to one (so `ä` and `Ä` are one name, `ß` stays `ß`), characters outside the
// basic plane as they are, no Unicode normalisation (a composed `ü` and `u` with a combining
// mark stay two names, as on NTFS). It follows ntfs_fold of the Windows app.
func FoldName(name string) string {
	return strings.Map(func(r rune) rune {
		if r > 0xFFFF {
			return r
		}
		return unicode.ToUpper(r)
	}, name)
}

// caseTwinPattern is a LIKE pattern that matches every name that may fold to the same as
// name. SQLite's LIKE ignores the case of ASCII letters only, so every other character
// becomes "_" (one character), and so do i and s: `ı` and `ſ` are upper case I and S.
func caseTwinPattern(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == '%' || r == '_' || r == '\\':
			b.WriteRune('\\')
			b.WriteRune(r)
		case r >= 0x80 || strings.ContainsRune("iIsS", r):
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// CaseTwins returns the names of the entries of the folder parent that differ from name only
// in upper and lower case.
func (f *fileClient) CaseTwins(ctx context.Context, parent int, name string) ([]string, error) {
	candidates, err := f.client.File.Query().
		Where(file.FileChildren(parent), file.NameNEQ(name), func(s *sql.Selector) {
			s.Where(sql.P(func(b *sql.Builder) {
				b.Ident(s.C(file.FieldName)).WriteString(" LIKE ").Arg(caseTwinPattern(name)).WriteString(` ESCAPE '\'`)
			}))
		}).
		Select(file.FieldName).
		Strings(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to look for names in other case: %w", err)
	}

	folded := FoldName(name)
	return lo.Filter(candidates, func(candidate string, _ int) bool { return FoldName(candidate) == folded }), nil
}

// ensureNoCaseTwins checks the files with the given ids as they are now, after a write: none
// may share its folder with another spelling of its name.
func (f *fileClient) ensureNoCaseTwins(ctx context.Context, ids ...int) error {
	for _, chunk := range lo.Chunk(ids, 500) {
		written, err := f.client.File.Query().
			Where(file.IDIn(chunk...)).
			Select(file.FieldID, file.FieldName, file.FieldFileChildren).
			All(ctx)
		if err != nil {
			return fmt.Errorf("failed to read written files: %w", err)
		}

		for _, w := range written {
			if w.FileChildren == 0 {
				continue // a root folder or an entry in the trash
			}
			twins, err := f.CaseTwins(ctx, w.FileChildren, w.Name)
			if err != nil {
				return err
			}
			if len(twins) > 0 {
				return ErrNameCaseTaken
			}
		}
	}
	return nil
}

// hasExactChild reports whether the folder parent has an entry named exactly name.
func (f *fileClient) hasExactChild(ctx context.Context, parent *ent.File, name string) (bool, error) {
	if parent == nil {
		return false, nil
	}
	return f.client.File.Query().Where(file.FileChildren(parent.ID), file.Name(name)).Exist(ctx)
}
