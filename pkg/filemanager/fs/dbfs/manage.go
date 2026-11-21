package dbfs

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/ent/user"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/activity"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/hashid"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/samber/lo"
	"golang.org/x/tools/container/intsets"
)

func (f *DBFS) Create(ctx context.Context, path *fs.URI, fileType types.FileType, opts ...fs.Option) (fs.File, error) {
	o := newDbfsOption()
	for _, opt := range opts {
		o.apply(opt)
	}

	// Get navigator
	navigator, err := f.getNavigator(ctx, path, NavigatorCapabilityCreateFile, NavigatorCapabilityLockFile)
	if err != nil {
		return nil, err
	}

	// Get most recent ancestor
	var ancestor *File
	if o.ancestor != nil {
		ancestor = o.ancestor
	} else {
		ancestor, err = f.getFileByPath(ctx, navigator, path)
		if err != nil && !ent.IsNotFound(err) {
			return nil, fmt.Errorf("failed to get ancestor: %w", err)
		}
	}

	// Fylvo: who may only add (a visitor of a link that takes uploads) gets a new file next
	// to one that is there, also in another spelling, and simply continues in a folder that
	// is there. A name that is taken is no error for them. An upload (which passes its
	// ancestor) chose its free name before, outside of its transaction.
	addOnly := !f.canWrite(ctx, ancestor) && f.canAdd(ctx, ancestor)
	if addOnly && fileType == types.FileTypeFile && o.ancestor == nil {
		taken, err := f.nameTaken(ctx, ancestor, path)
		if err != nil {
			return nil, err
		}
		if taken {
			free, err := f.freeName(ctx, navigator, path)
			if err != nil {
				return nil, err
			}

			return f.Create(ctx, free, fileType, opts...)
		}
	}

	if ancestor.Uri(false).IsSame(path, hashid.EncodeUserID(f.hasher, f.user.ID)) {

		if ancestor.Type() == fileType {
			if o.errOnConflict && !addOnly {
				return ancestor, fs.ErrFileExisted
			}

			// Target file already exist, return it.
			return ancestor, nil
		}

		// File with the same name but different type already exist
		return nil, fs.ErrFileExisted.
			WithError(fmt.Errorf("object with the same name but different type %v already exist", ancestor.Type()))
	}

	if !f.canAdd(ctx, ancestor) {
		return nil, fs.ErrOwnerOnly
	}

	// Fylvo: only the owner decides about metadata and shortcuts. A member with edit right
	// must not plant a shortcut to another share in the owner's tree.
	if (len(o.Metadata) > 0 || o.isSymbolicLink) && !f.isOwnerCall(ctx, ancestor) {
		return nil, fs.ErrOwnerOnly
	}

	// Lock ancestor
	lockedPath := ancestor.RootUri().JoinRaw(path.PathTrimmed())
	ls, err := f.acquireByPath(ctx, -1, f.user, false, fs.LockApp(fs.ApplicationCreate),
		&LockByPath{lockedPath, ancestor, fileType, ""})
	defer func() { _ = f.Release(ctx, ls) }()
	if err != nil {
		return nil, err
	}

	// For all ancestors in user's desired path, create folders if not exist
	existedElements := ancestor.Uri(false).Elements()
	desired := path.Elements()
	if (len(desired)-len(existedElements) > 1) && o.noChainedCreation {
		return nil, fs.ErrPathNotExist
	}

	for i := len(existedElements); i < len(desired); i++ {
		// Make sure parent is a folder
		if !ancestor.CanHaveChildren() {
			return nil, fs.ErrNotSupportedAction.WithError(fmt.Errorf("parent must be a valid folder"))
		}

		// Validate object name
		if err := validateFileName(desired[i]); err != nil {
			return nil, fs.ErrIllegalObjectName.WithError(err)
		}

		if i < len(desired)-1 || fileType == types.FileTypeFolder {
			args := &inventory.CreateFolderParameters{
				Owner: ancestor.Model.OwnerID,
				Name:  desired[i],
			}

			// Apply options for last element
			if i == len(desired)-1 {
				if o.Metadata != nil {
					args.Metadata = o.Metadata
				}
				args.IsSymbolic = o.isSymbolicLink
				args.ModifiedAt = o.ModifiedAt
			}

			// Create folder if it is not the last element or the target is a folder
			fc, tx, ctx, err := inventory.WithTx(ctx, f.fileClient)
			if err != nil {
				return nil, serializer.NewError(serializer.CodeDBError, "Failed to start transaction", err)
			}

			newFolder, err := fc.CreateFolder(ctx, ancestor.Model, args)
			if err != nil {
				_ = inventory.Rollback(tx)
				return nil, fmt.Errorf("failed to create folder %q: %w", desired[i], err)
			}

			if err := inventory.Commit(tx); err != nil {
				return nil, serializer.NewError(serializer.CodeDBError, "Failed to commit folder creation", err)
			}

			ancestor = newFile(ancestor, newFolder)
			f.emitFileCreated(ctx, ancestor)
			activity.Record(ctx, f.activityEntry(activity.FileCreate, ancestor, path))
		} else {
			// valide file name
			policy, err := f.getPreferredPolicy(ctx, ancestor)
			if err != nil {
				return nil, err
			}

			if err := validateExtension(desired[i], policy); err != nil {
				return nil, fs.ErrIllegalObjectName.WithError(err)
			}

			if err := validateFileNameRegexp(desired[i], policy); err != nil {
				return nil, fs.ErrIllegalObjectName.WithError(err)
			}

			file, err := f.createFile(ctx, ancestor, desired[i], fileType, o)
			if err != nil {
				return nil, err
			}
			if o.UploadRequest == nil {
				// Fylvo: an upload is logged when it is complete, see CompleteUpload.
				activity.Record(ctx, f.activityEntry(activity.FileCreate, file, path))
			}

			return file, nil
		}
	}

	return ancestor, nil
}

