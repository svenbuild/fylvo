package dbfs

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/cloudreve/Cloudreve/v4/application/constants"
	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/boolset"
	"github.com/cloudreve/Cloudreve/v4/pkg/cache"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/hashid"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/cloudreve/Cloudreve/v4/pkg/setting"
	"github.com/samber/lo"
)

var (
	ErrFsNotInitialized = fmt.Errorf("fs not initialized")
	ErrPermissionDenied = serializer.NewError(serializer.CodeNoPermissionErr, "Permission denied", nil)

	ErrShareIncorrectPassword  = serializer.NewError(serializer.CodeIncorrectPassword, "Incorrect share password", nil)
	ErrFileCountLimitedReached = serializer.NewError(serializer.CodeFileCountLimitedReached, "Walked file count reached limit", nil)
	ErrSymbolicFolderFound     = serializer.NewError(serializer.CodeNoPermissionErr, "Symbolic folder cannot be walked into", nil)
	ErrLoginRequired           = serializer.NewError(serializer.CodeCheckLogin, "Login required", nil)
	errRecycledFile            = errors.New("file was already put back for reuse")

	fullOrderByOption          = []string{"name", "size", "updated_at", "created_at"}
	searchLimitedOrderByOption = []string{"created_at"}
	fullOrderDirectionOption   = []string{"asc", "desc"}
)

type (
	// Navigator is a navigator for database file system.
	Navigator interface {
		Recycle()
		// To returns the file by path. If given path is not exist, returns ErrFileNotFound and most-recent ancestor.
		To(ctx context.Context, path *fs.URI) (*File, error)
		// Children returns the children of the parent file.
		Children(ctx context.Context, parent *File, args *ListArgs) (*ListResult, error)
		// Capabilities returns the capabilities of the navigator.
		Capabilities(isSearching bool) *fs.NavigatorProps
		// Walk walks the file tree until limit is reached.
		Walk(ctx context.Context, levelFiles []*File, limit, depth int, f WalkFunc) error
		// PersistState tells navigator to persist the state of the navigator before recycle.
		PersistState(kv cache.Driver, key string)
		// RestoreState restores the state of the navigator.
		RestoreState(s State) error
		// FollowTx let the navigator inherit the transaction. Return a function to reset back to previous DB client.
		FollowTx(ctx context.Context) (func(), error)
		// ExecuteHook performs custom operations before or after certain actions.
		ExecuteHook(ctx context.Context, hookType fs.HookType, file *File) error
		// GetView returns the view setting of the given file.
		GetView(ctx context.Context, file *File) *types.ExplorerView
	}

	State interface{}

	NavigatorCapability int
	ListArgs            struct {
		Page           *inventory.PaginationArgs
		Search         *inventory.SearchFileParameters
		SharedWithMe   bool
		StreamCallback func([]*File)
	}
	// ListResult is the result of a list operation.
	ListResult struct {
		Files                 []*File
		MixedType             bool
		Pagination            *inventory.PaginationResults
		RecursionLimitReached bool
		SingleFileView        bool
	}
	WalkFunc func([]*File, int) error
)

const (
	NavigatorCapabilityCreateFile NavigatorCapability = iota
	NavigatorCapabilityRenameFile
	NavigatorCapability_CommunityPlacehodler1
	NavigatorCapability_CommunityPlacehodler2
	NavigatorCapability_CommunityPlacehodler3
	NavigatorCapability_CommunityPlacehodler4
	NavigatorCapabilityUploadFile
	NavigatorCapabilityDownloadFile
	NavigatorCapabilityUpdateMetadata
	NavigatorCapabilityListChildren
	NavigatorCapabilityGenerateThumb
	NavigatorCapability_CommunityPlacehodler5
	NavigatorCapability_CommunityPlacehodler6
	NavigatorCapability_CommunityPlacehodler7
	NavigatorCapabilityDeleteFile
	NavigatorCapabilityLockFile
	NavigatorCapabilitySoftDelete
	NavigatorCapabilityRestore
	NavigatorCapabilityShare
	NavigatorCapabilityInfo
	NavigatorCapabilityVersionControl
	NavigatorCapability_CommunityPlacehodler8
	NavigatorCapability_CommunityPlacehodler9
	NavigatorCapabilityEnterFolder
	NavigatorCapabilityModifyProps

	searchTokenSeparator = "|"

	// maxParentsPerQuery Fylvo: the most folders whose children one query lists. Each folder is
	// one SQL parameter, and SQLite takes at most 32'766 in one query.
	maxParentsPerQuery = 10000
	// walkPageSize Fylvo: the most entries a walk lists and passes on at once.
	walkPageSize = 1000
)

