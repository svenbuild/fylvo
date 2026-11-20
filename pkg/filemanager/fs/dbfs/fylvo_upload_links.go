package dbfs

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/hashid"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
)

// Fylvo: share links that take uploads. A visitor adds files to the shared folder, also
// without an account, and never changes what is there.

// maxFreeNameTries is how many names are tried for an upload whose name is taken.
const maxFreeNameTries = 100

// maxNameLength is the longest name a file can have, in bytes (see validateFileName).
const maxNameLength = 255

// freeName returns an address next to taken that no file has yet: "name (1).ext" and so on.
func (f *DBFS) freeName(ctx context.Context, navigator Navigator, taken *fs.URI) (*fs.URI, error) {
	ext := path.Ext(taken.Name())
	base := strings.TrimSuffix(taken.Name(), ext)

	for i := 1; i <= maxFreeNameTries; i++ {
		// A name that is as long as names may be makes room for the addition.
		addition := fmt.Sprintf(" (%d)%s", i, ext)
		if room := maxNameLength - len(addition); room > 0 && len(base) > room {
			base = strings.ToValidUTF8(base[:room], "")
		}

		candidate := taken.DirUri().Join(base + addition)
		closest, err := f.getFileByPath(ctx, navigator, candidate)
		if err != nil && !ent.IsNotFound(err) {
			return nil, fmt.Errorf("failed to look for a free name: %w", err)
		}

		used, err := f.nameTaken(ctx, closest, candidate)
		if err != nil {
			return nil, err
		}
		if !used {
			return candidate, nil
		}
	}

	return nil, fs.ErrFileExisted
}

// nameTaken reports whether target is taken: by an entry of exactly that name or by one whose
// name differs only in upper and lower case. closest is what reading target found.
func (f *DBFS) nameTaken(ctx context.Context, closest *File, target *fs.URI) (bool, error) {
	me := hashid.EncodeUserID(f.hasher, f.user.ID)
	if closest.Uri(false).IsSame(target, me) {
		return true, nil
	}
	if !closest.Uri(false).IsSame(target.DirUri(), me) {
		return false, nil // the folder is not there either
	}

	// Inside a transaction its client is used: SQLite has a single connection.
	fc, _ := inventory.InheritTx(ctx, f.fileClient)
	twin, err := fc.CaseTwin(ctx, closest.ID(), target.Name())
	return twin != "", err
}

// reserveLinkUpload counts an upload against the share link it comes in through. Only
// visitors count: the owner and the members of a shared folder upload on their own account.
func (f *DBFS) reserveLinkUpload(ctx context.Context, navigator Navigator, target *File, size int64) error {
	shared, ok := navigator.(*shareNavigator)
	if !ok || shared.editable || !shared.addable || f.isOwnerCall(ctx, target) {
		return nil
	}

	err := f.shareClient.ReserveUpload(ctx, shared.share.ID, size)
	if errors.Is(err, inventory.ErrUploadLimitReached) {
		return serializer.NewError(serializer.CodeInsufficientCapacity, "The upload limit of this link is reached", err)
	}

	return err
}
