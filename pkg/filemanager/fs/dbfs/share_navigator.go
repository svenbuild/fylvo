package dbfs

import (
	"context"
	"fmt"

	"github.com/cloudreve/Cloudreve/v4/application/constants"
	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/boolset"
	"github.com/cloudreve/Cloudreve/v4/pkg/cache"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/hashid"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/cloudreve/Cloudreve/v4/pkg/setting"
)

var (
	ErrShareNotFound = serializer.NewError(serializer.CodeNotFound, "Shared file does not exist", nil)
	ErrNotPurchased  = serializer.NewError(serializer.CodePurchaseRequired, "You need to purchased this share", nil)
)

const (
	PurchaseTicketHeader = constants.CrHeaderPrefix + "Purchase-Ticket"
)

var (
	shareNavigatorCapability       = &boolset.BooleanSet{}
	shareEditorNavigatorCapability = &boolset.BooleanSet{}
)

// NewShareNavigator creates a navigator for user's "shared" file system.
func NewShareNavigator(u *ent.User, fileClient inventory.FileClient, shareClient inventory.ShareClient,
	l logging.Logger, config *setting.DBFS, hasher hashid.Encoder) Navigator {
	n := &shareNavigator{
		user:        u,
		l:           l,
		fileClient:  fileClient,
		shareClient: shareClient,
		config:      config,
	}
	n.baseNavigator = newBaseNavigator(fileClient, defaultFilter, u, hasher, config)
	return n
}

type (
	shareNavigator struct {
		l           logging.Logger
		user        *ent.User
		fileClient  inventory.FileClient
		shareClient inventory.ShareClient
		config      *setting.DBFS

		*baseNavigator
		shareRoot       *File
		singleFileShare bool
		ownerRoot       *File
		share           *ent.Share
		owner           *ent.User
		// Fylvo: the current user is a member with edit right on this shared folder.
		editable       bool
		disableRecycle bool
		persist        func()
	}

	shareNavigatorState struct {
		ShareRoot       *File
		OwnerRoot       *File
		SingleFileShare bool
		Share           *ent.Share
		Owner           *ent.User
	}
)

func (n *shareNavigator) PersistState(kv cache.Driver, key string) {
	n.disableRecycle = true
	n.persist = func() {
		// Fylvo: never cache access to a member-only share, so that removing a member
		// or changing a right takes effect with the next request.
		if n.share == nil || n.share.Props.IsMemberOnly() {
			return
		}

		kv.Set(key, shareNavigatorState{
			ShareRoot:       n.shareRoot,
			OwnerRoot:       n.ownerRoot,
			SingleFileShare: n.singleFileShare,
			Share:           n.share,
			Owner:           n.owner,
		}, ContextHintTTL)
	}
}

func (n *shareNavigator) RestoreState(s State) error {
	n.disableRecycle = true
	if state, ok := s.(shareNavigatorState); ok {
		// Such a state is never written, see PersistState. This only makes sure of it.
		if state.Share == nil || state.Share.Props.IsMemberOnly() {
			return fmt.Errorf("state of a member-only share is not restored")
		}

		n.shareRoot = state.ShareRoot
		n.ownerRoot = state.OwnerRoot
		n.singleFileShare = state.SingleFileShare
		n.share = state.Share
		n.owner = state.Owner
		return nil
	}

	return fmt.Errorf("invalid state type: %T", s)
}

func (n *shareNavigator) Recycle() {
	if n.persist != nil {
		n.persist()
		n.persist = nil
	}

	if !n.disableRecycle {
		if n.ownerRoot != nil {
			n.ownerRoot.Recycle()
		} else if n.shareRoot != nil {
			n.shareRoot.Recycle()
		}
	}
}

// Root loads the share and checks that the current user may open it. It is the only way
// into a share: every file under it is reached by walking down from the returned root.
// The navigator is only changed once all checks have passed.
func (n *shareNavigator) Root(ctx context.Context, path *fs.URI) (*File, error) {
	ctx = context.WithValue(ctx, inventory.LoadShareUser{}, true)
	ctx = context.WithValue(ctx, inventory.LoadUserGroup{}, true)
	ctx = context.WithValue(ctx, inventory.LoadShareFile{}, true)
	share, err := n.shareClient.GetByHashID(ctx, path.ID(hashid.EncodeUserID(n.hasher, n.user.ID)))
	if err != nil {
		return nil, ErrShareNotFound.WithError(err)
	}

	if err := inventory.IsValidShare(share); err != nil {
		return nil, ErrShareNotFound.WithError(err)
	}

	owner := share.Edges.User

	// Fylvo: a member-only share is open to its owner and to the listed members, nobody else.
	member := share.Props.Member(n.user.ID)
	if share.Props.IsMemberOnly() && n.user.ID != owner.ID && member == nil {
		return nil, ErrShareNotFound
	}

	// Check password
	if share.Password != "" && share.Password != path.Password() {
		return nil, ErrShareIncorrectPassword
	}

	// Share permission setting should overwrite root folder's permission
	shareRoot := newFile(nil, share.Edges.File)

	// Find the user side root of the file.
	ownerRoot, err := n.findRoot(ctx, shareRoot)
	if err != nil {
		return nil, err
	}

	singleFileShare := shareRoot.Type() == types.FileTypeFile
	if singleFileShare {
		shareRoot = shareRoot.Parent
	}

	// Check if any ancestors is deleted
	if ownerRoot.Name() != inventory.RootFolderName {
		return nil, ErrShareNotFound
	}

	if n.user.ID != owner.ID && !n.user.Edges.Group.Permissions.Enabled(int(types.GroupPermissionShareDownload)) {
		if inventory.IsAnonymousUser(n.user) {
			return nil, serializer.NewError(
				serializer.CodeAnonymouseAccessDenied,
				fmt.Sprintf("You don't have permission to access share links"),
				err,
			)
		}

		return nil, serializer.NewError(
			serializer.CodeNoPermissionErr,
			fmt.Sprintf("You don't have permission to access share links"),
			err,
		)
	}

	n.owner = owner
	n.share = share
	n.singleFileShare = singleFileShare
	// Fylvo: only a whole folder can be opened for editing. With a single shared file the
	// root is the folder that holds it, and that folder is not shared.
	n.editable = member != nil && member.CanEdit && !singleFileShare

	shareRoot.Path[pathIndexUser] = path.Root()
	shareRoot.OwnerModel = owner
	shareRoot.IsUserRoot = true
	shareRoot.editable = n.editable
	shareRoot.disableView = (share.Props == nil || !share.Props.ShareView) && n.user.ID != owner.ID
	shareRoot.CapabilitiesBs = n.Capabilities(false).Capability

	n.shareRoot = shareRoot
	n.ownerRoot = ownerRoot
	n.ownerRoot.Path[pathIndexRoot] = newMyIDUri(hashid.EncodeUserID(n.hasher, owner.ID))
	return n.shareRoot, nil
}

