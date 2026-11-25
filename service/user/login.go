package user

import (
	"context"
	"errors"
	"fmt"

	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/ent/user"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/pkg/activity"
	"github.com/cloudreve/Cloudreve/v4/pkg/attempts"
	"github.com/cloudreve/Cloudreve/v4/pkg/auth"
	"github.com/cloudreve/Cloudreve/v4/pkg/cluster/routes"
	"github.com/cloudreve/Cloudreve/v4/pkg/email"
	"github.com/cloudreve/Cloudreve/v4/pkg/hashid"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/cloudreve/Cloudreve/v4/pkg/util"
	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
	"github.com/pquerna/otp/totp"
)

// LoginParameterCtx define key fore UserLoginService
type LoginParameterCtx struct{}

// UserLoginService service for managing user login
type UserLoginService struct {
	UserName string `form:"email" json:"email" binding:"required,email"`
	Password string `form:"password" json:"password" binding:"required,min=4,max=128"`
}

type (
	// UserResetService password reset service
	UserResetService struct {
		Password string `form:"password" json:"password" binding:"required,min=6,max=128"`
		Secret   string `json:"secret" binding:"required"`
	}
	UserResetParameterCtx struct{}
)

// Reset resets the password
func (service *UserResetService) Reset(c *gin.Context) (*User, error) {
	dep := dependency.FromContext(c)
	userClient := dep.UserClient()
	kv := dep.KV()
	uid := hashid.FromContext(c)

	resetSession, ok := kv.Get(fmt.Sprintf("user_reset_%d", uid))
	if !ok || resetSession.(string) != service.Secret {
		return nil, serializer.NewError(serializer.CodeTempLinkExpired, "Link is expired", nil)
	}

	u, err := userClient.GetActiveByID(c, uid)
	if err != nil {
		return nil, serializer.NewError(serializer.CodeUserNotFound, "User not found", err)
	}

	// Fylvo: a password the person chose clears the forced password change, but only if it
	// differs from the one the admin gave. The link stays valid for another try.
	if inventory.MustChangePassword(u) && inventory.CheckPassword(u, service.Password) == nil {
		return nil, serializer.NewError(serializer.CodeSamePassword, "The new password must differ from the current one", nil)
	}

	if err := userClient.SetOwnPassword(c, u, service.Password); err != nil {
		if errors.Is(err, inventory.ErrPasswordChangedMeanwhile) {
			return nil, serializer.NewError(serializer.CodeTempLinkExpired, "Link is expired", nil)
		}
		return nil, serializer.NewError(serializer.CodeInternalSetting, "Failed to update password", err)
	}
	activity.RecordAccount(c, activity.PasswordChange, u.ID, "reset")

	// Fylvo: the password is already stored, so a failed delete is only logged.
	if err := kv.Delete(fmt.Sprintf("user_reset_%d", uid)); err != nil {
		dep.Logger().Warning("Failed to delete reset session of user %d: %s", uid, err)
	}
	u, err = userClient.GetActiveByID(c, uid)
	if err != nil {
		return nil, serializer.NewError(serializer.CodeUserNotFound, "User not found", err)
	}

	userRes := BuildUser(u, dep.HashIDEncoder())
	return &userRes, nil
}

type (
	// UserResetEmailService service for sending the password reset email
	UserResetEmailService struct {
		UserName string `form:"email" json:"email" binding:"required,email"`
		Language string `form:"language" json:"language"`
	}
	UserResetEmailParameterCtx struct{}
)

const userResetPrefix = "user_reset_"

// Reset sends the password reset email
// Fylvo: the mail is prepared and sent in the background, and the answer is the same whether
// the account exists, is blocked or the mail fails, so that neither the answer nor its timing
// tells whether an account exists. Failures are only logged.
func (service *UserResetEmailService) Reset(c *gin.Context) error {
	ctx := context.WithoutCancel(c.Request.Context())
	go func() {
		if err := service.sendReset(ctx); err != nil {
			logging.FromContext(ctx).Warning("Password reset mail for %q not sent: %s", service.UserName, err)
		}
	}()
	return nil
}

// resetMailPause is how long Fylvo waits before it sends another reset mail to the same
// account, so that strangers cannot flood an inbox or use up the mail quota.
const resetMailPause = 300

