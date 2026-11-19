package dbfs

import (
	"context"
	"strings"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/hashid"
	"github.com/samber/lo"
)

// Fylvo: shared folders show up under "Shared with me" without the member doing anything.

// memberShareEntries returns one entry for every shared folder the current user is a member
// of. An entry has the shape of a saved shortcut: a symbolic folder that redirects to the
// share. Nothing is written to the member's own tree. Shares listed in saved are left out,
// the member has a shortcut to them already.
func (t *sharedWithMeNavigator) memberShareEntries(ctx context.Context, search *inventory.SearchFileParameters,
	saved map[string]struct{}) ([]*File, error) {
	shares, err := t.shareClient.ListForMember(ctx, t.user.ID)
	if err != nil {
		return nil, err
	}

	entries := make([]*File, 0, len(shares))
	for _, share := range shares {
		shareID := hashid.EncodeShareID(t.hasher, share.ID)
		if _, ok := saved[shareID]; ok || !matchesSearch(share.Edges.File.Name, search) {
			continue
		}

		// A folder in the trash of its owner is not shared any more, the same rule as when opening it.
		root, err := t.findRoot(ctx, newFile(nil, share.Edges.File))
		if err != nil || root.Name() != inventory.RootFolderName {
			continue
		}

		model := *share.Edges.File
		model.IsSymbolic = true
		model.Edges.Metadata = []*ent.Metadata{
			{Name: MetadataSharedRedirect, Value: fs.NewShareUri(shareID, ""), IsPublic: true},
			{Name: MetadataSharedOwner, Value: hashid.EncodeUserID(t.hasher, share.Edges.User.ID), IsPublic: true},
		}

		entry := newFile(nil, &model)
		entry.OwnerModel = share.Edges.User
		entry.Path[pathIndexUser] = newSharedWithMeUri(hashid.EncodeFileID(t.hasher, model.ID))
		entries = append(entries, entry)
	}

	return entries, nil
}

// savedShares returns the IDs of the shares the given shortcuts point to.
func savedShares(shortcuts []*File) map[string]struct{} {
	saved := make(map[string]struct{})
	for _, shortcut := range shortcuts {
		redirect, err := fs.NewUriFromString(shortcut.Metadata()[MetadataSharedRedirect])
		if err != nil {
			continue
		}

		saved[redirect.ID("")] = struct{}{}
	}

	return saved
}

// matchesSearch reports whether a shared folder belongs into the result of a search.
// Only a search by name can find one.
func matchesSearch(name string, search *inventory.SearchFileParameters) bool {
	if search == nil {
		return true
	}

	if search.Type != nil && *search.Type != types.FileTypeFolder {
		return false
	}

	name = strings.ToLower(name)
	contains := func(term string) bool { return strings.Contains(name, strings.ToLower(term)) }
	if search.NameOperatorOr {
		return lo.SomeBy(search.Name, contains)
	}

	return len(search.Name) > 0 && lo.EveryBy(search.Name, contains)
}