func (n *shareNavigator) To(ctx context.Context, path *fs.URI) (*File, error) {
	if n.shareRoot == nil {
		if _, err := n.Root(ctx, path); err != nil {
			return nil, err
		}
	}

	current, lastAncestor := n.shareRoot, n.shareRoot
	elements := path.Elements()

	// If target is root of single file share, the root itself is the target.
	if len(elements) == 1 && n.singleFileShare {
		file, err := n.latestSharedSingleFile(ctx)
		if err != nil {
			return nil, err
		}

		if len(elements) == 1 && file.Name() != elements[0] {
			return nil, fs.ErrPathNotExist
		}

		return file, nil
	}

	// Fylvo: the root of a single file share is the folder that holds the file. Only the
	// file itself is shared, so nothing deeper may be reached through that folder.
	if len(elements) > 1 && n.singleFileShare {
		return nil, fs.ErrPathNotExist
	}

	var err error
	for index, element := range elements {
		lastAncestor = current
		current, err = n.walkNext(ctx, current, element, index == len(elements)-1)
		if err != nil {
			return lastAncestor, fmt.Errorf("failed to walk into %q: %w", element, err)
		}
	}

	return current, nil
}

func (n *shareNavigator) walkNext(ctx context.Context, root *File, next string, isLeaf bool) (*File, error) {
	nextFile, err := n.baseNavigator.walkNext(ctx, root, next, isLeaf)
	if err != nil {
		return nil, err
	}

	return nextFile, nil
}

func (n *shareNavigator) Children(ctx context.Context, parent *File, args *ListArgs) (*ListResult, error) {
	if n.singleFileShare {
		file, err := n.latestSharedSingleFile(ctx)
		if err != nil {
			return nil, err
		}

		return &ListResult{
			Files:          []*File{file},
			Pagination:     &inventory.PaginationResults{},
			SingleFileView: true,
		}, nil
	}

	return n.baseNavigator.children(ctx, parent, args)
}

func (n *shareNavigator) latestSharedSingleFile(ctx context.Context) (*File, error) {
	if n.singleFileShare {
		file, err := n.fileClient.GetByID(ctx, n.share.Edges.File.ID)
		if err != nil {
			return nil, err
		}

		f := newFile(n.shareRoot, file)
		f.OwnerModel = n.shareRoot.OwnerModel

		return f, nil
	}

	return nil, fs.ErrPathNotExist
}

func (n *shareNavigator) Capabilities(isSearching bool) *fs.NavigatorProps {
	capability := shareNavigatorCapability
	if n.editable {
		capability = shareEditorNavigatorCapability
	}

	res := &fs.NavigatorProps{
		Capability:            capability,
		OrderDirectionOptions: fullOrderDirectionOption,
		OrderByOptions:        fullOrderByOption,
		MaxPageSize:           n.config.MaxPageSize,
	}

	if isSearching {
		res.OrderByOptions = nil
		res.OrderDirectionOptions = nil
	}

	return res
}

func (n *shareNavigator) FollowTx(ctx context.Context) (func(), error) {
	if _, ok := ctx.Value(inventory.TxCtx{}).(*inventory.Tx); !ok {
		return nil, fmt.Errorf("navigator: no inherited transaction found in context")
	}
	newFileClient, _, _, err := inventory.WithTx(ctx, n.fileClient)
	if err != nil {
		return nil, err
	}

	newSharClient, _, _, err := inventory.WithTx(ctx, n.shareClient)

	oldFileClient, oldShareClient := n.fileClient, n.shareClient
	revert := func() {
		n.fileClient = oldFileClient
		n.shareClient = oldShareClient
		n.baseNavigator.fileClient = oldFileClient
	}

	n.fileClient = newFileClient
	n.shareClient = newSharClient
	n.baseNavigator.fileClient = newFileClient
	return revert, nil
}

func (n *shareNavigator) ExecuteHook(ctx context.Context, hookType fs.HookType, file *File) error {
	switch hookType {
	case fs.HookTypeBeforeDownload:
		return n.shareClient.Downloaded(ctx, n.share)
	}
	return nil
}

func (n *shareNavigator) Walk(ctx context.Context, levelFiles []*File, limit, depth int, f WalkFunc) error {
	return n.baseNavigator.walk(ctx, levelFiles, limit, depth, f)
}

func (n *shareNavigator) GetView(ctx context.Context, file *File) *types.ExplorerView {
	return file.View()
}