func (service *UserResetEmailService) sendReset(c context.Context) error {
	dep := dependency.FromContext(c)
	userClient := dep.UserClient()

	u, err := userClient.GetByEmail(c, service.UserName)
	if err != nil {
		return serializer.NewError(serializer.CodeUserNotFound, "User not found", err)
	}

	pauseKey := fmt.Sprintf("user_reset_sent_%d", u.ID)
	if _, sentRecently := dep.KV().Get(pauseKey); sentRecently {
		return fmt.Errorf("a reset mail was sent less than %d seconds ago", resetMailPause)
	}
	_ = dep.KV().Set(pauseKey, true, resetMailPause)

	if u.Status == user.StatusManualBanned || u.Status == user.StatusSysBanned {
		return serializer.NewError(serializer.CodeUserBaned, "This user is banned", nil)
	}

	if u.Status == user.StatusInactive {
		return serializer.NewError(serializer.CodeUserNotActivated, "This user is not activated", nil)
	}

	secret := util.RandStringRunesCrypto(32)
	if err := dep.KV().Set(fmt.Sprintf("%s%d", userResetPrefix, u.ID), secret, 3600); err != nil {
		return serializer.NewError(serializer.CodeInternalSetting, "Failed to create reset session", err)
	}

	base := dep.SettingProvider().SiteURL(c)
	resetUrl := routes.MasterUserResetUrl(base)
	queries := resetUrl.Query()
	queries.Add("id", hashid.EncodeUserID(dep.HashIDEncoder(), u.ID))
	queries.Add("secret", secret)
	resetUrl.RawQuery = queries.Encode()

	title, body, err := email.NewResetEmail(c, dep.SettingProvider(), u, resetUrl.String())
	if err != nil {
		return serializer.NewError(serializer.CodeFailedSendEmail, "Failed to send activation email", err)
	}

	if err := dep.EmailClient(c).Send(c, u.Email, title, body); err != nil {
		return serializer.NewError(serializer.CodeFailedSendEmail, "Failed to send activation email", err)
	}

	return nil
}

// Login user login function
func (service *UserLoginService) Login(c *gin.Context) (*ent.User, string, error) {
	dep := dependency.FromContext(c)
	userClient := dep.UserClient()

	ctx := context.WithValue(c, inventory.LoadUserGroup{}, true)
	var expectedUser *ent.User
	var err error
	// Fylvo: limit wrong passwords per account and client address.
	right, limitErr := attempts.Guard(c, attempts.KindLogin, service.UserName, func() bool {
		expectedUser, err = userClient.GetByEmail(ctx, service.UserName)
		if err != nil {
			inventory.CheckPasswordWithoutUser(service.Password)
			return false
		}
		return inventory.CheckPassword(expectedUser, service.Password) == nil
	})

	// A series of checks
	if limitErr != nil {
		// Fylvo: the account is looked up only when the entry is wanted, see FailureWanted.
		if activity.FailureWanted(c, activity.LoginLocked, activity.MethodPassword, service.UserName) {
			if u, err := userClient.GetByEmail(c, service.UserName); err == nil {
				activity.RecordFailure(c, activity.LoginLocked, u.ID, activity.MethodPassword)
			}
		}
		return nil, "", limitErr
	}
	if !right && expectedUser != nil &&
		activity.FailureWanted(c, activity.LoginFailed, activity.MethodPassword, service.UserName) {
		activity.RecordFailure(c, activity.LoginFailed, expectedUser.ID, activity.MethodPassword)
	}
	if !right {
		err = serializer.NewError(serializer.CodeInvalidPassword, "Incorrect password or email address", err)
	} else if expectedUser.Status == user.StatusManualBanned || expectedUser.Status == user.StatusSysBanned {
		err = serializer.NewError(serializer.CodeUserBaned, "This account has been blocked", nil)
	} else if expectedUser.Status == user.StatusInactive {
		err = serializer.NewError(serializer.CodeUserNotActivated, "This account is not activated", nil)
	}

	if err != nil {
		return nil, "", err
	}

	// Fylvo: replace a password stored in an older, fast format with bcrypt.
	if inventory.PasswordNeedsUpgrade(expectedUser) {
		if stored, upgradeErr := userClient.UpgradePassword(c, expectedUser, service.Password); upgradeErr == nil {
			expectedUser.Password = stored
		} else {
			dep.Logger().Warning("Failed to store the password of user %d with bcrypt: %s", expectedUser.ID, upgradeErr)
		}
	}

	if expectedUser.TwoFactorSecret != "" {
		twoFaSessionID := uuid.Must(uuid.NewV4())
		// Fylvo: the ticket is bound to the password just checked, see fylvo_password_change.go.
		dep.KV().Set(fmt.Sprintf("user_2fa_%s", twoFaSessionID), loginTicket(expectedUser), 600)
		return expectedUser, twoFaSessionID.String(), nil
	}

	return expectedUser, "", nil
}

