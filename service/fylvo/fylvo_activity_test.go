package fylvo

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/ent/enttest"
	"github.com/cloudreve/Cloudreve/v4/ent/user"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/activity"
	"github.com/cloudreve/Cloudreve/v4/pkg/cache"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/manager"
	"github.com/cloudreve/Cloudreve/v4/pkg/hashid"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/cloudreve/Cloudreve/v4/pkg/util"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Fylvo: who sees which activity log entries.

type activityEnv struct {
	t      *testing.T
	ctx    context.Context
	dep    dependency.Dep
	client *ent.Client
	hasher hashid.Encoder

	owner, member, outsider, anonymous *ent.User
	sharedID, docID, privateID         int
	memberShare, link                  string
}

func newActivityEnv(t *testing.T) *activityEnv {
	logger := logging.NewConsoleLogger(logging.LevelError)
	kv := cache.NewMemoStore("", logger)
	client := enttest.Open(t, "sqlite3", filepath.Join(t.TempDir(), "fylvo.db"))
	t.Cleanup(func() { _ = client.Close() })
	client, err := inventory.InitializeDBClient(logger, client, kv, "4.19.1")
	require.NoError(t, err)
	hasher, err := hashid.New("fylvo-test")
	require.NoError(t, err)

	dep := dependency.NewDependency(
		dependency.WithLogger(logger),
		dependency.WithConfigPath(filepath.Join(t.TempDir(), "conf.ini")),
		dependency.WithDbClient(client),
		dependency.WithKV(kv),
		dependency.WithHashIDEncoder(hasher),
	)
	ctx := context.WithValue(context.Background(), dependency.DepCtx{}, dep)
	ctx = context.WithValue(ctx, logging.LoggerCtx{}, logger)

	e := &activityEnv{t: t, ctx: ctx, dep: dep, client: client, hasher: hasher}
	e.owner = e.newUser("owner")
	e.member = e.newUser("member")
	e.outsider = e.newUser("outsider")
	e.anonymous, err = dep.UserClient().AnonymousUser(ctx)
	require.NoError(t, err)

	owner := manager.NewFileManager(dep, e.owner)
	shared, err := owner.Create(ctx, e.uri("cloudreve://my/Shared"), types.FileTypeFolder)
	require.NoError(t, err)
	doc, err := owner.Create(ctx, e.uri("cloudreve://my/Shared/doc.txt"), types.FileTypeFile)
	require.NoError(t, err)
	private, err := owner.Create(ctx, e.uri("cloudreve://my/Private"), types.FileTypeFolder)
	require.NoError(t, err)
	e.sharedID, e.docID, e.privateID = shared.ID(), doc.ID(), private.ID()

	members, err := dep.ShareClient().Upsert(ctx, &inventory.CreateShareParams{OwnerID: e.owner.ID, FileID: e.sharedID,
		Props: &types.ShareProps{MemberOnly: true, Members: []types.ShareMember{{UserID: e.member.ID, CanEdit: true}}}})
	require.NoError(t, err)
	link, err := dep.ShareClient().Upsert(ctx, &inventory.CreateShareParams{OwnerID: e.owner.ID, FileID: e.sharedID,
		Props: &types.ShareProps{AllowUpload: true}})
	require.NoError(t, err)
	e.memberShare = fs.NewShareUri(hashid.EncodeShareID(hasher, members.ID), "")
	e.link = fs.NewShareUri(hashid.EncodeShareID(hasher, link.ID), "")
	return e
}

func (e *activityEnv) newUser(name string) *ent.User {
	created, err := e.client.User.Create().SetEmail(name + "@fylvo.test").SetNick(name).
		SetStatus(user.StatusActive).SetGroupID(2).SetSettings(&types.UserSetting{}).Save(e.ctx)
	require.NoError(e.t, err)
	loaded, err := e.dep.UserClient().GetByID(context.WithValue(e.ctx, inventory.LoadUserGroup{}, true), created.ID)
	require.NoError(e.t, err)
	return loaded
}

func (e *activityEnv) uri(raw string) *fs.URI {
	parsed, err := fs.NewUriFromString(raw)
	require.NoError(e.t, err)
	return parsed
}

// as returns a request of u.
func (e *activityEnv) as(u *ent.User) *gin.Context {
	c, engine := gin.CreateTestContext(httptest.NewRecorder())
	engine.ContextWithFallback = true
	c.Request = httptest.NewRequest("GET", "/api/v4/file/activity", nil)
	util.WithValue(c, dependency.DepCtx{}, e.dep)
	util.WithValue(c, inventory.UserCtx{}, u)
	return c
}

// add stores an entry directly, as the writer would.
func (e *activityEnv) add(typ activity.Type, actor, owner, file, parent int, name string) {
	q := e.client.Activity.Create().SetType(int(typ)).SetCreatedAt(time.Now()).
		SetIP("192.0.2.1").SetUserAgent("agent").SetContent(&types.ActivityContent{Name: name})
	if actor != 0 {
		q.SetUserID(actor)
	}
	if owner != 0 {
		q.SetOwnerID(owner)
	}
	if file != 0 {
		q.SetFileID(file)
	}
	if parent != 0 {
		q.SetParentID(parent)
	}
	require.NoError(e.t, q.Exec(e.ctx))
}

func names(res *ListActivityResponse) []string {
	out := make([]string, 0, len(res.Entries))
	for _, entry := range res.Entries {
		out = append(out, entry.Content.Name)
	}
	return out
}

func requireAppCode(t *testing.T, err error, code int) {
	t.Helper()
	var appErr serializer.AppError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, code, appErr.ErrCode())
}