func (f *DBFS) Rename(ctx context.Context, path *fs.URI, newName string) (fs.File, *fs.IndexDiff, error) {
	// Get navigator
	navigator, err := f.getNavigator(ctx, path, NavigatorCapabilityRenameFile, NavigatorCapabilityLockFile)
	if err != nil {
		return nil, nil, err
	}

	// Get target file
	ctx = context.WithValue(ctx, inventory.LoadFileMetadata{}, true)
	target, err := f.getFileByPath(ctx, navigator, path)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get target file: %w", err)
	}
	oldName := target.Name()

	if !f.canWrite(ctx, target) {
		return nil, nil, fs.ErrOwnerOnly
	}

	// Root folder cannot be modified
	if target.IsRootFolder() {
		return nil, nil, fs.ErrNotSupportedAction.WithError(fmt.Errorf("cannot modify root folder"))
	}

	// Validate new name
	if err := validateFileName(newName); err != nil {
		return nil, nil, fs.ErrIllegalObjectName.WithError(err)
	}

	// If target is a file, validate file extension
	policy, err := f.getPreferredPolicy(ctx, target)
	if err != nil {
		return nil, nil, err
	}

	if target.Type() == types.FileTypeFile {
		if err := validateExtension(newName, policy); err != nil {
			return nil, nil, fs.ErrIllegalObjectName.WithError(err)
		}

		if err := validateFileNameRegexp(newName, policy); err != nil {
			return nil, nil, fs.ErrIllegalObjectName.WithError(err)
		}
	}

	// Lock target
	ls, err := f.acquireByPath(ctx, -1, f.user, false, fs.LockApp(fs.ApplicationRename),
		&LockByPath{target.Uri(true), target, target.Type(), ""})
	defer func() { _ = f.Release(ctx, ls) }()
	if err != nil {
		return nil, nil, err
	}

	// Rename target
	fc, tx, ctx, err := inventory.WithTx(ctx, f.fileClient)
	if err != nil {
		return nil, nil, serializer.NewError(serializer.CodeDBError, "Failed to start transaction", err)
	}

	updated, err := fc.Rename(ctx, target.Model, newName)
	if err != nil {
		_ = inventory.Rollback(tx)
		return nil, nil, writeError(err, "failed to update file")
	}

	if target.Type() == types.FileTypeFile && !strings.EqualFold(filepath.Ext(newName), filepath.Ext(oldName)) {
		if err := fc.RemoveMetadata(ctx, target.Model, ThumbDisabledKey); err != nil {
			_ = inventory.Rollback(tx)
			return nil, nil, serializer.NewError(serializer.CodeDBError, "failed to remove disabled thumbnail mark", err)
		}
	}

	if err := inventory.Commit(tx); err != nil {
		return nil, nil, serializer.NewError(serializer.CodeDBError, "Failed to commit rename change", err)
	}

	f.emitFileRenamed(ctx, target, newName)
	renamed := f.activityEntry(activity.FileRename, target, path)
	renamed.Content.OldName = oldName
	renamed.Content.Name = newName
	activity.Record(ctx, renamed)

	originalMetadata := target.Metadata()
	newFile := target.Replace(updated)
	var diff *fs.IndexDiff
	if _, ok := originalMetadata[FullTextIndexKey]; ok {
		diff = &fs.IndexDiff{
			IndexToRename: []fs.IndexDiffRenameDetails{
				{
					Uri:      *newFile.Uri(false),
					FileID:   newFile.ID(),
					EntityID: newFile.PrimaryEntityID(),
				},
			},
		}
	}

	return newFile, diff, nil
}

