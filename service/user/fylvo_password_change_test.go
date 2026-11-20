package user

import (
	"context"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/ent/enttest"
	"github.com/cloudreve/Cloudreve/v4/ent/user"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/boolset"
	"github.com/cloudreve/Cloudreve/v4/pkg/cache"
	"github.com/cloudreve/Cloudreve/v4/pkg/hashid"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/cloudreve/Cloudreve/v4/pkg/util"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type passwordChangeEnv struct {
	users inventory.UserClient
	kv    cache.Driver
	dep   dependency.Dep
	held  *ent.User
}

func newPasswordChangeEnv(t *testing.T) *passwordChangeEnv {
	client := enttest.Open(t, "sqlite3", filepath.Join(t.TempDir(), "fylvo.db"))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := context.Background()
	g, err := client.Group.Create().SetName("User").SetPermissions(&boolset.BooleanSet{}).Save(ctx)
	require.NoError(t, err)

	users := inventory.NewUserClient(client)
	kv := cache.NewMemoStore("", logging.NewConsoleLogger(logging.LevelError))
	encoder, err := hashid.New("test")
	require.NoError(t, err)
	held, err := users.Upsert(ctx, &ent.User{Email: "neu@fylvo.local", Status: user.StatusActive, GroupUsers: g.ID},
		"erstes-passwort", "", &types.UserSetting{FylvoMustChangePassword: true})
	require.NoError(t, err)
	return &passwordChangeEnv{
		users: users,
		kv:    kv,
		dep:   dependency.NewDependency(dependency.WithUserClient(users), dependency.WithKV(kv), dependency.WithDbClient(client), dependency.WithHashIDEncoder(encoder)),
		held:  held,
	}
}

func (e *passwordChangeEnv) context() *gin.Context {
	c, engine := gin.CreateTestContext(httptest.NewRecorder())
	engine.ContextWithFallback = true
	c.Request = httptest.NewRequest("POST", "/api/v4/session/token/password", nil)
	util.WithValue(c, dependency.DepCtx{}, e.dep)
	return c
}

func (e *passwordChangeEnv) start(t *testing.T, u *ent.User) string {
	res, err := StartPasswordChange(e.context(), u)
	require.NoError(t, err)
	require.Equal(t, serializer.CodePasswordChangeRequired, res.Code)
	require.Equal(t, PasswordChangeRequiredMsg, res.Msg)
	return res.Data.(string)
}

func appErrorCode(t *testing.T, err error) int {
	var appErr serializer.AppError
	require.ErrorAs(t, err, &appErr)
	return appErr.ErrCode()
}

func TestPasswordChangeFlow(t *testing.T) {
	e := newPasswordChangeEnv(t)
	ticket := e.start(t, e.held)

	// The same password is refused, and the ticket stays valid for another try.
	_, err := (&PasswordChangeService{SessionID: ticket, Password: "erstes-passwort"}).Finish(e.context())
	require.Equal(t, serializer.CodeSamePassword, appErrorCode(t, err))

	u, err := (&PasswordChangeService{SessionID: ticket, Password: "eigenes-passwort"}).Finish(e.context())
	require.NoError(t, err)
	require.False(t, inventory.MustChangePassword(u))
	require.NotNil(t, u.Edges.Group, "the group is loaded for the sign-in answer")
	require.NoError(t, inventory.CheckPassword(u, "eigenes-passwort"))
	require.Error(t, inventory.CheckPassword(u, "erstes-passwort"))

	// The ticket is used up.
	_, err = (&PasswordChangeService{SessionID: ticket, Password: "noch-eines-1"}).Finish(e.context())
	require.Equal(t, serializer.CodePasswordChangeExpired, appErrorCode(t, err))
}

func TestPasswordChangeTicketEndsWithAnotherPassword(t *testing.T) {
	e := newPasswordChangeEnv(t)
	ticket := e.start(t, e.held)

	// The admin sets another password before the person finishes.
	_, err := e.users.Upsert(context.Background(), e.held, "admin-passwort", "", nil)
	require.NoError(t, err)
	_, err = (&PasswordChangeService{SessionID: ticket, Password: "eigenes-passwort"}).Finish(e.context())
	require.Equal(t, serializer.CodePasswordChangeExpired, appErrorCode(t, err))
}

func TestPasswordChangeTicketEndsWhenAdminLiftsTheHold(t *testing.T) {
	e := newPasswordChangeEnv(t)
	ticket := e.start(t, e.held)

	_, err := e.users.Upsert(context.Background(), e.held, "", "", &types.UserSetting{})
	require.NoError(t, err)
	_, err = (&PasswordChangeService{SessionID: ticket, Password: "eigenes-passwort"}).Finish(e.context())
	require.Equal(t, serializer.CodePasswordChangeExpired, appErrorCode(t, err))
}

func TestUnknownPasswordChangeTicket(t *testing.T) {
	e := newPasswordChangeEnv(t)
	for _, ticket := range []string{"", "unbekannt", "1:abc"} {
		_, err := (&PasswordChangeService{SessionID: ticket, Password: "eigenes-passwort"}).Finish(e.context())
		require.Equal(t, serializer.CodePasswordChangeExpired, appErrorCode(t, err))
	}
}

func TestTwoFATicketIsBoundToThePassword(t *testing.T) {
	e := newPasswordChangeEnv(t)
	ctx := e.context()
	require.NoError(t, e.kv.Set("user_2fa_sitzung", loginTicket(e.held), 600))
	require.Equal(t, e.held.ID, userFromTicket(ctx, loginTicket(e.held)).ID)

	_, err := e.users.Upsert(context.Background(), e.held, "admin-passwort", "", nil)
	require.NoError(t, err)
	_, err = (&OtpValidationService{SessionID: "sitzung", OTP: "123456"}).Verify2FA(ctx)
	require.Equal(t, serializer.CodeNotFound, appErrorCode(t, err))

	// Tickets of the old format (only the user ID) are no longer accepted.
	require.Nil(t, userFromTicket(ctx, e.held.ID))
}

func TestResetByMailClearsTheHold(t *testing.T) {
	e := newPasswordChangeEnv(t)
	ctx := e.context()
	util.WithValue(ctx, hashid.ObjectIDCtx{}, e.held.ID)
	require.NoError(t, e.kv.Set(fmt.Sprintf("user_reset_%d", e.held.ID), "geheim", 3600))

	// The admin's password is refused, and the link stays usable.
	_, err := (&UserResetService{Secret: "geheim", Password: "erstes-passwort"}).Reset(ctx)
	require.Equal(t, serializer.CodeSamePassword, appErrorCode(t, err))

	_, err = (&UserResetService{Secret: "geheim", Password: "eigenes-passwort"}).Reset(ctx)
	require.NoError(t, err)
	u, err := e.users.GetByID(context.Background(), e.held.ID)
	require.NoError(t, err)
	require.False(t, inventory.MustChangePassword(u))
	require.NoError(t, inventory.CheckPassword(u, "eigenes-passwort"))
}
