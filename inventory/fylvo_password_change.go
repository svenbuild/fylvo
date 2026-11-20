package inventory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/ent/user"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
)

// Fylvo: an account the admin created, or gave a new password, has to set its own password at
// the next sign-in. The flag is UserSetting.FylvoMustChangePassword. While it is set, sign-in
// gives no session but a short-lived ticket for the change (service/user/fylvo_password_change.go),
// and existing tokens, token refresh and WebDAV are refused.

// ErrPasswordChangedMeanwhile is returned when the stored password is no longer the one the
// caller checked.
var ErrPasswordChangedMeanwhile = errors.New("the password was changed meanwhile")

// MustChangePassword reports whether u has to set a new password before it can sign in.
func MustChangePassword(u *ent.User) bool {
	return u != nil && u.Settings != nil && u.Settings.FylvoMustChangePassword
}

// PasswordFingerprint returns a hash of the stored password hash of u. It changes with every
// new password, so tickets made after a password check can tell whether it is still valid.
func PasswordFingerprint(u *ent.User) string {
	sum := sha256.Sum256([]byte(u.Password))
	return hex.EncodeToString(sum[:])
}

// SetOwnPassword stores a password the person chose and clears the password-change flag, in one
// write and only while the stored password is still the one in u. A second call with the same u
// therefore fails, as does a call after the admin set another password.
func (c *userClient) SetOwnPassword(ctx context.Context, u *ent.User, newPassword string) error {
	digest, err := digestPassword(newPassword)
	if err != nil {
		return err
	}

	settings := &types.UserSetting{}
	if u.Settings != nil {
		copied := *u.Settings
		settings = &copied
	}
	settings.FylvoMustChangePassword = false

	updated, err := c.client.User.Update().
		Where(user.ID(u.ID), user.Password(u.Password)).
		SetPassword(digest).
		SetSettings(settings).
		Save(ctx)
	if err != nil {
		return err
	}
	if updated == 0 {
		return ErrPasswordChangedMeanwhile
	}
	return nil
}