func (f *DBFS) SoftDelete(ctx context.Context, path ...*fs.URI) error {
	ae := serializer.NewAggregateError()
	targets := make([]*File, 0, len(path))
	for _, p := range path {
		// Get navigator
		navigator, err := f.getNavigator(ctx, p, NavigatorCapabilitySoftDelete)
		if err != nil {
			ae.Add(p.String(), err)
			continue
		}

		// Get target file
		target, err := f.getFileByPath(ctx, navigator, p)
		if err != nil {
			ae.Add(p.String(), fmt.Errorf("failed to get target file: %w", err))
			continue
		}

		if !f.canWrite(ctx, target) {
			ae.Add(p.String(), fs.ErrOwnerOnly.WithError(fmt.Errorf("only file owner can delete file without trash bin")))
			continue
		}

		// Root folder cannot be deleted
		if target.IsRootFolder() {
			ae.Add(p.String(), fs.ErrNotSupportedAction.WithError(fmt.Errorf("cannot delete root folder")))
			continue
		}

		targets = append(targets, target)
	}

	if len(targets) == 0 {
		return ae.Aggregate()
	}
	// Lock all targets
	lockTargets := lo.Map(targets, func(value *File, key int) *LockByPath {
		return &LockByPath{value.Uri(true), value, value.Type(), ""}
	})
	ls, err := f.acquireByPath(ctx, -1, f.user, false, fs.LockApp(fs.ApplicationSoftDelete), lockTargets...)
	defer func() { _ = f.Release(ctx, ls) }()
	if err != nil {
		return err
	}

	// Fylvo: taken before, the trash renames the files and drops their folder.
	trashed := lo.Map(targets, func(target *File, _ int) activity.Entry {
		return f.activityEntry(activity.FileTrash, target, nil)
	})

	// Start transaction to soft-delete files
	fc, tx, ctx, err := inventory.WithTx(ctx, f.fileClient)
	if err != nil {
		return serializer.NewError(serializer.CodeDBError, "Failed to start transaction", err)
	}

	for _, target := range targets {
		modifiedAt, err := keptModifiedAt(ctx, fc, target.Model)
		if err != nil {
			_ = inventory.Rollback(tx)
			return serializer.NewError(serializer.CodeDBError, "failed to read file date", err)
		}

		// Perform soft-delete
		if err := fc.SoftDelete(ctx, target.Model); err != nil {
			_ = inventory.Rollback(tx)
			return serializer.NewError(serializer.CodeDBError, "failed to soft-delete file", err)
		}

		// Save restore uri into metadata
		if err := fc.UpsertMetadata(ctx, target.Model, map[string]string{
			MetadataRestoreUri: target.Uri(true).String(),
			MetadataExpectedCollectTime: strconv.FormatInt(
				time.Now().Add(time.Duration(target.Owner().Edges.Group.Settings.TrashRetention)*time.Second).Unix(),
				10),
			MetadataRestoreModifiedAt: modifiedAt,
		}, nil); err != nil {
			_ = inventory.Rollback(tx)
			return serializer.NewError(serializer.CodeDBError, "failed to update metadata", err)
		}
	}

	// Commit transaction
	if err := inventory.Commit(tx); err != nil {
		return serializer.NewError(serializer.CodeDBError, "Failed to commit soft-delete change", err)
	}

	for _, e := range trashed {
		activity.Record(ctx, e)
	}
	f.emitFileDeleted(ctx, targets...)

	return ae.Aggregate()
}

func (f *DBFS) Delete(ctx context.Context, path []*fs.URI, opts ...fs.Option) ([]fs.Entity, *fs.IndexDiff, error) {
	o := newDbfsOption()
	for _, opt := range opts {
		o.apply(opt)
	}

	var opt *types.EntityProps
	if o.UnlinkOnly {
		opt = &types.EntityProps{
			UnlinkOnly: true,
		}
	}

	ae := serializer.NewAggregateError()
	fileNavGroup := make(map[Navigator][]*File)
	ctx = context.WithValue(ctx, inventory.LoadFileEntity{}, true)
	// Fylvo: for the real name of files in the trash, see activityEntry.
	ctx = context.WithValue(ctx, inventory.LoadFileMetadata{}, true)

	for _, p := range path {
		// Get navigator
		navigator, err := f.getNavigator(ctx, p, NavigatorCapabilityDeleteFile, NavigatorCapabilityLockFile)
		if err != nil {
			ae.Add(p.String(), err)
			continue
		}

		// Get target file
		target, err := f.getFileByPath(ctx, navigator, p)
		if err != nil {
			ae.Add(p.String(), fmt.Errorf("failed to get target file: %w", err))
			continue
		}

		if _, ok := ctx.Value(ByPassOwnerCheckCtxKey{}).(bool); !o.SysSkipSoftDelete && !ok && target.Owner().ID != f.user.ID {
			ae.Add(p.String(), fs.ErrOwnerOnly)
			continue
		}

		// Root folder cannot be deleted
		if target.IsRootFolder() {
			ae.Add(p.String(), fs.ErrNotSupportedAction.WithError(fmt.Errorf("cannot delete root folder")))
			continue
		}

		if _, ok := fileNavGroup[navigator]; !ok {
			fileNavGroup[navigator] = make([]*File, 0)
		}
		fileNavGroup[navigator] = append(fileNavGroup[navigator], target)
	}

	targets := lo.Flatten(lo.Values(fileNavGroup))
	if len(targets) == 0 {
		return nil, nil, ae.Aggregate()
	}
	// Lock all targets
	lockTargets := lo.Map(targets, func(value *File, key int) *LockByPath {
		return &LockByPath{value.Uri(true), value, value.Type(), ""}
	})
	ls, err := f.acquireByPath(ctx, -1, f.user, false, fs.LockApp(fs.ApplicationDelete), lockTargets...)
	defer func() { _ = f.Release(ctx, ls) }()
	if err != nil {
		return nil, nil, err
	}

	deleted := lo.Map(targets, func(target *File, _ int) activity.Entry {
		return f.activityEntry(activity.FileDelete, target, nil)
	})

	fc, tx, ctx, err := inventory.WithTx(ctx, f.fileClient)
	if err != nil {
		return nil, nil, serializer.NewError(serializer.CodeDBError, "Failed to start transaction", err)
	}

	// Delete targets
	newStaleEntities, storageDiff, indexToDelete, err := f.deleteFiles(ctx, fileNavGroup, fc, opt)
	if err != nil {
		_ = inventory.Rollback(tx)
		return nil, nil, serializer.NewError(serializer.CodeDBError, "failed to delete files", err)
	}

	tx.AppendStorageDiff(storageDiff)
	if err := inventory.CommitWithStorageDiff(ctx, tx, f.l, f.userClient); err != nil {
		return nil, nil, serializer.NewError(serializer.CodeDBError, "Failed to commit delete change", err)
	}
	for _, e := range deleted {
		activity.Record(ctx, e)
	}
	f.emitFileDeleted(ctx, targets...)
	return newStaleEntities, &fs.IndexDiff{
		IndexToDelete: indexToDelete,
	}, ae.Aggregate()
}

