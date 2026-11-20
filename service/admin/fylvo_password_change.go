package admin

import (
	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
)

// passwordChangeSettings Fylvo: returns the settings to store with an admin create or update, or
// nil to leave them as they are. A new account, and an account the admin gives a new password,
// has to set its own password at the next sign-in, unless the admin switches that off. The
// admin can also switch it on or off without a new password. The settings sent by the admin
// form are ignored: only the flag is taken from the request.
func (s *UpsertUserService) passwordChangeSettings(existing *ent.User) *types.UserSetting {
	if existing == nil {
		return &types.UserSetting{FylvoMustChangePassword: s.MustChangePassword == nil || *s.MustChangePassword}
	}

	want := inventory.MustChangePassword(existing)
	if s.Password != "" {
		want = true
	}
	if s.MustChangePassword != nil {
		want = *s.MustChangePassword
	}
	if want == inventory.MustChangePassword(existing) {
		return nil
	}

	settings := &types.UserSetting{}
	if existing.Settings != nil {
		copied := *existing.Settings
		settings = &copied
	}
	settings.FylvoMustChangePassword = want
	return settings
}
