package dbfs

import (
	"context"
	"errors"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/hashid"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
)

// Fylvo: a conditional update is an upload that names the version it replaces ("previous",
// the hashed id of the current entity of the file). It replaces exactly that version of
// exactly that file, or it fails:
//   - 40076 (stale version): the file has another current version, or another file took its place;
//   - 40904 (target gone): there is no file at the address, only with PreviousRequired. Without it
//     a missing file is created new, as before; the web app and the desktop client rely on that.

// ErrUploadTargetGone is the answer to a conditional update whose file is no longer there.
var ErrUploadTargetGone = serializer.NewError(serializer.CodeUploadTargetGone, "The file to replace is gone", nil)

// checkPreviousRequired checks what can be told before the lock.
func checkPreviousRequired(props *fs.UploadProps, fileExisted bool) error {
	if !props.PreviousRequired {
		return nil
	}

	if props.PreviousVersion == "" {
		return serializer.NewError(serializer.CodeParamErr, "previous_required needs previous", nil)
	}

	if !fileExisted {
		return ErrUploadTargetGone
	}

	return nil
}

// recheckUploadTarget reads the target of a conditional update again, under the lock of the
// upload. The target was read before the lock, so another upload, a delete or a move may
// have come in between. lockErr is what taking the lock answered: a conflict means that the
// file or a folder above it changed in between, which is told apart here.
func (f *DBFS) recheckUploadTarget(ctx context.Context, navigator Navigator, props *fs.UploadProps, read *File, lockErr error) (*File, error) {
	var appErr serializer.AppError
	if lockErr != nil && (!errors.As(lockErr, &appErr) || appErr.Code != serializer.CodeConflict) {
		return nil, lockErr
	}

	current, err := f.getFileByPath(ctx, navigator, props.Uri)
	if err != nil && !ent.IsNotFound(err) {
		return nil, err
	}

	if err != nil || !current.Uri(false).IsSame(props.Uri, hashid.EncodeUserID(f.hasher, f.user.ID)) {
		if props.PreviousRequired {
			return nil, ErrUploadTargetGone
		}
		return nil, fs.ErrModified.WithError(errors.New("the file was removed before lock acquisition"))
	}

	if current.ID() != read.ID() || current.Type() != types.FileTypeFile {
		return nil, fs.ErrStaleVersion
	}

	if lockErr != nil {
		return nil, lockErr
	}

	return current, nil
}