func (f *DBFS) VersionControl(ctx context.Context, path *fs.URI, versionId int, delete bool) (*fs.IndexDiff, error) {
	// Get navigator
	navigator, err := f.getNavigator(ctx, path, NavigatorCapabilityVersionControl)
	if err != nil {
		return nil, err
	}

	// Get target file
	ctx = context.WithValue(ctx, inventory.LoadFileEntity{}, true)
	if !delete {
		ctx = context.WithValue(ctx, inventory.LoadFileMetadata{}, true)
	}
	target, err := f.getFileByPath(ctx, navigator, path)
	if err != nil {
		return nil, fmt.Errorf("failed to get target file: %w", err)
	}

	if _, ok := ctx.Value(ByPassOwnerCheckCtxKey{}).(bool); !ok && target.Owner().ID != f.user.ID {
		return nil, fs.ErrOwnerOnly
	}

	// Target must be a file
	if target.Type() != types.FileTypeFile {
		return nil, fs.ErrNotSupportedAction.WithError(fmt.Errorf("target must be a valid file"))
	}

	// Lock file
	ls, err := f.acquireByPath(ctx, -1, f.user, true, fs.LockApp(fs.ApplicationVersionControl),
		&LockByPath{target.Uri(true), target, target.Type(), ""})
	defer func() { _ = f.Release(ctx, ls) }()
	if err != nil {
		return nil, err
	}

	if delete {
		// Fylvo: removing the part of an upload that never finished is no deleted version.
		finished := lo.ContainsBy(target.Entities(), func(e fs.Entity) bool {
			return e.ID() == versionId && e.Type() == types.EntityTypeVersion && e.UploadSessionID() == nil
		})
		storageDiff, err := f.deleteEntity(ctx, target, versionId)
		if err != nil {
			return nil, err
		}
		if finished {
			activity.Record(ctx, f.activityEntry(activity.VersionDelete, target, path))
		}

		if err := f.userClient.ApplyStorageDiff(ctx, storageDiff); err != nil {
			f.l.Error("Failed to apply storage diff after deleting version: %s", err)
		}
		return nil, nil
	} else {
		changed := target.PrimaryEntityID() != versionId
		if err := f.setCurrentVersion(ctx, target, versionId); err != nil {
			return nil, err
		}
		if changed {
			activity.Record(ctx, f.activityEntry(activity.VersionRestore, target, path))
		}

		if _, ok := target.Metadata()[FullTextIndexKey]; ok {
			return &fs.IndexDiff{
				IndexToUpdate: []fs.IndexDiffUpdateDetails{
					{
						Uri:      *target.Uri(false),
						FileID:   target.ID(),
						OwnerID:  target.Owner().ID,
						EntityID: versionId,
					},
				},
			}, nil
		}

		return nil, nil
	}
}

func (f *DBFS) Restore(ctx context.Context, path ...*fs.URI) error {
	ae := serializer.NewAggregateError()
	targets := make([]*File, 0, len(path))
	ctx = context.WithValue(ctx, inventory.LoadFilePublicMetadata{}, true)

	for _, p := range path {
		// Get navigator
		navigator, err := f.getNavigator(ctx, p, NavigatorCapabilityRestore)
		if err != nil {
			ae.Add(p.String(), err)
			continue
		}

		// Get target file
		target, err := f.getFileByPath(ctx, navigator, p)
		if err != nil {
			ae.Add(p.String(), fmt.Errorf("failed to get file: %w", err))
			continue
		}

		targets = append(targets, target)
	}

	if len(targets) == 0 {
		return ae.Aggregate()
	}

	allTrashUriStr := lo.FilterMap(targets, func(t *File, key int) ([]*fs.URI, bool) {
		if restoreUri, ok := t.Metadata()[MetadataRestoreUri]; ok {
			srcUrl, err := fs.NewUriFromString(restoreUri)
			if err != nil {
				ae.Add(t.Uri(false).String(), fs.ErrNotSupportedAction.WithError(fmt.Errorf("invalid restore uri: %w", err)))
				return nil, false
			}

			return []*fs.URI{t.Uri(false), srcUrl.DirUri()}, true
		}

		ae.Add(t.Uri(false).String(), fs.ErrNotSupportedAction.WithError(fmt.Errorf("cannot restore file without required metadata mark")))
		return nil, false
	})

	// Copy each file to its original location
	for _, uris := range allTrashUriStr {
		if _, err := f.MoveOrCopy(ctx, []*fs.URI{uris[0]}, uris[1], false); err != nil {
			if !ae.Merge(err) {
				ae.Add(uris[0].String(), err)
			}
		}
	}

	return ae.Aggregate()

}