// folderChunks Fylvo: the folders in groups of at most maxParentsPerQuery, sorted by ID, so a
// search that continues on the next page finds the same groups again.
func folderChunks(folders map[int]*File) [][]*ent.File {
	ids := lo.Keys(folders)
	slices.Sort(ids)
	return lo.Chunk(lo.Map(ids, func(id int, _ int) *ent.File {
		return folders[id].Model
	}), maxParentsPerQuery)
}

// folderGroup Fylvo: the folders a search reads in one group: those with from <= ID < to (to 0:
// no end). With to 0, the group is the next maxParentsPerQuery folders from from on, and to
// becomes the ID of the folder after them (0 if none). A group is fixed by IDs, not by its
// number, so a folder deleted between two pages does not move folders into a group that was
// read already.
func folderGroup(folders map[int]*File, from, to int) ([]*ent.File, int) {
	ids := lo.Filter(lo.Keys(folders), func(id int, _ int) bool { return id >= from && (to == 0 || id < to) })
	slices.Sort(ids)
	if to == 0 && len(ids) > maxParentsPerQuery {
		to = ids[maxParentsPerQuery]
		ids = ids[:maxParentsPerQuery]
	}
	return lo.Map(ids, func(id int, _ int) *ent.File { return folders[id].Model }), to
}

func init() {
	boolset.Sets(map[NavigatorCapability]bool{
		NavigatorCapabilityCreateFile:     true,
		NavigatorCapabilityRenameFile:     true,
		NavigatorCapabilityUploadFile:     true,
		NavigatorCapabilityDownloadFile:   true,
		NavigatorCapabilityUpdateMetadata: true,
		NavigatorCapabilityListChildren:   true,
		NavigatorCapabilityGenerateThumb:  true,
		NavigatorCapabilityDeleteFile:     true,
		NavigatorCapabilityLockFile:       true,
		NavigatorCapabilitySoftDelete:     true,
		NavigatorCapabilityShare:          true,
		NavigatorCapabilityInfo:           true,
		NavigatorCapabilityVersionControl: true,
		NavigatorCapabilityEnterFolder:    true,
		NavigatorCapabilityModifyProps:    true,
	}, myNavigatorCapability)
	boolset.Sets(map[NavigatorCapability]bool{
		NavigatorCapabilityDownloadFile:   true,
		NavigatorCapabilityListChildren:   true,
		NavigatorCapabilityGenerateThumb:  true,
		NavigatorCapabilityLockFile:       true,
		NavigatorCapabilityInfo:           true,
		NavigatorCapabilityVersionControl: true,
		NavigatorCapabilityEnterFolder:    true,
		NavigatorCapabilityModifyProps:    true,
	}, shareNavigatorCapability)
	// Fylvo: a member with edit right on a shared folder. Delete is listed because the clean-up
	// of a failed or cancelled upload removes the placeholder for good; a delete asked for
	// by the member is still refused by the owner check in Delete.
	boolset.Sets(map[NavigatorCapability]bool{
		NavigatorCapabilityCreateFile:     true,
		NavigatorCapabilityRenameFile:     true,
		NavigatorCapabilityUploadFile:     true,
		NavigatorCapabilityDownloadFile:   true,
		NavigatorCapabilityListChildren:   true,
		NavigatorCapabilityGenerateThumb:  true,
		NavigatorCapabilityDeleteFile:     true,
		NavigatorCapabilityLockFile:       true,
		NavigatorCapabilitySoftDelete:     true,
		NavigatorCapabilityInfo:           true,
		NavigatorCapabilityVersionControl: true,
		NavigatorCapabilityEnterFolder:    true,
		NavigatorCapabilityModifyProps:    true,
	}, shareEditorNavigatorCapability)
	// Fylvo: a visitor of a link that takes uploads. New files and folders, nothing that
	// changes what is there. Delete is listed for the clean-up of the visitor's own failed
	// or cancelled upload only, see above.
	boolset.Sets(map[NavigatorCapability]bool{
		NavigatorCapabilityCreateFile:     true,
		NavigatorCapabilityUploadFile:     true,
		NavigatorCapabilityDownloadFile:   true,
		NavigatorCapabilityListChildren:   true,
		NavigatorCapabilityGenerateThumb:  true,
		NavigatorCapabilityDeleteFile:     true,
		NavigatorCapabilityLockFile:       true,
		NavigatorCapabilityInfo:           true,
		NavigatorCapabilityVersionControl: true,
		NavigatorCapabilityEnterFolder:    true,
		NavigatorCapabilityModifyProps:    true,
	}, shareUploadNavigatorCapability)
	// Fylvo: the same visitor on a link that hides the folder (a drop box): nothing that
	// reads a file or tells anything about one.
	boolset.Sets(map[NavigatorCapability]bool{
		NavigatorCapabilityCreateFile:   true,
		NavigatorCapabilityUploadFile:   true,
		NavigatorCapabilityListChildren: true,
		NavigatorCapabilityDeleteFile:   true,
		NavigatorCapabilityLockFile:     true,
		NavigatorCapabilityEnterFolder:  true,
	}, shareDropNavigatorCapability)
	boolset.Sets(map[NavigatorCapability]bool{
		NavigatorCapabilityListChildren: true,
		NavigatorCapabilityDeleteFile:   true,
		NavigatorCapabilityLockFile:     true,
		NavigatorCapabilityRestore:      true,
		NavigatorCapabilityInfo:         true,
	}, trashNavigatorCapability)
	boolset.Sets(map[NavigatorCapability]bool{
		NavigatorCapabilityListChildren: true,
		NavigatorCapabilityDownloadFile: true,
		NavigatorCapabilityEnterFolder:  true,
	}, sharedWithMeNavigatorCapability)
}

