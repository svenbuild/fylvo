package fylvo

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/cloudreve/Cloudreve/v4/application/constants"
	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs/dbfs"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/manager"
	"github.com/cloudreve/Cloudreve/v4/pkg/hashid"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/cloudreve/Cloudreve/v4/service/explorer"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

// Favorites are private to the account: stored as file IDs in the account's settings, so renaming
// and moving keep them. Reading one never grants anything: the ID is turned into an address the
// person could open anyway (own files, or a shared folder they are a member of), and the file is
// then loaded through the normal file manager as that person, with all its checks.

const favoritesMax = 200

var errFavoriteUnavailable = errors.New("favorite is not available")

type (
	// FavoriteIDsResponse is what a change of the favorites returns: the starred IDs, newest first.
	FavoriteIDsResponse struct {
		Favorites []string `json:"favorites"`
	}

	FavoritesResponse struct {
		Files       []explorer.FileResponse `json:"files"`
		Unavailable int                     `json:"unavailable"`
	}

	FavoritesAddParamCtx struct{}
	FavoritesAddService  struct {
		Uris []string `json:"uris" binding:"required,min=1,max=200"`
	}

	FavoritesRemoveParamCtx struct{}
	FavoritesRemoveService  struct {
		IDs []string `json:"ids" binding:"required,min=1,max=200"`
	}
)

// favoriteResolver turns file IDs into addresses for one request; it remembers the folders it
// has read, so many favorites in the same folders cost few queries.
type favoriteResolver struct {
	dep    dependency.Dep
	user   *ent.User
	fm     manager.FileManager
	files  map[int]*ent.File
	shares []*ent.Share // member-only shares the user belongs to (several may share a folder)
}

func newFavoriteResolver(dep dependency.Dep, u *ent.User, fm manager.FileManager) *favoriteResolver {
	return &favoriteResolver{dep: dep, user: u, fm: fm, files: make(map[int]*ent.File)}
}

func (r *favoriteResolver) file(ctx context.Context, id int) (*ent.File, error) {
	if f, ok := r.files[id]; ok {
		return f, nil
	}
	f, err := r.dep.FileClient().GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	r.files[id] = f
	return f, nil
}

func (r *favoriteResolver) memberShares(ctx context.Context) ([]*ent.Share, error) {
	if r.shares != nil {
		return r.shares, nil
	}
	shares, err := r.dep.ShareClient().ListForMember(ctx, r.user.ID)
	if err != nil {
		return nil, err
	}
	r.shares = make([]*ent.Share, 0, len(shares))
	for _, share := range shares {
		if share.Edges.File != nil {
			r.shares = append(r.shares, share)
		}
	}
	return r.shares, nil
}

// isMemberShare tells whether the share with the given hash ID is a member-only share the user
// belongs to.
func (r *favoriteResolver) isMemberShare(ctx context.Context, hashID string) (bool, error) {
	id, err := r.dep.HashIDEncoder().Decode(hashID, hashid.ShareID)
	if err != nil {
		return false, nil
	}
	shares, err := r.memberShares(ctx)
	if err != nil {
		return false, err
	}
	for _, share := range shares {
		if share.ID == id {
			return true, nil
		}
	}
	return false, nil
}

