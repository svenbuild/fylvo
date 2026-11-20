package fylvo

import (
	"context"
	"time"

	"github.com/cloudreve/Cloudreve/v4/application/constants"
	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/ent/user"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/activity"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/manager"
	"github.com/cloudreve/Cloudreve/v4/pkg/hashid"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

// The activity log as seen by users and the admin. Who may see what:
//   - The history of a file or folder: only its owner, opened through the owner's own files or
//     trash. Members of a shared folder and guests of a link get none. No address or device.
//   - Recent sign-ins: everyone sees those of their own account, with address and device.
//   - The admin sees everything.

const activityPageSize = 50

type (
	// ActivityUser is an account named in an entry.
	ActivityUser struct {
		ID    string `json:"id"`
		Nick  string `json:"nickname"`
		Email string `json:"email,omitempty"`
	}

	ActivityEntry struct {
		ID        int                    `json:"id"`
		Type      int                    `json:"type"`
		CreatedAt time.Time              `json:"created_at"`
		Actor     *ActivityUser          `json:"actor,omitempty"`
		Owner     *ActivityUser          `json:"owner,omitempty"`
		Guest     bool                   `json:"guest,omitempty"`
		System    bool                   `json:"system,omitempty"`
		Via       string                 `json:"via,omitempty"`
		IP        string                 `json:"ip,omitempty"`
		UserAgent string                 `json:"user_agent,omitempty"`
		FileID    string                 `json:"file_id,omitempty"`
		Content   *types.ActivityContent `json:"content,omitempty"`
	}

	ListActivityResponse struct {
		Entries []ActivityEntry `json:"entries"`
		// Next is passed back as "next" for the following page; 0 means no more entries.
		Next int `json:"next,omitempty"`
	}

	// buildOptions tells what an entry may show to the reader.
	buildOptions struct {
		withAddress bool // IP and user agent
		withOwner   bool
		withEmail   bool
	}
)

type (
	FileActivityParamCtx struct{}
	FileActivityService  struct {
		Uri  string `form:"uri" binding:"required"`
		Next int    `form:"next"`
	}
)

// List returns the history of a file or folder; a folder also shows what happened to the files
// directly in it.
func (s *FileActivityService) List(c *gin.Context) (*ListActivityResponse, error) {
	dep := dependency.FromContext(c)
	u := inventory.UserFromContext(c)
	if inventory.IsAnonymousUser(u) {
		return nil, serializer.NewError(serializer.CodeCheckLogin, "Login required", nil)
	}

	uri, err := fs.NewUriFromString(s.Uri)
	if err != nil {
		return nil, serializer.NewError(serializer.CodeParamErr, "unknown uri", err)
	}

	// Only through the owner's own files and trash: never a share, neither a link nor a shared
	// folder of someone else.
	if fsType := uri.FileSystem(); fsType != constants.FileSystemMy && fsType != constants.FileSystemTrash {
		return nil, serializer.NewError(serializer.CodeNoPermissionErr, "Only the owner sees the history", nil)
	}

	m := manager.NewFileManager(dep, u)
	defer m.Recycle()
	file, err := m.Get(c, uri)
	if err != nil {
		return nil, serializer.NewError(serializer.CodeNotFound, "File not found", err)
	}
	if file.OwnerID() != u.ID {
		return nil, serializer.NewError(serializer.CodeNoPermissionErr, "Only the owner sees the history", nil)
	}

	res, err := activityClient(dep).List(c, &inventory.ListActivityArgs{
		FileID:   file.ID(),
		Folder:   file.Type() == types.FileTypeFolder,
		OwnerID:  u.ID,
		Before:   s.Next,
		PageSize: activityPageSize,
	})
	if err != nil {
		return nil, serializer.NewError(serializer.CodeDBError, "Failed to list activity", err)
	}

	return buildActivity(c, dep, res, buildOptions{}), nil
}

type (
	OwnActivityParamCtx struct{}
	OwnActivityService  struct {
		Next int `form:"next"`
	}
)

// List returns the recent sign-ins and security changes of the current account.
func (s *OwnActivityService) List(c *gin.Context) (*ListActivityResponse, error) {
	dep := dependency.FromContext(c)
	u := inventory.UserFromContext(c)

	res, err := activityClient(dep).List(c, &inventory.ListActivityArgs{
		OwnerID:  u.ID,
		Types:    activity.AccountTypes,
		Before:   s.Next,
		PageSize: activityPageSize,
	})
	if err != nil {
		return nil, serializer.NewError(serializer.CodeDBError, "Failed to list activity", err)
	}

	return buildActivity(c, dep, res, buildOptions{withAddress: true}), nil
}

type (
	AdminActivityParamCtx struct{}
	AdminActivityService  struct {
		Next int `form:"next"`
		// Account is the hash ID of an account the entries are done by or about.
		Account string `form:"account"`
		// Group filters by kind of entry: "files", "shares", "account".
		Group string `form:"group"`
	}
)

// Kinds of entries for the admin filter.
var activityGroups = map[string][]int{
	"files": {int(activity.FileCreate), int(activity.FileUpload), int(activity.FileUpdate), int(activity.FileRename),
		int(activity.FileMove), int(activity.FileCopy), int(activity.FileTrash), int(activity.FileRestore),
		int(activity.FileDelete), int(activity.VersionRestore), int(activity.VersionDelete)},
	"shares":  {int(activity.ShareCreate), int(activity.ShareUpdate), int(activity.ShareDelete), int(activity.GuestDownload)},
	"account": append([]int{int(activity.AccountDelete)}, activity.AccountTypes...),
}

// List returns all entries, for the admin.
func (s *AdminActivityService) List(c *gin.Context) (*ListActivityResponse, error) {
	dep := dependency.FromContext(c)
	args := &inventory.ListActivityArgs{Before: s.Next, PageSize: activityPageSize}
	if s.Account != "" {
		uid, err := dep.HashIDEncoder().Decode(s.Account, hashid.UserID)
		if err != nil {
			return nil, serializer.NewError(serializer.CodeParamErr, "unknown account", err)
		}
		args.Account = uid
	}
	if s.Group != "" {
		group, ok := activityGroups[s.Group]
		if !ok {
			return nil, serializer.NewError(serializer.CodeParamErr, "unknown group", nil)
		}
		args.Types = group
	}

	res, err := activityClient(dep).List(c, args)
	if err != nil {
		return nil, serializer.NewError(serializer.CodeDBError, "Failed to list activity", err)
	}

	return buildActivity(c, dep, res, buildOptions{withAddress: true, withOwner: true, withEmail: true}), nil
}

func activityClient(dep dependency.Dep) inventory.ActivityClient {
	return inventory.NewActivityClient(dep.DBClient())
}

// buildActivity turns entries into responses, with the names of the accounts in them.
func buildActivity(ctx context.Context, dep dependency.Dep, res *inventory.ListActivityResult, o buildOptions) *ListActivityResponse {
	hasher := dep.HashIDEncoder()
	ids := make([]int, 0, len(res.Entries)*2)
	for _, e := range res.Entries {
		ids = append(ids, e.UserID)
		if o.withOwner {
			ids = append(ids, e.OwnerID)
		}
	}
	users := loadUsers(ctx, dep, lo.Uniq(lo.Without(ids, 0)))
	account := func(id int) *ActivityUser {
		if id == 0 {
			return nil
		}
		au := &ActivityUser{ID: hashid.EncodeUserID(hasher, id)}
		if u, ok := users[id]; ok {
			au.Nick = u.Nick
			if o.withEmail {
				au.Email = u.Email
			}
		}
		return au
	}

	out := &ListActivityResponse{Entries: make([]ActivityEntry, 0, len(res.Entries)), Next: res.Next}
	for _, e := range res.Entries {
		entry := ActivityEntry{
			ID:        e.ID,
			Type:      e.Type,
			CreatedAt: e.CreatedAt,
			Actor:     account(e.UserID),
			Via:       e.Via,
			Content:   e.Content,
			System:    e.Via == activity.ViaSystem,
			Guest:     e.UserID == 0 && e.ShareID != 0,
		}
		if e.FileID != 0 {
			entry.FileID = hashid.EncodeFileID(hasher, e.FileID)
		}
		if o.withOwner {
			entry.Owner = account(e.OwnerID)
		}
		if o.withAddress {
			entry.IP = e.IP
			entry.UserAgent = e.UserAgent
		}
		out.Entries = append(out.Entries, entry)
	}

	return out
}

func loadUsers(ctx context.Context, dep dependency.Dep, ids []int) map[int]*ent.User {
	if len(ids) == 0 {
		return nil
	}

	// Deleted accounts are named too, their entries stay.
	found, err := dep.DBClient().User.Query().Where(user.IDIn(ids...)).All(ctx)
	if err != nil {
		dep.Logger().Warning("Failed to load accounts for the activity log: %s", err)
		return nil
	}

	return lo.SliceToMap(found, func(u *ent.User) (int, *ent.User) { return u.ID, u })
}