func (f *DBFS) MoveOrCopy(ctx context.Context, path []*fs.URI, dst *fs.URI, isCopy bool, opts ...fs.Option) (*fs.IndexDiff, error) {
	o := newDbfsOption()
	for _, opt := range opts {
		o.apply(opt)
	}
	if o.CopyName != "" && (!isCopy || len(path) != 1) {
		return nil, fs.ErrNotSupportedAction.WithError(fmt.Errorf("a name is only given to the copy of one file"))
	}

	targets := make([]*File, 0, len(path))
	dstNavigator, err := f.getNavigator(ctx, dst, NavigatorCapabilityLockFile)
	if err != nil {
		return nil, err
	}

	// Get destination file
	destination, err := f.getFileByPath(ctx, dstNavigator, dst)
	if err != nil {
		return nil, fmt.Errorf("faield to get destination folder: %w", err)
	}

	if !f.canWrite(ctx, destination) {
		return nil, fs.ErrOwnerOnly
	}

	// Target must be a folder
	if !destination.CanHaveChildren() {
		return nil, fs.ErrNotSupportedAction.WithError(fmt.Errorf("destination must be a valid folder"))
	}

	ae := serializer.NewAggregateError()
	fileNavGroup := make(map[Navigator][]*File)
	dstRootPath := destination.Uri(true)
	ctx = context.WithValue(ctx, inventory.LoadFileEntity{}, true)
	ctx = context.WithValue(ctx, inventory.LoadFileMetadata{}, true)

	for _, p := range path {
		// Get navigator
		navigator, err := f.getNavigator(ctx, p, NavigatorCapabilityLockFile)
		if err != nil {
			ae.Add(p.String(), err)
			continue
		}

		// Check fs capability
		if !CanMoveOrCopyTo(p, dst, isCopy) {
			ae.Add(p.String(), fs.ErrNotSupportedAction.WithError(fmt.Errorf("cannot move or copy file form %s to %s", p.String(), dst.String())))
			continue
		}

		// Get target file
		target, err := f.getFileByPath(ctx, navigator, p)
		if err != nil {
			ae.Add(p.String(), fmt.Errorf("failed to get file: %w", err))
			continue
		}

		if !f.canWrite(ctx, target) {
			ae.Add(p.String(), fs.ErrOwnerOnly)
			continue
		}

		// Root folder cannot be moved or copied
		if target.IsRootFolder() {
			ae.Add(p.String(), fs.ErrNotSupportedAction.WithError(fmt.Errorf("cannot move root folder")))
			continue
		}

		// Cannot move or copy folder to its descendant
		if target.Type() == types.FileTypeFolder &&
			dstRootPath.EqualOrIsDescendantOf(target.Uri(true), hashid.EncodeUserID(f.hasher, f.user.ID)) {
			ae.Add(p.String(), fs.ErrNotSupportedAction.WithError(fmt.Errorf("cannot move or copy folder to itself or its descendant")))
			continue
		}

		// Fylvo: the name of the copy is checked as a rename would check it.
		if o.CopyName != "" {
			if err := f.validateCopyName(ctx, target, destination, o.CopyName); err != nil {
				ae.Add(p.String(), err)
				continue
			}
		}

		targets = append(targets, target)
		if isCopy {
			if _, ok := fileNavGroup[navigator]; !ok {
				fileNavGroup[navigator] = make([]*File, 0)
			}
			fileNavGroup[navigator] = append(fileNavGroup[navigator], target)
		}
	}

	indexDiff := &fs.IndexDiff{}
	if len(targets) > 0 {
		// Lock all targets
		lockTargets := lo.Map(targets, func(value *File, key int) *LockByPath {
			return &LockByPath{value.Uri(true), value, value.Type(), ""}
		})

		// Lock destination
		dstBase := destination.Uri(true)
		dstLockTargets := lo.Map(targets, func(value *File, key int) *LockByPath {
			return &LockByPath{dstBase.Join(lo.Ternary(o.CopyName != "", o.CopyName, value.Name())), destination, value.Type(), ""}
		})
		allLockTargets := make([]*LockByPath, 0, len(targets)*2)
		if !isCopy {
			// For moving files from trash bin, also lock the dst with restored name.
			dstRestoreTargets := lo.FilterMap(targets, func(value *File, key int) (*LockByPath, bool) {
				if _, ok := value.Metadata()[MetadataRestoreUri]; ok {
					return &LockByPath{dstBase.Join(value.DisplayName()), destination, value.Type(), ""}, true
				}
				return nil, false
			})
			allLockTargets = append(allLockTargets, lockTargets...)
			allLockTargets = append(allLockTargets, dstRestoreTargets...)
		}
		allLockTargets = append(allLockTargets, dstLockTargets...)
		ls, err := f.acquireByPath(ctx, -1, f.user, false, fs.LockApp(fs.ApplicationMoveCopy), allLockTargets...)
		defer func() { _ = f.Release(ctx, ls) }()
		if err != nil {
			return nil, err
		}

		// Fylvo: where each target comes from, before moving changes it.
		before := lo.Map(targets, func(target *File, _ int) movedFrom {
			_, restored := target.Metadata()[MetadataRestoreUri]
			return movedFrom{parent: target.Model.FileChildren, folder: folderName(target.Parent), restored: restored,
				entry: f.activityEntry(activity.FileMove, target, nil)}
		})

		// Start transaction to move files
		fc, tx, ctx, err := inventory.WithTx(ctx, f.fileClient)
		if err != nil {
			return nil, serializer.NewError(serializer.CodeDBError, "Failed to start transaction", err)
		}

		var (
			copiedNewTargetsMap map[int]*ent.File
			storageDiff         inventory.StorageDiff
			indexDiffBatch      *fs.IndexDiff
		)
		if isCopy {
			copiedNewTargetsMap, storageDiff, indexDiffBatch, err = f.copyFiles(ctx, fileNavGroup, destination, fc, o.CopyName)
		} else {
			storageDiff, indexDiffBatch, err = f.moveFiles(ctx, targets, destination, fc, dstNavigator)
		}

		if err != nil {
			_ = inventory.Rollback(tx)
			return nil, err
		}

		indexDiff.Merge(indexDiffBatch)
		tx.AppendStorageDiff(storageDiff)
		if err := inventory.CommitWithStorageDiff(ctx, tx, f.l, f.userClient); err != nil {
			return nil, serializer.NewError(serializer.CodeDBError, "Failed to commit move change", err)
		}

		for i, target := range targets {
			if isCopy {
				copied := newFile(destination, copiedNewTargetsMap[target.ID()])
				f.emitFileCreated(ctx, copied)
				e := f.activityEntry(activity.FileCopy, copied, dst)
				// Fylvo: the folder it came from only when the copy stays with its owner; the
				// owner of the destination must not learn the name of someone else's folder.
				if target.Model.OwnerID == copied.Model.OwnerID {
					e.Content.FromFolder = before[i].folder
				}
				e.Content.ToFolder = folderName(destination)
				activity.Record(ctx, e)
			} else {
				f.emitFileMoved(ctx, target, destination)
				if e, ok := before[i].moved(destination); ok {
					activity.Record(ctx, e)
				}
			}
		}

		// TODO: after move, dbfs cache should be cleared
	}

	return indexDiff, ae.Aggregate()
}

