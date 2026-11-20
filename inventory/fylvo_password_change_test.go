package inventory

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/ent/enttest"
	"github.com/cloudreve/Cloudreve/v4/ent/user"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/boolset"
	"github.com/stretchr/testify/require"
)

func newPasswordChangeTestClient(t *testing.T) (*ent.Client, UserClient, int) {
	client := enttest.Open(t, "sqlite3", filepath.Join(t.TempDir(), "fylvo.db"))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	g, err := client.Group.Create().SetName("User").SetPermissions(&boolset.BooleanSet{}).Save(context.Background())
	require.NoError(t, err)
	return client, NewUserClient(client), g.ID
}

func TestAdminCreateStoresTheHoldWithThePassword(t *testing.T) {
	ctx := context.Background()
	_, users, groupID := newPasswordChangeTestClient(t)

	u, err := users.Upsert(ctx, &ent.User{Email: "neu@fylvo.local", Status: user.StatusActive, GroupUsers: groupID},
		"erstes-passwort", "", &types.UserSetting{FylvoMustChangePassword: true})
	require.NoError(t, err)
	require.True(t, MustChangePassword(u))
	require.NoError(t, CheckPassword(u, "erstes-passwort"))

	// Without settings, an update leaves them as they are.
	u.Nick = "Neu"
	u, err = users.Upsert(ctx, u, "", "", nil)
	require.NoError(t, err)
	require.True(t, MustChangePassword(u))

	// An admin update with settings writes the flag together with the new password.
	settings := *u.Settings
	settings.FylvoMustChangePassword = false
	u, err = users.Upsert(ctx, u, "zweites-passwort", "", &settings)
	require.NoError(t, err)
	reloaded, err := users.GetByID(ctx, u.ID)
	require.NoError(t, err)
	require.False(t, MustChangePassword(reloaded))
	require.NoError(t, CheckPassword(reloaded, "zweites-passwort"))
}

func TestSetOwnPasswordClearsTheHold(t *testing.T) {
	ctx := context.Background()
	_, users, groupID := newPasswordChangeTestClient(t)
	u, err := users.Upsert(ctx, &ent.User{Email: "neu@fylvo.local", Status: user.StatusActive, GroupUsers: groupID},
		"erstes-passwort", "", &types.UserSetting{FylvoMustChangePassword: true, Language: "de-DE"})
	require.NoError(t, err)
	fingerprint := PasswordFingerprint(u)

	require.NoError(t, users.SetOwnPassword(ctx, u, "eigenes-passwort"))
	reloaded, err := users.GetByID(ctx, u.ID)
	require.NoError(t, err)
	require.False(t, MustChangePassword(reloaded))
	require.Equal(t, "de-DE", reloaded.Settings.Language, "other settings stay")
	require.NoError(t, CheckPassword(reloaded, "eigenes-passwort"))
	require.Error(t, CheckPassword(reloaded, "erstes-passwort"))
	require.NotEqual(t, fingerprint, PasswordFingerprint(reloaded))
}

func TestSetOwnPasswordRefusesAPasswordChangedMeanwhile(t *testing.T) {
	ctx := context.Background()
	_, users, groupID := newPasswordChangeTestClient(t)
	u, err := users.Upsert(ctx, &ent.User{Email: "neu@fylvo.local", Status: user.StatusActive, GroupUsers: groupID},
		"erstes-passwort", "", &types.UserSetting{FylvoMustChangePassword: true})
	require.NoError(t, err)
	stale := *u

	// The admin sets another password in between: the change made with the old copy must fail,
	// and a second finish with the same copy too (the proof is single-use).
	_, err = users.Upsert(ctx, u, "admin-passwort", "", nil)
	require.NoError(t, err)
	require.ErrorIs(t, users.SetOwnPassword(ctx, &stale, "eigenes-passwort"), ErrPasswordChangedMeanwhile)

	fresh, err := users.GetByID(ctx, u.ID)
	require.NoError(t, err)
	require.NoError(t, users.SetOwnPassword(ctx, fresh, "eigenes-passwort"))
	require.ErrorIs(t, users.SetOwnPassword(ctx, fresh, "noch-eines"), ErrPasswordChangedMeanwhile)
}

func TestMustChangePasswordWithoutSettings(t *testing.T) {
	require.False(t, MustChangePassword(nil))
	require.False(t, MustChangePassword(&ent.User{}))
	require.True(t, MustChangePassword(&ent.User{Settings: &types.UserSetting{FylvoMustChangePassword: true}}))
}
