package user

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/pkg/activity"
	"github.com/cloudreve/Cloudreve/v4/pkg/attempts"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
)

// Fylvo: forced password change at sign-in. When an account has to set a new password
// (inventory.MustChangePassword), a right password (and 2FA code) or passkey gives no session
// but a ticket. With the ticket and a new password, POST /session/token/password stores the
// password, clears the flag and signs the person in.

const (
	passwordChangePrefix = "fylvo_pw_change_"
	passwordChangeTTL    = 600

	// PasswordChangeRequiredMsg is shown by apps that do not know the step.
	PasswordChangeRequiredMsg = "Bitte zuerst im Browser anmelden und ein neues Passwort setzen."
)

// errPasswordChangeExpired answers for unknown, expired and outdated tickets alike.
var errPasswordChangeExpired = serializer.NewError(serializer.CodePasswordChangeExpired,
	"Session expired, please sign in again", nil)

// loginTicket binds a ticket to the account and to its password at the moment it was checked.
func loginTicket(u *ent.User) string {
	return fmt.Sprintf("%d:%s", u.ID, inventory.PasswordFingerprint(u))
}

// userFromTicket loads the active account of a ticket made by loginTicket, with its group. It
// returns nil if the ticket is malformed, the account is gone or blocked, or its password
// changed since the ticket was made.
func userFromTicket(ctx context.Context, raw any) *ent.User {
	ticket, _ := raw.(string)
	id, fingerprint, ok := strings.Cut(ticket, ":")
	if !ok {
		return nil
	}
	uid, err := strconv.Atoi(id)
	if err != nil {
		return nil
	}

	ctx = context.WithValue(ctx, inventory.LoadUserGroup{}, true)
	u, err := dependency.FromContext(ctx).UserClient().GetActiveByID(ctx, uid)
	if err != nil || subtle.ConstantTimeCompare([]byte(fingerprint), []byte(inventory.PasswordFingerprint(u))) != 1 {
		return nil
	}
	return u
}

// StartPasswordChange makes a ticket for the password change of u and returns the answer for
// the sign-in request.
func StartPasswordChange(c *gin.Context, u *ent.User) (serializer.Response, error) {
	sessionID := uuid.Must(uuid.NewV4()).String()
	if err := dependency.FromContext(c).KV().Set(passwordChangePrefix+sessionID, loginTicket(u), passwordChangeTTL); err != nil {
		return serializer.Response{}, serializer.NewError(serializer.CodeInternalSetting, "Failed to create password change session", err)
	}
	return serializer.Response{
		Code: serializer.CodePasswordChangeRequired,
		Data: sessionID,
		Msg:  PasswordChangeRequiredMsg,
	}, nil
}

type (
	PasswordChangeParameterCtx struct{}
	// PasswordChangeService sets the new password with a ticket from StartPasswordChange.
	PasswordChangeService struct {
		SessionID string `json:"session_id" binding:"required"`
		Password  string `json:"password" binding:"required,min=6,max=128"`
	}
)

// Finish stores the new password and returns the account, ready for a token.
func (s *PasswordChangeService) Finish(c *gin.Context) (*ent.User, error) {
	dep := dependency.FromContext(c)
	kv := dep.KV()

	// Unknown, expired and outdated tickets look the same and count as wrong guesses.
	var u *ent.User
	valid, err := attempts.Guard(c, attempts.KindPasswordChange, "ticket", func() bool {
		raw, ok := kv.Get(passwordChangePrefix + s.SessionID)
		if ok {
			u = userFromTicket(c, raw)
		}
		return u != nil && inventory.MustChangePassword(u)
	})
	if err != nil {
		return nil, err
	}
	if !valid {
		return nil, errPasswordChangeExpired
	}

	if inventory.CheckPassword(u, s.Password) == nil {
		return nil, serializer.NewError(serializer.CodeSamePassword, "The new password must differ from the current one", nil)
	}

	if err := dep.UserClient().SetOwnPassword(c, u, s.Password); err != nil {
		if errors.Is(err, inventory.ErrPasswordChangedMeanwhile) {
			return nil, errPasswordChangeExpired
		}
		return nil, serializer.NewError(serializer.CodeDBError, "Failed to update password", err)
	}
	activity.RecordAccount(c, activity.PasswordChange, u.ID, "")
	_ = kv.Delete(passwordChangePrefix, s.SessionID)

	ctx := context.WithValue(c, inventory.LoadUserGroup{}, true)
	u, err = dep.UserClient().GetActiveByID(ctx, u.ID)
	if err != nil {
		return nil, serializer.NewError(serializer.CodeUserNotFound, "User not found", err)
	}
	return u, nil
}