func TestFileActivityOnlyForTheOwner(t *testing.T) {
	e := newActivityEnv(t)
	owner, member := e.owner.ID, e.member.ID
	e.add(activity.FileUpload, member, owner, e.docID, e.sharedID, "doc.txt")      // by the member, in the folder
	e.add(activity.FileRename, owner, owner, e.sharedID, 0, "Shared")              // the folder itself
	e.add(activity.FileTrash, member, owner, 999, e.sharedID, "gone.txt")          // a child that is gone
	e.add(activity.FileUpload, owner, owner, 998, e.privateID, "secret.txt")       // elsewhere
	e.add(activity.Login, owner, owner, 0, 0, "")                                  // no file
	e.add(activity.FileUpload, e.outsider.ID, e.outsider.ID, e.sharedID, 0, "odd") // someone else's entry

	res, err := (&FileActivityService{Uri: "cloudreve://my/Shared"}).List(e.as(e.owner))
	require.NoError(t, err)
	require.Equal(t, []string{"gone.txt", "Shared", "doc.txt"}, names(res))
	for _, entry := range res.Entries {
		require.Empty(t, entry.IP, "no address in the history")
		require.Empty(t, entry.UserAgent)
		require.Nil(t, entry.Owner)
	}
	require.Equal(t, "member", res.Entries[0].Actor.Nick)
	require.Empty(t, res.Entries[0].Actor.Email)

	res, err = (&FileActivityService{Uri: "cloudreve://my/Shared/doc.txt"}).List(e.as(e.owner))
	require.NoError(t, err)
	require.Equal(t, []string{"doc.txt"}, names(res))

	// Members and guests get no history, through no address.
	for _, u := range []*ent.User{e.member, e.outsider, e.anonymous} {
		for _, raw := range []string{e.memberShare, e.memberShare + "/doc.txt", e.link, e.link + "/doc.txt",
			"cloudreve://shared_with_me", "cloudreve://my/Shared"} {
			_, err := (&FileActivityService{Uri: raw}).List(e.as(u))
			require.Error(t, err, "%s via %s", u.Nick, raw)
		}
	}
	_, err = (&FileActivityService{Uri: e.memberShare}).List(e.as(e.member))
	requireAppCode(t, err, serializer.CodeNoPermissionErr)
	_, err = (&FileActivityService{Uri: "cloudreve://my"}).List(e.as(e.anonymous))
	requireAppCode(t, err, serializer.CodeCheckLogin)
}

func TestFileActivityPages(t *testing.T) {
	e := newActivityEnv(t)
	for i := 0; i < activityPageSize+3; i++ {
		e.add(activity.FileUpdate, e.owner.ID, e.owner.ID, e.docID, e.sharedID, "doc.txt")
	}

	first, err := (&FileActivityService{Uri: "cloudreve://my/Shared/doc.txt"}).List(e.as(e.owner))
	require.NoError(t, err)
	require.Len(t, first.Entries, activityPageSize)
	require.NotZero(t, first.Next)

	second, err := (&FileActivityService{Uri: "cloudreve://my/Shared/doc.txt", Next: first.Next}).List(e.as(e.owner))
	require.NoError(t, err)
	require.Len(t, second.Entries, 3)
	require.Zero(t, second.Next)
	require.Less(t, second.Entries[0].ID, first.Entries[activityPageSize-1].ID)
}

func TestOwnActivityShowsOnlyTheOwnAccount(t *testing.T) {
	e := newActivityEnv(t)
	e.add(activity.Login, e.owner.ID, e.owner.ID, 0, 0, "")
	e.add(activity.LoginFailed, 0, e.owner.ID, 0, 0, "")
	e.add(activity.Login, e.member.ID, e.member.ID, 0, 0, "")
	e.add(activity.FileUpload, e.owner.ID, e.owner.ID, e.docID, e.sharedID, "doc.txt")

	res, err := (&OwnActivityService{}).List(e.as(e.owner))
	require.NoError(t, err)
	require.Len(t, res.Entries, 2)
	require.Equal(t, int(activity.LoginFailed), res.Entries[0].Type)
	require.Nil(t, res.Entries[0].Actor, "who tried is unknown")
	require.Equal(t, int(activity.Login), res.Entries[1].Type)
	require.Equal(t, "192.0.2.1", res.Entries[1].IP, "the own address and device")
	require.Equal(t, "agent", res.Entries[1].UserAgent)
}

func TestAdminActivitySeesAll(t *testing.T) {
	e := newActivityEnv(t)
	e.add(activity.Login, e.member.ID, e.member.ID, 0, 0, "")
	e.add(activity.FileUpload, e.member.ID, e.owner.ID, e.docID, e.sharedID, "doc.txt")
	e.add(activity.FileUpload, e.owner.ID, e.owner.ID, 998, e.privateID, "secret.txt")

	res, err := (&AdminActivityService{}).List(e.as(e.owner))
	require.NoError(t, err)
	require.Len(t, res.Entries, 3)
	require.Equal(t, "192.0.2.1", res.Entries[0].IP)
	require.Equal(t, "owner@fylvo.test", res.Entries[0].Owner.Email)

	res, err = (&AdminActivityService{Account: hashid.EncodeUserID(e.hasher, e.member.ID), Group: "files"}).List(e.as(e.owner))
	require.NoError(t, err)
	require.Equal(t, []string{"doc.txt"}, names(res))

	_, err = (&AdminActivityService{Group: "nonsense"}).List(e.as(e.owner))
	requireAppCode(t, err, serializer.CodeParamErr)
}