// validateCopyName checks name as the name of a copy of target in destination, as Rename
// checks a new name.
func (f *DBFS) validateCopyName(ctx context.Context, target, destination *File, name string) error {
	if err := validateFileName(name); err != nil {
		return fs.ErrIllegalObjectName.WithError(err)
	}
	if target.Type() != types.FileTypeFile {
		return nil
	}

	policy, err := f.getPreferredPolicy(ctx, destination)
	if err != nil {
		return err
	}
	if err := validateExtension(name, policy); err != nil {
		return fs.ErrIllegalObjectName.WithError(err)
	}
	if err := validateFileNameRegexp(name, policy); err != nil {
		return fs.ErrIllegalObjectName.WithError(err)
	}
	return nil
}

func (f *DBFS) GetFileFromDirectLink(ctx context.Context, dl *ent.DirectLink) (fs.File, error) {
	fileModel, err := dl.Edges.FileOrErr()
	if err != nil {
		return nil, err
	}

	owner, err := fileModel.Edges.OwnerOrErr()
	if err != nil {
		return nil, err
	}

	// File owner must be active
	if owner.Status != user.StatusActive {
		return nil, fs.ErrDirectLinkInvalid.WithError(fmt.Errorf("file owner is not active"))
	}

	file := newFile(nil, fileModel)

	// Traverse to the root file
	baseNavigator := newBaseNavigator(f.fileClient, defaultFilter, f.user, f.hasher, f.settingClient.DBFS(ctx))
	root, err := baseNavigator.findRoot(ctx, file)
	if err != nil {
		return nil, fmt.Errorf("failed to find root file: %w", err)
	}

	if root.Name() != inventory.RootFolderName {
		return nil, serializer.NewError(serializer.CodeNotFound, "direct link not found", err)
	}

	return file, nil
}

func (f *DBFS) TraverseFile(ctx context.Context, fileID int) (fs.File, error) {
	fileModel, err := f.fileClient.GetByID(ctx, fileID)
	if err != nil {
		return nil, err
	}

	if fileModel.OwnerID != f.user.ID && !f.user.Edges.Group.Permissions.Enabled(int(types.GroupPermissionIsAdmin)) {
		return nil, fs.ErrOwnerOnly.WithError(fmt.Errorf("only file owner can traverse file's uri"))
	}

	file := newFile(nil, fileModel)

	// Traverse to the root file
	baseNavigator := newBaseNavigator(f.fileClient, defaultFilter, f.user, f.hasher, f.settingClient.DBFS(ctx))
	root, err := baseNavigator.findRoot(ctx, file)
	if err != nil {
		return nil, fmt.Errorf("failed to find root file: %w", err)
	}

	rootUri := newMyUri()
	if fileModel.OwnerID != f.user.ID {
		rootUri = newMyIDUri(hashid.EncodeUserID(f.hasher, fileModel.OwnerID))
	}

	if root.Name() != inventory.RootFolderName {
		rootUri = newTrashUri(root.Name())
	}

	navigator, err := f.getNavigator(ctx, rootUri)
	if err != nil {
		return nil, fmt.Errorf("failed to get navigator for root file: %w", err)
	}

	root.Path[pathIndexRoot] = rootUri
	root.Path[pathIndexUser] = rootUri
	file.CapabilitiesBs = navigator.Capabilities(false).Capability

	return file, nil
}