// ==================== Base Navigator ====================
type (
	fileFilter    func(ctx context.Context, f *File) (*File, bool)
	baseNavigator struct {
		fileClient inventory.FileClient
		listFilter fileFilter
		user       *ent.User
		hasher     hashid.Encoder
		config     *setting.DBFS
	}
)

var defaultFilter = func(ctx context.Context, f *File) (*File, bool) { return f, true }

func newBaseNavigator(fileClient inventory.FileClient, filterFunc fileFilter, user *ent.User,
	hasher hashid.Encoder, config *setting.DBFS) *baseNavigator {
	return &baseNavigator{
		fileClient: fileClient,
		listFilter: filterFunc,
		user:       user,
		hasher:     hasher,
		config:     config,
	}
}

func (b *baseNavigator) walkNext(ctx context.Context, root *File, next string, isLeaf bool) (*File, error) {
	var model *ent.File
	if root != nil {
		model = root.Model
		// Fylvo: a file that was put back for reuse (Recycle) while a walk still held it ends
		// the walk with an error instead of a crash.
		if model == nil {
			return nil, errRecycledFile
		}
		if root.IsSymbolic() {
			return nil, ErrSymbolicFolderFound
		}

		root.mu.Lock()
		if child, ok := root.Children[next]; ok && !isLeaf {
			root.mu.Unlock()
			return child, nil
		}
		root.mu.Unlock()
	}

	child, err := b.fileClient.GetChildFile(ctx, model, b.user.ID, next, isLeaf)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fs.ErrPathNotExist.WithError(err)
		}

		return nil, fmt.Errorf("faield to get child %q: %w", next, err)
	}

	return newFile(root, child), nil
}

// findRoot finds the root folder of the given child.
func (b *baseNavigator) findRoot(ctx context.Context, child *File) (*File, error) {
	root := child
	for {
		newRoot, err := b.walkUp(ctx, root)
		if err != nil {
			if !ent.IsNotFound(err) {
				return nil, err
			}

			break
		}

		root = newRoot
	}

	return root, nil
}