// uri returns the address under which the user can open the file, or errFavoriteUnavailable.
func (r *favoriteResolver) uri(ctx context.Context, id int) (*fs.URI, error) {
	// The chain from the file up to its root.
	chain := make([]*ent.File, 0, 8)
	for current := id; ; {
		f, err := r.file(ctx, current)
		if err != nil {
			if ent.IsNotFound(err) {
				return nil, errFavoriteUnavailable
			}
			return nil, err
		}
		chain = append(chain, f)
		if f.FileChildren == 0 {
			break
		}
		current = f.FileChildren
	}

	// A root with a name is a trash root: the file or a folder above it is in the trash.
	root := chain[len(chain)-1]
	if root.Name != inventory.RootFolderName {
		return nil, errFavoriteUnavailable
	}

	names := func(from int) []string {
		elements := make([]string, 0, from)
		for i := from - 1; i >= 0; i-- {
			elements = append(elements, chain[i].Name)
		}
		return elements
	}

	if root.OwnerID == r.user.ID {
		base, err := fs.NewUriFromString(fs.NewMyUri(""))
		if err != nil {
			return nil, err
		}
		return base.Join(names(len(chain) - 1)...), nil
	}

	shares, err := r.memberShares(ctx)
	if err != nil {
		return nil, err
	}
	for i, f := range chain {
		share, ok := lo.Find(shares, func(s *ent.Share) bool { return s.Edges.File.ID == f.ID })
		if !ok {
			continue
		}
		base, err := fs.NewUriFromString(fs.NewShareUri(hashid.EncodeShareID(r.dep.HashIDEncoder(), share.ID), ""))
		if err != nil {
			return nil, err
		}
		return base.Join(names(i)...), nil
	}

	return nil, errFavoriteUnavailable
}

// load opens the file with the given ID as the user and checks that the address still leads to
// that file (another file may have taken the old place).
func (r *favoriteResolver) load(ctx context.Context, id int) (fs.File, error) {
	uri, err := r.uri(ctx, id)
	if err != nil {
		return nil, err
	}

	f, err := r.fm.Get(ctx, uri, dbfs.WithFilePublicMetadata(), dbfs.WithRequiredCapabilities(dbfs.NavigatorCapabilityInfo))
	if err != nil || f == nil || f.ID() != id {
		return nil, errFavoriteUnavailable
	}

	return f, nil
}

// ListFavorites returns the favorites the user can open, newest first. It writes nothing.
func ListFavorites(c *gin.Context) (*FavoritesResponse, error) {
	dep := dependency.FromContext(c)
	u := inventory.UserFromContext(c)
	fm := manager.NewFileManager(dep, u)
	defer fm.Recycle()

	r := newFavoriteResolver(dep, u, fm)
	res := &FavoritesResponse{Files: []explorer.FileResponse{}}
	favorites := favoritesOf(u)
	for i := len(favorites) - 1; i >= 0; i-- {
		f, err := r.load(c, favorites[i].FileID)
		if err != nil {
			if errors.Is(err, errFavoriteUnavailable) {
				res.Unavailable++
				continue
			}
			return nil, serializer.NewError(serializer.CodeDBError, "Failed to load favorites", err)
		}
		res.Files = append(res.Files, *explorer.BuildFileResponse(c, u, f, dep.HashIDEncoder(), nil))
	}

	return res, nil
}

func favoritesOf(u *ent.User) []types.FylvoFavorite {
	if u.Settings == nil {
		return nil
	}
	return u.Settings.FylvoFavorites
}