func (f *DBFS) deleteEntity(ctx context.Context, target *File, entityId int) (inventory.StorageDiff, error) {
	if target.PrimaryEntityID() == entityId {
		return nil, fs.ErrNotSupportedAction.WithError(fmt.Errorf("cannot delete current version"))
	}

	targetVersion, found := lo.Find(target.Entities(), func(item fs.Entity) bool {
		return item.ID() == entityId
	})
	if !found {
		return nil, fs.ErrEntityNotExist.WithError(fmt.Errorf("version not found"))
	}

	diff, err := f.fileClient.UnlinkEntity(ctx, targetVersion.Model(), target.Model, target.Owner())
	if err != nil {
		return nil, serializer.NewError(serializer.CodeDBError, "Failed to unlink entity", err)
	}

	if targetVersion.UploadSessionID() != nil {
		// Fylvo: only the marks of this upload, not those of a newer one.
		if err := f.removeUploadMarks(ctx, target, targetVersion.UploadSessionID().String()); err != nil {
			return nil, err
		}
	}

	f.emitFileModified(ctx, target)
	return diff, nil
}

func (f *DBFS) setCurrentVersion(ctx context.Context, target *File, versionId int) error {
	if target.PrimaryEntityID() == versionId {
		return nil
	}

	targetVersion, found := lo.Find(target.Entities(), func(item fs.Entity) bool {
		return item.ID() == versionId && item.Type() == types.EntityTypeVersion && item.UploadSessionID() == nil
	})
	if !found {
		return fs.ErrEntityNotExist.WithError(fmt.Errorf("version not found"))
	}

	fc, tx, ctx, err := inventory.WithTx(ctx, f.fileClient)
	if err != nil {
		return serializer.NewError(serializer.CodeDBError, "Failed to start transaction", err)
	}

	if err := fc.SetPrimaryEntity(ctx, target.Model, targetVersion.Model()); err != nil {
		_ = inventory.Rollback(tx)
		return serializer.NewError(serializer.CodeDBError, "Failed to set primary entity", err)
	}

	// Cap thumbnail entities
	diff, err := fc.CapEntities(ctx, target.Model, target.Owner(), 0, types.EntityTypeThumbnail)
	if err != nil {
		_ = inventory.Rollback(tx)
		return serializer.NewError(serializer.CodeDBError, "Failed to cap thumbnail entities", err)
	}

	tx.AppendStorageDiff(diff)
	if err := inventory.CommitWithStorageDiff(ctx, tx, f.l, f.userClient); err != nil {
		return serializer.NewError(serializer.CodeDBError, "Failed to commit set current version", err)
	}

	f.emitFileModified(ctx, target)
	return nil
}

func (f *DBFS) deleteFiles(ctx context.Context, targets map[Navigator][]*File, fc inventory.FileClient, opt *types.EntityProps) ([]fs.Entity, inventory.StorageDiff, []int, error) {
	if f.user.Edges.Group == nil {
		return nil, nil, nil, fmt.Errorf("user group not loaded")
	}
	allStaleEntities := make([]fs.Entity, 0, len(targets))
	storageDiff := make(inventory.StorageDiff)
	indexToDelete := make([]int, 0)
	for n, files := range targets {
		// Let navigator use tx
		reset, err := n.FollowTx(ctx)
		if err != nil {
			return nil, nil, nil, err
		}

		defer reset()

		remove := func(files []*ent.File) error {
			staleEntities, diff, err := fc.Delete(ctx, files, opt)
			if err != nil {
				return fmt.Errorf("failed to delete files: %w", err)
			}
			storageDiff.Merge(diff)
			allStaleEntities = append(allStaleEntities, lo.Map(staleEntities, func(item *ent.Entity, index int) fs.Entity {
				return fs.NewEntity(item)
			})...)
			return nil
		}

		// Fylvo: files are deleted page by page as the walk passes them, so a big tree is not
		// held in memory. Folders go last: the walk still lists their children.
		folders := make([]*ent.File, 0)
		if err := n.Walk(ctx, files, intsets.MaxInt, intsets.MaxInt, func(targets []*File, level int) error {
			indexToDelete = append(indexToDelete, lo.Map(targets, func(item *File, index int) int {
				return item.ID()
			})...)
			leaves := make([]*ent.File, 0, len(targets))
			for _, target := range targets {
				if target.Type() == types.FileTypeFolder {
					folders = append(folders, target.Model)
				} else {
					leaves = append(leaves, target.Model)
				}
			}
			return remove(leaves)
		}); err != nil {
			return nil, nil, nil, fmt.Errorf("failed to walk files: %w", err)
		}

		if err := remove(folders); err != nil {
			return nil, nil, nil, err
		}
	}

	return allStaleEntities, storageDiff, indexToDelete, nil
}

