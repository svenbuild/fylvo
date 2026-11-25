package controllers

import (
	"strings"

	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/pkg/activity"
	"github.com/cloudreve/Cloudreve/v4/pkg/hashid"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/cloudreve/Cloudreve/v4/pkg/util"
	"github.com/cloudreve/Cloudreve/v4/service/share"
	"github.com/cloudreve/Cloudreve/v4/service/user"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

// StartLoginAuthn starts WebAuthn login
func StartLoginAuthn(c *gin.Context) {
	res, err := user.PreparePasskeyLogin(c)
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		return
	}

	c.JSON(200, serializer.Response{Data: res})
}

// FinishLoginAuthn finishes WebAuthn login
func FinishLoginAuthn(c *gin.Context) {
	service := ParametersFromContext[*user.FinishPasskeyLoginService](c, user.FinishPasskeyLoginParameterCtx{})
	u, err := service.FinishPasskeyLogin(c)
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		c.Abort()
		return
	}

	util.WithValue(c, inventory.UserCtx{}, u)
}

// StartRegAuthn starts WebAuthn registration
func StartRegAuthn(c *gin.Context) {
	res, err := user.PreparePasskeyRegister(c)
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		return
	}

	c.JSON(200, serializer.Response{Data: res})
}

// FinishRegAuthn finishes WebAuthn registration
func FinishRegAuthn(c *gin.Context) {
	service := ParametersFromContext[*user.FinishPasskeyRegisterService](c, user.FinishPasskeyRegisterParameterCtx{})
	res, err := service.FinishPasskeyRegister(c)
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		return
	}

	c.JSON(200, serializer.Response{Data: res})
}

// UserDeletePasskey deletes user passkey
func UserDeletePasskey(c *gin.Context) {
	service := ParametersFromContext[*user.DeletePasskeyService](c, user.DeletePasskeyParameterCtx{})
	err := service.DeletePasskey(c)
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		return
	}

	c.JSON(200, serializer.Response{})
}

// UserLoginValidation validates user login request
func UserLoginValidation(c *gin.Context) {
	service := ParametersFromContext[*user.UserLoginService](c, user.LoginParameterCtx{})
	expectedUser, twoFaSession, err := service.Login(c)
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		c.Abort()
		return
	}

	if twoFaSession == "" {
		// No 2FA required, proceed
		util.WithValue(c, inventory.UserCtx{}, expectedUser)
		c.Next()
		return
	}

	c.JSON(200, serializer.Response{Code: serializer.CodeNotFullySuccess, Data: twoFaSession})
	c.Abort()
}

// UserLogin2FAValidation validates user OTP code
func UserLogin2FAValidation(c *gin.Context) {
	service := ParametersFromContext[*user.OtpValidationService](c, user.OtpValidationParameterCtx{})
	expectedUser, err := service.Verify2FA(c)
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		c.Abort()
		return
	}

	util.WithValue(c, inventory.UserCtx{}, expectedUser)
	c.Next()
}

// UserIssueToken generates new token pair for user
func UserIssueToken(c *gin.Context) {
	// Fylvo: an account that has to set a new password gets a ticket instead of tokens.
	if u := inventory.UserFromContext(c); inventory.MustChangePassword(u) {
		res, err := user.StartPasswordChange(c, u)
		if err != nil {
			c.JSON(200, serializer.Err(c, err))
			c.Abort()
			return
		}
		c.JSON(200, res)
		return
	}

	resp, err := user.IssueToken(c)
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		c.Abort()
		return
	}

	// Fylvo: every way of signing in in the browser ends here.
	activity.RecordAccount(c, activity.Login, inventory.UserFromContext(c).ID, loginMethod(c.FullPath()))
	c.JSON(200, serializer.Response{
		Data: resp,
	})
}

// loginMethod tells the sign-in method from the route (Fylvo). Setting a new password after a
// held sign-in counts as signing in with a password.
func loginMethod(route string) string {
	switch {
	case strings.HasSuffix(route, "/token/2fa"):
		return activity.MethodTwoFA
	case strings.HasSuffix(route, "/authn"):
		return activity.MethodPasskey
	default:
		return activity.MethodPassword
	}
}

// UserRefreshToken refreshes token pair for user
func UserRefreshToken(c *gin.Context) {
	service := ParametersFromContext[*user.RefreshTokenService](c, user.RefreshTokenParameterCtx{})
	resp, err := service.Refresh(c)
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		c.Abort()
		return
	}

	c.JSON(200, serializer.Response{
		Data: resp,
	})
}

// UserRegister registers a user
func UserRegister(c *gin.Context) {
	service := ParametersFromContext[*user.UserRegisterService](c, user.RegisterParameterCtx{})
	c.JSON(200, service.Register(c))
}

// UserSendReset sends the password reset email
func UserSendReset(c *gin.Context) {
	service := ParametersFromContext[*user.UserResetEmailService](c, user.UserResetEmailParameterCtx{})
	if err := service.Reset(c); err != nil {
		c.JSON(200, serializer.Err(c, err))
		c.Abort()
		return
	}
	c.JSON(200, serializer.Response{})
}

// UserReset resets the password
func UserReset(c *gin.Context) {
	service := ParametersFromContext[*user.UserResetService](c, user.UserResetParameterCtx{})
	res, err := service.Reset(c)
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		c.Abort()
		return
	}
	c.JSON(200, serializer.Response{Data: res})
}