func (b *baseNavigator) walkUp(ctx context.Context, child *File) (*File, error) {
	parent, err := b.fileClient.GetParentFile(ctx, child.Model, false)
	if err != nil {
		return nil, fmt.Errorf("faield to get Parent for %q: %w", child.Name(), err)
	}

	return newParentFile(parent, child), nil
}

func (b *baseNavigator) children(ctx context.Context, parent *File, args *ListArgs) (*ListResult, error) {
	var model *ent.File
	if parent != nil {
		model = parent.Model
		if parent.Model.Type != int(types.FileTypeFolder) {
			return nil, fs.ErrPathNotExist
		}

		if parent.IsSymbolic() {
			return nil, ErrSymbolicFolderFound
		}

		parent.Path[pathIndexUser] = parent.Uri(false)
	}

	if args.Search != nil {
		return b.search(ctx, parent, args)
	}

	children, err := b.fileClient.GetChildFiles(ctx, &inventory.ListFileParameters{
		PaginationArgs: args.Page,
		SharedWithMe:   args.SharedWithMe,
	}, b.user.ID, model)
	if err != nil {
		return nil, fmt.Errorf("failed to get children: %w", err)
	}

	return &ListResult{
		Files: lo.FilterMap(children.Files, func(model *ent.File, index int) (*File, bool) {
			f := newFile(parent, model)
			return b.listFilter(ctx, f)
		}),
		MixedType:  children.MixedType,
		Pagination: children.PaginationResults,
	}, nil
}

// newWalkedFile Fylvo: a file as newFile makes it, but not kept in the Children of its parent.
// Each file the walk passes would otherwise stay there: the whole tree below the start of the
// walk was held in memory until the end of the request.
func newWalkedFile(parent *File, model *ent.File) *File {
	f := newFile(parent, model)
	parent.mu.Lock()
	if parent.Children[model.Name] == f {
		delete(parent.Children, model.Name)
	}
	parent.mu.Unlock()
	return f
}

// walk passes the files and everything below them to f, level by level, at most limit entries.
// Fylvo: f gets the entries of a level one page at a time (at most walkPageSize), and only the
// folders of a level are kept for the next one (see newWalkedFile). The whole tree used to be
// held, so memory grew with the size of a folder: copying 20'000 files took 230 MB.
func (b *baseNavigator) walk(ctx context.Context, levelFiles []*File, limit, depth int, f WalkFunc) error {
	if len(levelFiles) == 0 {
		return nil
	}

	owner := levelFiles[0].Owner()
	walked := 0
	// emit passes files to f and returns the folders among them to walk into.
	emit := func(files []*File, level int) ([]*File, error) {
		if len(files) == 0 {
			return nil, nil
		}
		stop := false
		if len(files) > limit-walked {
			files = files[:limit-walked]
			stop = true
		}
		if err := f(files, level); err != nil {
			return nil, err
		}
		if stop {
			return nil, ErrFileCountLimitedReached
		}

		walked += len(files)
		return lo.Filter(files, func(f *File, index int) bool {
			return f.Model.Type == int(types.FileTypeFolder) && !f.IsSymbolic()
		}), nil
	}

	folders, err := emit(levelFiles, 0)
	if err != nil {
		return err
	}

	for level := 1; level <= depth && len(folders) > 0 && walked < limit; level++ {
		parents := lo.SliceToMap(folders, func(file *File) (int, *File) {
			return file.Model.ID, file
		})
		folders = nil
		// Fylvo: the children of at most maxParentsPerQuery folders per query, and the page token
		// must survive the inner loop. It was reset in each round, so a level with more entries
		// than one page (about 32'700 on SQLite) read its first page again and again: entries came
		// twice, and a walk without limit (deleting for good) did not end.
		for _, chunk := range folderChunks(parents) {
			token := ""
			for walked < limit {
				res, err := b.fileClient.GetChildFiles(ctx,
					&inventory.ListFileParameters{
						PaginationArgs: &inventory.PaginationArgs{
							UseCursorPagination: true,
							PageToken:           token,
							PageSize:            min(limit-walked, walkPageSize),
						},
						MixedType: true,
					},
					owner.ID,
					chunk...)
				if err != nil {
					return serializer.NewError(serializer.CodeDBError, "Failed to list children", err)
				}

				found, err := emit(lo.Map(res.Files, func(model *ent.File, index int) *File {
					return newWalkedFile(parents[model.FileChildren], model)
				}), level)
				if err != nil {
					return err
				}
				folders = append(folders, found...)

				// All files listed
				if res.NextPageToken == "" {
					break
				}

				token = res.NextPageToken
			}
		}
	}

	if walked >= limit {
		return ErrFileCountLimitedReached
	}

	return nil
}