// copyFiles copies the targets into destination. copyName, if not empty, is the name of the copy
// of the one target.
func (f *DBFS) copyFiles(ctx context.Context, targets map[Navigator][]*File, destination *File, fc inventory.FileClient,
	copyName string) (map[int]*ent.File, inventory.StorageDiff, *fs.IndexDiff, error) {
	if f.user.Edges.Group == nil {
		return nil, nil, nil, fmt.Errorf("user group not loaded")
	}
	limit := max(f.user.Edges.Group.Settings.MaxWalkedFiles, 1)
	capacity, err := f.Capacity(ctx, destination.Owner())
	if err != nil {
		return nil, nil, nil, fmt.Errorf("copy files: failed to destination owner capacity: %w", err)
	}

	dstAncestors := lo.Map(destination.AncestorsChain(), func(item *File, index int) *ent.File {
		return item.Model
	})

	// Fylvo: only the owner decides about shortcuts and system metadata in their tree, as in
	// Create. A copy into the tree of somebody else takes none of them along. The marker of
	// an upload in progress stays, as in any copy: the copy shows that it has no content. The
	// note that an upload made the file does not: the upload did not make the copy.
	excludedMetadata := []string{FullTextIndexKey, MetadataUploadNewFile}
	foreignDestination := !f.isOwnerCall(ctx, destination)
	if foreignDestination {
		excludedMetadata = append(excludedMetadata, MetadataSharedRedirect,
			MetadataSharedOwner, MetadataRestoreUri, MetadataExpectedCollectTime, MetadataRestoreModifiedAt)
	}

	// newTargetsMap is the map of between new target files in first layer, and its src file ID.
	newTargetsMap := make(map[int]*ent.File)
	storageDiff := make(inventory.StorageDiff)
	indexToCopy := make([]fs.IndexDiffCopyDetails, 0)
	for n, files := range targets {
		// Fylvo: the walk passes a level page by page. parentDst holds the copies of the folders
		// of the level above, nextDst collects the copies of the folders of this level.
		parentDst := make(map[int][]*ent.File)
		for _, file := range files {
			parentDst[file.Model.FileChildren] = dstAncestors
		}
		nextDst := make(map[int][]*ent.File)
		dstLevel := 0
		// Let navigator use tx
		reset, err := n.FollowTx(ctx)
		if err != nil {
			return nil, nil, nil, err
		}

		defer reset()

		if err := n.Walk(ctx, files, limit, intsets.MaxInt, func(targets []*File, level int) error {
			// check capacity for each file
			sizeTotal := int64(0)
			for _, file := range targets {
				// Fylvo: a copy for another owner takes the current version only (see Copy).
				if file.OwnerID() == destination.OwnerID() {
					sizeTotal += file.SizeUsed()
				} else {
					sizeTotal += file.Size()
				}
				if foreignDestination && file.IsSymbolic() {
					return fs.ErrOwnerOnly.WithError(fmt.Errorf("a shortcut is not copied into the files of somebody else"))
				}
			}

			if err := f.validateUserCapacityRaw(ctx, sizeTotal, capacity); err != nil {
				return fs.ErrInsufficientCapacity
			}

			limit -= len(targets)
			if level != dstLevel {
				parentDst, nextDst, dstLevel = nextDst, make(map[int][]*ent.File), level
			}
			copied, diff, err := fc.Copy(ctx, &inventory.CopyParameter{
				Files: lo.Map(targets, func(item *File, index int) *ent.File {
					if level == 0 && copyName != "" {
						renamed := *item.Model
						renamed.Name = copyName
						return &renamed
					}
					return item.Model
				}),
				ExcludedMetadataKeys: excludedMetadata,
				DstMap:               parentDst,
			})
			if err != nil {
				return writeError(err, "Failed to copy files")
			}

			storageDiff.Merge(diff)
			for _, file := range targets {
				if level == 0 {
					newTargetsMap[file.ID()] = copied[file.ID()][0]
				}
				if file.Type() == types.FileTypeFolder {
					nextDst[file.ID()] = copied[file.ID()]
				}
			}

			for _, file := range targets {
				if _, ok := file.Metadata()[FullTextIndexKey]; ok {
					copiedFile := copied[file.ID()][0]
					indexToCopy = append(indexToCopy, fs.IndexDiffCopyDetails{
						OriginalFileID: file.ID(),
						FileID:         copiedFile.ID,
						Uri:            *destination.Uri(false).Join(file.Name()),
						EntityID:       copiedFile.PrimaryEntity,
						OwnerID:        destination.OwnerID(),
					})
				}
			}

			capacity.Used += sizeTotal

			return nil
		}); err != nil {
			return nil, nil, nil, fmt.Errorf("failed to walk files: %w", err)
		}
	}

	var indexDiff *fs.IndexDiff
	if len(indexToCopy) > 0 {
		indexDiff = &fs.IndexDiff{
			IndexToCopy: indexToCopy,
		}
	}

	return newTargetsMap, storageDiff, indexDiff, nil
}

func (f *DBFS) moveFiles(ctx context.Context, targets []*File, destination *File, fc inventory.FileClient, n Navigator) (inventory.StorageDiff, *fs.IndexDiff, error) {
	models := lo.Map(targets, func(value *File, key int) *ent.File {
		return value.Model
	})

	// Change targets' parent
	if err := fc.SetParent(ctx, models, destination.Model); err != nil {
		return nil, nil, writeError(err, "Failed to move file")
	}

	var (
		storageDiff inventory.StorageDiff
	)

	// For files moved out from trash bin
	for _, file := range targets {
		if _, ok := file.Metadata()[MetadataRestoreUri]; !ok {
			continue
		}

		// renaming it to its original name
		if _, err := fc.Rename(ctx, file.Model, file.DisplayName()); err != nil {
			return storageDiff, nil, writeError(err, "Failed to rename file from trash bin to its original name")
		}

		if err := restoreModifiedAt(ctx, fc, file); err != nil {
			return storageDiff, nil, serializer.NewError(serializer.CodeDBError, "Failed to restore the date of the file", err)
		}

		// Remove trash bin metadata
		if err := fc.RemoveMetadata(ctx, file.Model, MetadataRestoreUri, MetadataExpectedCollectTime, MetadataRestoreModifiedAt); err != nil {
			return storageDiff, nil, serializer.NewError(serializer.CodeDBError, "Failed to remove trash related metadata", err)
		}
	}

	return storageDiff, nil, nil
}