// UserActivate activates a user
func UserActivate(c *gin.Context) {
	c.JSON(200, user.ActivateUser(c))
}

// UserSignOut logs the user out
func UserSignOut(c *gin.Context) {
	service := ParametersFromContext[*user.RefreshTokenService](c, user.RefreshTokenParameterCtx{})
	res, err := service.Delete(c)
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		c.Abort()
		return
	}

	c.JSON(200, serializer.Response{
		Data: res,
	})
}

// UserMe gets the currently logged in user
func UserMe(c *gin.Context) {
	dep := dependency.FromContext(c)
	c.JSON(200, serializer.Response{
		Data: user.BuildUser(inventory.UserFromContext(c), dep.HashIDEncoder()),
	})
}

// UserGet gets user info
func UserGet(c *gin.Context) {
	u, err := user.GetUser(c)
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		c.Abort()
		return
	}

	isAnonymous := inventory.IsAnonymousUser(inventory.UserFromContext(c))
	redactLevel := user.RedactLevelUser
	if isAnonymous {
		redactLevel = user.RedactLevelAnonymous
	}
	c.JSON(200, serializer.Response{
		Data: user.BuildUserRedacted(c, u, redactLevel, dependency.FromContext(c).HashIDEncoder()),
	})
}

// UserStorage gets the user's storage info
func UserStorage(c *gin.Context) {
	res, err := user.GetUserCapacity(c)
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		c.Abort()
		return
	}

	c.JSON(200, serializer.Response{
		Data: res,
	})
}

// UserSetting gets user settings
func UserSetting(c *gin.Context) {
	res, err := user.GetUserSettings(c)
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		c.Abort()
		return
	}

	c.JSON(200, serializer.Response{
		Data: res,
	})
}

// UploadAvatar uploads an avatar from a file
func UploadAvatar(c *gin.Context) {
	if err := user.UpdateUserAvatar(c); err != nil {
		c.JSON(200, serializer.Err(c, err))
		return
	}

	c.JSON(200, serializer.Response{})
}

// GetUserAvatar gets the user avatar
func GetUserAvatar(c *gin.Context) {
	service := ParametersFromContext[*user.GetAvatarService](c, user.GetAvatarServiceParamsCtx{})
	err := service.Get(c)
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		c.Abort()
		return
	}
}

// UpdateOption changes user settings
func UpdateOption(c *gin.Context) {
	service := ParametersFromContext[*user.PatchUserSetting](c, user.PatchUserSettingParamsCtx{})
	err := service.Patch(c)
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		c.Abort()
		return
	}

	c.JSON(200, serializer.Response{})

	//var service user.SettingUpdateService
	//if err := c.ShouldBindUri(&service); err == nil {
	//	var (
	//		subService user.OptionsChangeHandler
	//		subErr     error
	//	)
	//
	//	switch service.Option {
	//	case "nick":
	//		subService = &user.ChangerNick{}
	//	case "vip":
	//		subService = &user.VIPUnsubscribe{}
	//	case "qq":
	//		subService = &user.QQBind{}
	//	case "policy":
	//		subService = &user.PolicyChange{}
	//	case "homepage":
	//		subService = &user.HomePage{}
	//	case "password":
	//		subService = &user.PasswordChange{}
	//	case "2fa":
	//		subService = &user.Enable2FA{}
	//	case "authn":
	//		subService = &user.DeleteWebAuthn{}
	//	case "theme":
	//		subService = &user.ThemeChose{}
	//	default:
	//		subService = &user.ChangerNick{}
	//	}
	//
	//	subErr = c.ShouldBindJSON(subService)
	//	if subErr != nil {
	//		c.JSON(200, ErrorResponse(subErr))
	//		return
	//	}
	//
	//	res := subService.Update(c, CurrentUser(c))
	//	c.JSON(200, res)
	//
	//} else {
	//	c.JSON(200, ErrorResponse(err))
	//}
}

// UserInit2FA initializes two-factor authentication
func UserInit2FA(c *gin.Context) {
	secret, err := user.Init2FA(c)
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		c.Abort()
		return
	}

	c.JSON(200, serializer.Response{
		Data: secret,
	})
}

// UserPrepareLogin validates precondition for login
func UserPrepareLogin(c *gin.Context) {
	service := ParametersFromContext[*user.PrepareLoginService](c, user.PrepareLoginParameterCtx{})
	res, err := service.Prepare(c)
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		c.Abort()
		return
	}

	c.JSON(200, serializer.Response{Data: res})
}

// UserSearch Search user by keyword
func UserSearch(c *gin.Context) {
	service := ParametersFromContext[*user.SearchUserService](c, user.SearchUserParamCtx{})
	u, err := service.Search(c)
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		c.Abort()
		return
	}

	hasher := dependency.FromContext(c).HashIDEncoder()
	c.JSON(200, serializer.Response{
		Data: lo.Map(u, func(item *ent.User, index int) user.User {
			return user.BuildUserRedacted(c, item, user.RedactLevelUser, hasher)
		}),
	})
}

// ListPublicShare lists all public shares for given user
func ListPublicShare(c *gin.Context) {
	service := ParametersFromContext[*share.ListShareService](c, share.ListShareParamCtx{})
	resp, err := service.ListInUserProfile(c, hashid.FromContext(c))
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		c.Abort()
		return
	}

	if resp != nil {
		c.JSON(200, serializer.Response{
			Data: resp,
		})
	}
}
