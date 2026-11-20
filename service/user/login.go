package user

import (
	"context"
	"fmt"

	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/ent/user"
	"github.com/cloudreve/Cloudreve/v4/inventory"
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

// UserLoginService 管理用户登录的服务
type UserLoginService struct {
	UserName string `form:"email" json:"email" binding:"required,email"`
	Password string `form:"password" json:"password" binding:"required,min=4,max=128"`
}

type (
	// UserResetService 密码重设服务
	UserResetService struct {
		Password string `form:"password" json:"password" binding:"required,min=6,max=128"`
		Secret   string `json:"secret" binding:"required"`
	}
	UserResetParameterCtx struct{}
)

// Reset 重设密码
func (service *UserResetService) Reset(c *gin.Context) (*User, error) {
	dep := dependency.FromContext(c)
	userClient := dep.UserClient()
	kv := dep.KV()
	uid := hashid.FromContext(c)

	resetSession, ok := kv.Get(fmt.Sprintf("user_reset_%d", uid))
	if !ok || resetSession.(string) != service.Secret {
		return nil, serializer.NewError(serializer.CodeTempLinkExpired, "Link is expired", nil)
	}

	if err := kv.Delete(fmt.Sprintf("user_reset_%d", uid)); err != nil {
		return nil, serializer.NewError(serializer.CodeInternalSetting, "Failed to delete reset session", err)
	}

	u, err := userClient.GetActiveByID(c, uid)
	if err != nil {
		return nil, serializer.NewError(serializer.CodeUserNotFound, "User not found", err)
	}

	u, err = userClient.UpdatePassword(c, u, service.Password)
	if err != nil {
		return nil, serializer.NewError(serializer.CodeInternalSetting, "Failed to update password", err)
	}

	userRes := BuildUser(u, dep.HashIDEncoder())
	return &userRes, nil
}

type (
	// UserResetEmailService 发送密码重设邮件服务
	UserResetEmailService struct {
		UserName string `form:"email" json:"email" binding:"required,email"`
		Language string `form:"language" json:"language"`
	}
	UserResetEmailParameterCtx struct{}
)

const userResetPrefix = "user_reset_"

// Reset 发送密码重设邮件
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

// Login 用户登录函数
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

	// 一系列校验
	if limitErr != nil {
		return nil, "", limitErr
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
		dep.KV().Set(fmt.Sprintf("user_2fa_%s", twoFaSessionID), expectedUser.ID, 600)
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

// Login 用户登录函数
func (service *OtpValidationService) Verify2FA(c *gin.Context) (*ent.User, error) {
	dep := dependency.FromContext(c)
	kv := dep.KV()

	sessionRaw, ok := kv.Get(fmt.Sprintf("user_2fa_%s", service.SessionID))
	if !ok {
		return nil, serializer.NewError(serializer.CodeNotFound, "Session not found", nil)
	}

	uid := sessionRaw.(int)
	ctx := context.WithValue(c, inventory.LoadUserGroup{}, true)
	expectedUser, err := dep.UserClient().GetByID(ctx, uid)
	if err != nil {
		return nil, serializer.NewError(serializer.CodeNotFound, "User not found", err)
	}

	if expectedUser.TwoFactorSecret != "" {
		// Fylvo: limit wrong codes per account and client address.
		right, err := attempts.Guard(c, attempts.KindTwoFA, fmt.Sprint(uid), func() bool {
			return totp.Validate(service.OTP, expectedUser.TwoFactorSecret)
		})
		if err != nil {
			return nil, err
		}
		if !right {
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