func (b *baseNavigator) search(ctx context.Context, parent *File, args *ListArgs) (*ListResult, error) {
	if parent == nil {
		// Performs mega search for all files in trash fs.
		children, err := b.fileClient.GetChildFiles(ctx, &inventory.ListFileParameters{
			PaginationArgs: args.Page,
			MixedType:      true,
			Search:         args.Search,
			SharedWithMe:   args.SharedWithMe,
		}, b.user.ID, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to get children: %w", err)
		}

		return &ListResult{
			Files: lo.FilterMap(children.Files, func(model *ent.File, index int) (*File, bool) {
				f := newFile(parent, model)
				return b.listFilter(ctx, f)
			}),
			MixedType:  children.MixedType,
			Pagination: children.PaginationResults,
		}, nil
	}
	// Performs recursive search for all files under the given folder.
	walkedFolder := 1
	parents := []map[int]*File{{parent.Model.ID: parent}}
	startLevel, groupFrom, groupTo, innerPageToken, err := parseSearchPageToken(args.Page.PageToken)
	if err != nil {
		return nil, err
	}
	args.Page.PageToken = innerPageToken

	// Fylvo: the folders of the next level are listed for at most maxParentsPerQuery folders
	// per query, and all their pages go into the one map of that level. A second page used to
	// become a level of its own, and the folder count only grew by the first page.
	stepLevel := func(level int) (bool, error) {
		// We don't need metadata in level search.
		listCtx := context.WithValue(ctx, inventory.LoadFilePublicMetadata{}, nil)
		next := make(map[int]*File)
		for _, chunk := range folderChunks(parents[level]) {
			token := ""
			for walkedFolder <= b.config.MaxRecursiveSearchedFolder {
				res, err := b.fileClient.GetChildFiles(listCtx,
					&inventory.ListFileParameters{
						PaginationArgs: &inventory.PaginationArgs{
							UseCursorPagination: true,
							PageToken:           token,
						},
						FolderOnly: true,
					},
					parent.Model.OwnerID,
					chunk...)
				if err != nil {
					return false, serializer.NewError(serializer.CodeDBError, "Failed to list children", err)
				}

				for _, model := range res.Files {
					p := parents[level][model.FileChildren]
					f := newFile(p, model)
					f.Path[pathIndexUser] = p.Uri(false).Join(model.Name)
					next[model.ID] = f
				}

				walkedFolder += len(res.Files)
				if res.NextPageToken == "" {
					break
				}

				token = res.NextPageToken
			}
		}

		parents = append(parents, next)
		// All possible folders is searched
		return len(next) == 0, nil
	}

	// We need to walk from root folder to get the correct level.
	for level := 0; level < startLevel; level++ {
		stop, err := stepLevel(level)
		if err != nil {
			return nil, err
		}

		if stop {
			return &ListResult{}, nil
		}
	}

	// Search files starting from current level
	res := make([]*File, 0, args.Page.PageSize)
	args.Page.UseCursorPagination = true
	originalPageSize := args.Page.PageSize
	stop := false
	// Fylvo: the folders of a level are searched in groups (folderGroup), from groupFrom to groupTo.
	for len(res) < originalPageSize && walkedFolder <= b.config.MaxRecursiveSearchedFolder {
		var group []*ent.File
		group, groupTo = folderGroup(parents[startLevel], groupFrom, groupTo)
		if len(group) == 0 {
			// The folders of this group are gone since the last page.
			args.Page.PageToken = ""
		} else {
			// Only requires minimum number of files
			args.Page.PageSize = min(originalPageSize, originalPageSize-len(res))
			searchRes, err := b.fileClient.GetChildFiles(ctx,
				&inventory.ListFileParameters{
					PaginationArgs: args.Page,
					MixedType:      true,
					Search:         args.Search,
				},
				parent.Model.OwnerID,
				group...)

			if err != nil {
				return nil, serializer.NewError(serializer.CodeDBError, "Failed to search files", err)
			}

			newRes := lo.FilterMap(searchRes.Files, func(model *ent.File, index int) (*File, bool) {
				p := parents[startLevel][model.FileChildren]
				f := newFile(p, model)
				f.Path[pathIndexUser] = p.Uri(false).Join(model.Name)
				return b.listFilter(ctx, f)
			})
			res = append(res, newRes...)
			if args.StreamCallback != nil {
				args.StreamCallback(newRes)
			}

			args.Page.PageToken = searchRes.NextPageToken
		}
		// If no more results in this group of folders, move to the next group
		if args.Page.PageToken == "" && groupTo != 0 {
			groupFrom, groupTo = groupTo, 0
			continue
		}

		// If no more results under current level, move to next level
		if args.Page.PageToken == "" {
			groupFrom, groupTo = 0, 0
			if len(res) == originalPageSize {
				// Current page is full, no need to search more
				startLevel++
				break
			}

			finished, err := stepLevel(startLevel)
			if err != nil {
				return nil, err
			}

			if finished {
				stop = true
				// No more folders under next level, all result is presented
				break
			}

			startLevel++
		}
	}

	if args.StreamCallback != nil {
		// Clear res if it's streamed
		res = res[:0]
	}

	searchRes := &ListResult{
		Files:                 res,
		MixedType:             true,
		Pagination:            &inventory.PaginationResults{IsCursor: true},
		RecursionLimitReached: walkedFolder > b.config.MaxRecursiveSearchedFolder,
	}

	if walkedFolder <= b.config.MaxRecursiveSearchedFolder && !stop {
		searchRes.Pagination.NextPageToken = strings.Join([]string{strconv.Itoa(startLevel), strconv.Itoa(groupFrom),
			strconv.Itoa(groupTo), args.Page.PageToken}, searchTokenSeparator)
	}

	return searchRes, nil
}