// Add stars the given files. Every address must be one the user can open as owner or member.
func (s *FavoritesAddService) Add(c *gin.Context) (*FavoriteIDsResponse, error) {
	dep := dependency.FromContext(c)
	u := inventory.UserFromContext(c)
	fm := manager.NewFileManager(dep, u)
	defer fm.Recycle()

	// The slow checks run before the settings lock.
	r := newFavoriteResolver(dep, u, fm)
	ids := make([]int, 0, len(s.Uris))
	for _, raw := range s.Uris {
		uri, err := fs.NewUriFromString(raw)
		if err != nil {
			return nil, paramError("invalid address %q", raw)
		}
		fsType := uri.FileSystem()
		if (fsType != constants.FileSystemMy && fsType != constants.FileSystemShare) || uri.SearchParameters() != nil {
			return nil, paramError("this address cannot be a favorite: %q", raw)
		}
		// Through a share only when it is a shared folder the person is a member of, never a
		// public link (even one to a file they could reach otherwise).
		if fsType == constants.FileSystemShare {
			member, err := r.isMemberShare(c, uri.ID(""))
			if err != nil {
				return nil, serializer.NewError(serializer.CodeDBError, "Failed to read shares", err)
			}
			if !member {
				return nil, paramError("only files in shared folders you are a member of can be favorites: %q", raw)
			}
		}

		f, err := fm.Get(c, uri, dbfs.WithRequiredCapabilities(dbfs.NavigatorCapabilityInfo))
		if err != nil || f == nil {
			return nil, paramError("file not found: %q", raw)
		}
		if f.IsRootFolder() && fsType == constants.FileSystemMy {
			return nil, paramError("the root folder cannot be a favorite")
		}
		// Only what leads back to the same file through the user's own files or a shared folder
		// they are a member of: this refuses public links, the trash and anything else.
		if _, err := r.load(c, f.ID()); err != nil {
			return nil, paramError("this file cannot be a favorite: %q", raw)
		}
		ids = append(ids, f.ID())
	}

	return saveFavorites(c, dep, u, func(favorites []types.FylvoFavorite) ([]types.FylvoFavorite, error) {
		now := time.Now()
		for _, id := range ids {
			if slices.ContainsFunc(favorites, func(f types.FylvoFavorite) bool { return f.FileID == id }) {
				continue
			}
			favorites = append(favorites, types.FylvoFavorite{FileID: id, At: now})
		}
		if len(favorites) > favoritesMax {
			return nil, paramError("at most %d favorites", favoritesMax)
		}
		return favorites, nil
	})
}

// Remove unstars the given files (hash IDs as in prefs.favorites). Unknown IDs are ignored.
func (s *FavoritesRemoveService) Remove(c *gin.Context) (*FavoriteIDsResponse, error) {
	dep := dependency.FromContext(c)
	u := inventory.UserFromContext(c)

	remove := make(map[int]bool, len(s.IDs))
	for _, raw := range s.IDs {
		id, err := dep.HashIDEncoder().Decode(raw, hashid.FileID)
		if err != nil {
			continue
		}
		remove[id] = true
	}

	return saveFavorites(c, dep, u, func(favorites []types.FylvoFavorite) ([]types.FylvoFavorite, error) {
		return slices.DeleteFunc(favorites, func(f types.FylvoFavorite) bool { return remove[f.FileID] }), nil
	})
}

// saveFavorites changes the stored list under the settings lock. Favorites of files that no
// longer exist at all are dropped on the way.
func saveFavorites(c *gin.Context, dep dependency.Dep, u *ent.User,
	change func([]types.FylvoFavorite) ([]types.FylvoFavorite, error)) (*FavoriteIDsResponse, error) {
	ids := make([]int, 0, len(favoritesOf(u)))
	for _, f := range favoritesOf(u) {
		ids = append(ids, f.FileID)
	}
	existing := make(map[int]bool, len(ids))
	for page := 0; len(ids) > 0 && page >= 0; {
		files, next, err := dep.FileClient().GetByIDs(c, ids, page)
		if err != nil {
			return nil, serializer.NewError(serializer.CodeDBError, "Failed to read favorites", err)
		}
		for _, f := range files {
			existing[f.ID] = true
		}
		page = next
	}

	settings, err := dep.UserClient().UpdateSettings(c, u.ID, func(s *types.UserSetting) error {
		// Only drop what was checked above; a favorite added meanwhile stays.
		kept := slices.DeleteFunc(slices.Clone(s.FylvoFavorites), func(f types.FylvoFavorite) bool {
			return slices.Contains(ids, f.FileID) && !existing[f.FileID]
		})
		changed, err := change(kept)
		if err != nil {
			return err
		}
		s.FylvoFavorites = changed
		return nil
	})
	if err != nil {
		return nil, serializer.KeepAppError(err, serializer.CodeDBError, "Failed to save favorites")
	}

	u.Settings = settings
	return &FavoriteIDsResponse{Favorites: favoriteIDs(dep, settings)}, nil
}