type (
	LoginLogCtx struct{}
)

func IssueToken(c *gin.Context) (*BuiltinLoginResponse, error) {
	dep := dependency.FromContext(c)
	u := inventory.UserFromContext(c)
	token, err := dep.TokenAuth().Issue(c, &auth.IssueTokenArgs{
		User:        u,
		RootTokenID: nil,
	})
	if err != nil {
		return nil, serializer.NewError(serializer.CodeEncryptError, "Failed to issue token pair", err)
	}

	return &BuiltinLoginResponse{
		User:  BuildUser(u, dep.HashIDEncoder()),
		Token: *token,
	}, nil
}

// RefreshTokenParameterCtx define key fore RefreshTokenService
type RefreshTokenParameterCtx struct{}

// RefreshTokenService refresh token service
type RefreshTokenService struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

func (s *RefreshTokenService) Refresh(c *gin.Context) (*auth.Token, error) {
	dep := dependency.FromContext(c)
	token, err := dep.TokenAuth().Refresh(c, s.RefreshToken)
	if err != nil {
		return nil, serializer.NewError(serializer.CodeCredentialInvalid, "Failed to issue token pair", err)
	}

	return token, nil
}

func (s *RefreshTokenService) Delete(c *gin.Context) (string, error) {
	dep := dependency.FromContext(c)
	claims, err := dep.TokenAuth().Claims(c, s.RefreshToken)
	if err != nil {
		return "", serializer.NewError(serializer.CodeCredentialInvalid, "Failed to parse token", err)
	}

	// Block root token
	if claims.RootTokenID != nil {
		tokenSettings := dep.SettingProvider().TokenAuth(c)
		dep.KV().Set(fmt.Sprintf("%s%s", auth.RevokeTokenPrefix, claims.RootTokenID.String()), true, int(tokenSettings.RefreshTokenTTL.Seconds()+10))
	}

	return "", nil
}

type (
	OtpValidationParameterCtx struct{}
	OtpValidationService      struct {
		OTP       string `json:"otp" binding:"required"`
		SessionID string `json:"session_id" binding:"required"`
	}
)

// Login user login function
func (service *OtpValidationService) Verify2FA(c *gin.Context) (*ent.User, error) {
	dep := dependency.FromContext(c)
	kv := dep.KV()

	sessionRaw, ok := kv.Get(fmt.Sprintf("user_2fa_%s", service.SessionID))
	if !ok {
		return nil, serializer.NewError(serializer.CodeNotFound, "Session not found", nil)
	}

	// Fylvo: the account must still be active and have the password that was checked.
	expectedUser := userFromTicket(c, sessionRaw)
	if expectedUser == nil {
		return nil, serializer.NewError(serializer.CodeNotFound, "Session not found", nil)
	}
	uid := expectedUser.ID

	if expectedUser.TwoFactorSecret != "" {
		// Fylvo: limit wrong codes per account and client address.
		right, err := attempts.Guard(c, attempts.KindTwoFA, fmt.Sprint(uid), func() bool {
			return totp.Validate(service.OTP, expectedUser.TwoFactorSecret)
		})
		if err != nil {
			if activity.FailureWanted(c, activity.LoginLocked, activity.MethodTwoFA, fmt.Sprint(uid)) {
				activity.RecordFailure(c, activity.LoginLocked, uid, activity.MethodTwoFA)
			}
			return nil, err
		}
		if !right {
			if activity.FailureWanted(c, activity.LoginFailed, activity.MethodTwoFA, fmt.Sprint(uid)) {
				activity.RecordFailure(c, activity.LoginFailed, uid, activity.MethodTwoFA)
			}
			err := serializer.NewError(serializer.Code2FACodeErr, "Incorrect 2FA code", nil)
			return nil, err
		}
	}

	kv.Delete("user_2fa_", service.SessionID)
	return expectedUser, nil
}

type (
	PrepareLoginParameterCtx struct{}
	PrepareLoginService      struct {
		Email string `form:"email" binding:"required,email"`
	}
)

// Prepare Fylvo: answers the same for every address, so that it does not tell whether an
// account exists or has a passkey. The sign-in page then asks for the password and offers
// passkeys when they are switched on.
func (service *PrepareLoginService) Prepare(c *gin.Context) (*PrepareLoginResponse, error) {
	dep := dependency.FromContext(c)
	return &PrepareLoginResponse{
		WebAuthnEnabled: dep.SettingProvider().AuthnEnabled(c),
		PasswordEnabled: true,
	}, nil
}