// parseSearchPageToken reads "level|from|to|inner token" (Fylvo: with the group of folders, see
// folderGroup).
func parseSearchPageToken(token string) (level, from, to int, inner string, err error) {
	if token == "" {
		return 0, 0, 0, "", nil
	}

	tokens := strings.Split(token, searchTokenSeparator)
	if len(tokens) != 4 {
		return 0, 0, 0, "", fmt.Errorf("invalid page token")
	}

	numbers := make([]int, 3)
	for i := range numbers {
		numbers[i], err = strconv.Atoi(tokens[i])
		if err != nil || numbers[i] < 0 {
			return 0, 0, 0, "", fmt.Errorf("invalid page token")
		}
	}

	return numbers[0], numbers[1], numbers[2], tokens[3], nil
}

func newMyUri() *fs.URI {
	res, _ := fs.NewUriFromString(constants.CloudreveScheme + "://" + string(constants.FileSystemMy))
	return res
}

func newMyIDUri(uid string) *fs.URI {
	res, _ := fs.NewUriFromString(fmt.Sprintf("%s://%s@%s", constants.CloudreveScheme, uid, constants.FileSystemMy))
	return res
}

func newTrashUri(name string) *fs.URI {
	res, _ := fs.NewUriFromString(fmt.Sprintf("%s://%s", constants.CloudreveScheme, constants.FileSystemTrash))
	return res.Join(name)
}

func newSharedWithMeUri(id string) *fs.URI {
	res, _ := fs.NewUriFromString(fmt.Sprintf("%s://%s", constants.CloudreveScheme, constants.FileSystemSharedWithMe))
	return res.Join(id)
}
