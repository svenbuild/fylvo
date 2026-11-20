package admin

import (
	"testing"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/stretchr/testify/require"
)

func TestPasswordChangeSettings(t *testing.T) {
	yes, no := true, false
	held := &ent.User{Settings: &types.UserSetting{FylvoMustChangePassword: true, Language: "de-DE"}}
	free := &ent.User{Settings: &types.UserSetting{Language: "de-DE"}}

	// New accounts: on by default, the admin can switch it off.
	require.True(t, (&UpsertUserService{Password: "erstes"}).passwordChangeSettings(nil).FylvoMustChangePassword)
	require.False(t, (&UpsertUserService{Password: "erstes", MustChangePassword: &no}).passwordChangeSettings(nil).FylvoMustChangePassword)

	// A new password switches it on, keeping the other settings.
	s := (&UpsertUserService{Password: "neu"}).passwordChangeSettings(free)
	require.True(t, s.FylvoMustChangePassword)
	require.Equal(t, "de-DE", s.Language)
	require.False(t, free.Settings.FylvoMustChangePassword, "the loaded settings are not changed in place")

	// The admin's explicit choice wins.
	require.Nil(t, (&UpsertUserService{Password: "neu", MustChangePassword: &no}).passwordChangeSettings(free))
	require.False(t, (&UpsertUserService{MustChangePassword: &no}).passwordChangeSettings(held).FylvoMustChangePassword)
	require.True(t, (&UpsertUserService{MustChangePassword: &yes}).passwordChangeSettings(free).FylvoMustChangePassword)

	// Nothing to change: settings stay untouched.
	require.Nil(t, (&UpsertUserService{}).passwordChangeSettings(free))
	require.Nil(t, (&UpsertUserService{}).passwordChangeSettings(held))
}
