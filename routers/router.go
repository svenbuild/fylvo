package routers

import (
	"net/http"

	"github.com/cloudreve/Cloudreve/v4/application/constants"
	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/middleware"
	"github.com/cloudreve/Cloudreve/v4/pkg/cluster"
	"github.com/cloudreve/Cloudreve/v4/pkg/conf"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected"
	"github.com/cloudreve/Cloudreve/v4/pkg/downloader/slave"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/hashid"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/cloudreve/Cloudreve/v4/pkg/webdav"
	"github.com/cloudreve/Cloudreve/v4/routers/controllers"
	adminsvc "github.com/cloudreve/Cloudreve/v4/service/admin"
	"github.com/cloudreve/Cloudreve/v4/service/basic"
	"github.com/cloudreve/Cloudreve/v4/service/explorer"
	fylvosvc "github.com/cloudreve/Cloudreve/v4/service/fylvo"
	"github.com/cloudreve/Cloudreve/v4/service/node"
	"github.com/cloudreve/Cloudreve/v4/service/oauth"
	"github.com/cloudreve/Cloudreve/v4/service/setting"
	sharesvc "github.com/cloudreve/Cloudreve/v4/service/share"
	usersvc "github.com/cloudreve/Cloudreve/v4/service/user"
	"github.com/gin-contrib/cors"
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
)

// InitRouter initializes the router
func InitRouter(dep dependency.Dep) *gin.Engine {
	l := dep.Logger()
	if dep.ConfigProvider().System().Mode == conf.MasterMode {
		l.Info("Current running mode: Master.")
		return initMasterRouter(dep)
	}

	l.Info("Current running mode: Slave.")
	return initSlaveRouter(dep)
}

func newGinEngine(dep dependency.Dep) *gin.Engine {
	r := gin.New()
	r.ContextWithFallback = true
	r.Use(middleware.Recovery())
	trustProxies(r, dep.Logger(), dep.ConfigProvider().System().ProxyHeader)
	r.Use(securityHeaders())
	r.Use(middleware.InitializeHandling(dep))
	if dep.ConfigProvider().System().Mode == conf.SlaveMode {
		r.Use(middleware.InitializeHandlingSlave())
	}
	r.Use(middleware.Logging())
	return r
}

func initSlaveFileRouter(v4 *gin.RouterGroup) {
	// Upload related, no signature required under this router group
	upload := v4.Group("upload")
	{
		// upload part
		upload.POST(":sessionId",
			controllers.FromUri[explorer.UploadService](explorer.UploadParameterCtx{}),
			controllers.SlaveUpload,
		)
		// create upload session
		upload.PUT("",
			controllers.FromJSON[explorer.SlaveCreateUploadSessionService](explorer.SlaveCreateUploadSessionParamCtx{}),
			controllers.SlaveGetUploadSession,
		)
		// delete upload session
		upload.DELETE(":sessionId",
			controllers.FromUri[explorer.SlaveDeleteUploadSessionService](explorer.SlaveDeleteUploadSessionParamCtx{}),
			controllers.SlaveDeleteUploadSession)
	}
	file := v4.Group("file")
	{
		// Get entity content for preview/download
		file.GET("content/:nodeId/:src/:speed/:name",
			middleware.Sandbox(),
			controllers.FromUri[explorer.EntityDownloadService](explorer.EntityDownloadParameterCtx{}),
			controllers.SlaveServeEntity,
		)
		file.HEAD("content/:nodeId/:src/:speed/:name",
			controllers.FromUri[explorer.EntityDownloadService](explorer.EntityDownloadParameterCtx{}),
			controllers.SlaveServeEntity,
		)
		// Get media metadata
		file.GET("meta/:src/:ext",
			controllers.FromUri[explorer.SlaveMetaService](explorer.SlaveMetaParamCtx{}),
			controllers.SlaveMeta,
		)
		// Get thumbnail
		file.GET("thumb/:src/:ext",
			controllers.FromUri[explorer.SlaveThumbService](explorer.SlaveThumbParamCtx{}),
			controllers.SlaveThumb,
		)
		// delete file
		file.DELETE("",
			controllers.FromJSON[explorer.SlaveDeleteFileService](explorer.SlaveDeleteFileParamCtx{}),
			controllers.SlaveDelete)
		// list files
		file.GET("list",
			controllers.FromQuery[explorer.SlaveListService](explorer.SlaveListParamCtx{}),
			controllers.SlaveList,
		)
	}
}

// initSlaveRouter initializes routes for slave mode
func initSlaveRouter(dep dependency.Dep) *gin.Engine {
	r := newGinEngine(dep)
	// CORS related
	initCORS(dep.Logger(), dep.ConfigProvider(), r)
	v4 := r.Group(constants.APIPrefix + "/slave")
	// auth middleware
	v4.Use(middleware.SignRequired(dep.GeneralAuth()))
	// disable caching
	v4.Use(middleware.CacheControl())

	/*
		routes
	*/
	{
		// Ping
		v4.POST("ping",
			controllers.FromJSON[adminsvc.SlavePingService](adminsvc.SlavePingParameterCtx{}),
			controllers.SlavePing,
		)
		// // test the Aria2 RPC connection
		// v4.POST("ping/aria2", controllers.AdminTestAria2)
		initSlaveFileRouter(v4)

		// offline download
		download := v4.Group("download")
		{
			// create offline download task
			download.POST("task",
				controllers.FromJSON[slave.CreateSlaveDownload](node.CreateSlaveDownloadTaskParamCtx{}),
				middleware.PrepareSlaveDownloader(dep, node.CreateSlaveDownloadTaskParamCtx{}),
				controllers.SlaveDownloadTaskCreate)
			// get task status
			download.POST("status",
				controllers.FromJSON[slave.GetSlaveDownload](node.GetSlaveDownloadTaskParamCtx{}),
				middleware.PrepareSlaveDownloader(dep, node.GetSlaveDownloadTaskParamCtx{}),
				controllers.SlaveDownloadTaskStatus)
			// cancel offline download task
			download.POST("cancel",
				controllers.FromJSON[slave.CancelSlaveDownload](node.CancelSlaveDownloadTaskParamCtx{}),
				middleware.PrepareSlaveDownloader(dep, node.CancelSlaveDownloadTaskParamCtx{}),
				controllers.SlaveCancelDownloadTask)
			// select task files
			download.POST("select",
				controllers.FromJSON[slave.SetSlaveFilesToDownload](node.SelectSlaveDownloadFilesParamCtx{}),
				middleware.PrepareSlaveDownloader(dep, node.SelectSlaveDownloadFilesParamCtx{}),
				controllers.SlaveSelectFilesToDownload)
			// test downloader connection
			download.POST("test",
				controllers.FromJSON[slave.TestSlaveDownload](node.TestSlaveDownloadParamCtx{}),
				middleware.PrepareSlaveDownloader(dep, node.TestSlaveDownloadParamCtx{}),
				controllers.SlaveTestDownloader,
			)
		}

		// async tasks
		task := v4.Group("task")
		{
			task.PUT("",
				controllers.FromJSON[cluster.CreateSlaveTask](node.CreateSlaveTaskParamCtx{}),
				controllers.SlaveCreateTask)
			task.GET(":id",
				controllers.FromUri[node.GetSlaveTaskService](node.GetSlaveTaskParamCtx{}),
				controllers.SlaveGetTask)
			task.POST("cleanup",
				controllers.FromJSON[cluster.FolderCleanup](node.FolderCleanupParamCtx{}),
				controllers.SlaveCleanupFolder)
		}
	}
	return r
}

// initCORS initializes the CORS config
func initCORS(l logging.Logger, config conf.ConfigProvider, router *gin.Engine) {
	c := config.Cors()
	if c.AllowOrigins[0] != "UNSET" {
		router.Use(cors.New(cors.Config{
			AllowOrigins:     c.AllowOrigins,
			AllowMethods:     c.AllowMethods,
			AllowHeaders:     c.AllowHeaders,
			AllowCredentials: c.AllowCredentials,
			ExposeHeaders:    c.ExposeHeaders,
		}))
		return
	}

	// warning when CORS is not enabled in slave mode
	if config.System().Mode == conf.SlaveMode {
		l.Warning("You are running Fylvo as slave node, if you are using slave storage policy, please enable CORS feature in config file, otherwise file cannot be uploaded from Master basic.")
	}
}

// initMasterRouter initializes routes for master mode
func initMasterRouter(dep dependency.Dep) *gin.Engine {
	r := newGinEngine(dep)
	// CORS related
	initCORS(dep.Logger(), dep.ConfigProvider(), r) // Done

	/*
		static resources
	*/
	r.Use(gzip.Gzip(gzip.DefaultCompression, gzip.WithExcludedPaths([]string{"/api/"})))
	r.Use(middleware.SharePreview(dep))
	r.Use(middleware.FrontendFileHandler(dep))
	r.GET("manifest.json", controllers.Manifest)

	noAuth := r.Group(constants.APIPrefix)
	wopi := noAuth.Group("file/wopi", middleware.HashID(hashid.FileID), middleware.ViewerSessionValidation())
	{
		// get file info
		wopi.GET(":id", controllers.CheckFileInfo)
		// get file content
		wopi.GET(":id/contents", controllers.GetFile)
		// update file content
		wopi.POST(":id/contents", controllers.PutFile)
		// general file operations
		wopi.POST(":id", controllers.ModifyFile)
	}

	// Fylvo: the calls of the Office server for a file of a connected storage. The token
	// in the query is the permission (see explorer.openConnectedOffice).
	connectedWopi := noAuth.Group("fylvo/connected/wopi")
	{
		connectedWopi.GET(":session", controllers.FylvoConnectedOfficeInfo)
		connectedWopi.GET(":session/contents", controllers.FylvoConnectedOfficeGet)
		connectedWopi.POST(":session/contents", controllers.FylvoConnectedOfficePut)
		connectedWopi.POST(":session", controllers.FylvoConnectedOfficeOther)
	}

	// Fylvo: the preview of a file in a connected storage. The grant in the path is the
	// permission; the picture comes from the provider or from disk. Whatever sign-in a
	// request may carry plays no part: it is answered by the grant alone.
	connectedThumb := noAuth.Group("fylvo/connected/thumb", middleware.ContentCORS())
	{
		connectedThumb.OPTIONS("*option", middleware.ContentCORS())
		connectedThumb.GET(":grant/:name", middleware.Sandbox(), controllers.FylvoConnectedThumb)
	}

	// Fylvo: the browser comes back here from the provider of a storage that is being
	// connected. It carries no sign-in; a cookie ties it to the browser that started.
	noAuth.GET("fylvo/connected/callback", controllers.FylvoConnectedCallback)
	// Fylvo: where an app sends the system browser to connect a storage. No sign-in: the
	// ticket in the path belongs to the first browser that opens it, and the page names
	// the account before anything goes to the provider. Like the callback these pages
	// answer by themselves, whatever a session of this browser may be.
	noAuth.GET("fylvo/connected/start/:ticket", controllers.FylvoConnectedStart)
	noAuth.POST("fylvo/connected/start/:ticket", controllers.FylvoConnectedStart)

	v4 := r.Group(constants.APIPrefix)

	/*
		middleware
	*/
	v4.Use(middleware.Session(dep)) // Done

	// user session
	v4.Use(middleware.CurrentUser())

	// disable caching
	v4.Use(middleware.CacheControl()) // Done

	/*
		routes
	*/
	{
		// Redirect file source link
		source := r.Group("f")
		source.Use(middleware.ContentCORS())
		{
			source.OPTIONS("*option", middleware.ContentCORS())
			source.GET(":id/:name",
				middleware.HashID(hashid.SourceLinkID),
				controllers.AnonymousPermLink(false))
			source.GET("d/:id/:name",
				middleware.HashID(hashid.SourceLinkID),
				controllers.AnonymousPermLink(true))
		}

		shareShort := r.Group("s")
		{
			shareShort.GET(":id",
				controllers.FromUri[sharesvc.ShortLinkRedirectService](sharesvc.ShortLinkRedirectParamCtx{}),
				controllers.ShareRedirect,
			)
			shareShort.GET(":id/:password",
				controllers.FromUri[sharesvc.ShortLinkRedirectService](sharesvc.ShortLinkRedirectParamCtx{}),
				controllers.ShareRedirect,
			)
		}

		// global settings
		site := v4.Group("site")
		{
			// test routes
			site.GET("ping", controllers.Ping)
			// captcha
			site.GET("captcha", controllers.Captcha)
			// site global config
			site.GET("config/:section",
				controllers.FromUri[basic.GetSettingService](basic.GetSettingParamCtx{}),
				controllers.SiteConfig,
			)
		}

		// User authentication
		session := v4.Group("session")
		{
			token := session.Group("token")
			// Token based authentication
			{
				// user login
				token.POST("",
					middleware.CaptchaRequired(func(c *gin.Context) bool {
						return dep.SettingProvider().LoginCaptchaEnabled(c)
					}),
					controllers.FromJSON[usersvc.UserLoginService](usersvc.LoginParameterCtx{}),
					controllers.UserLoginValidation,
					controllers.UserIssueToken,
				)
				// 2-factor authentication
				token.POST("2fa",
					controllers.FromJSON[usersvc.OtpValidationService](usersvc.OtpValidationParameterCtx{}),
					controllers.UserLogin2FAValidation,
					controllers.UserIssueToken,
				)
				// Fylvo: set a new password with the ticket of a held sign-in
				token.POST("password",
					controllers.FromJSON[usersvc.PasswordChangeService](usersvc.PasswordChangeParameterCtx{}),
					controllers.UserFinishPasswordChange,
					controllers.UserIssueToken,
				)
				token.POST("refresh",
					middleware.RequiredScopes(types.ScopeOfflineAccess),
					controllers.FromJSON[usersvc.RefreshTokenService](usersvc.RefreshTokenParameterCtx{}),
					controllers.UserRefreshToken,
				)
				token.DELETE("",
					controllers.FromJSON[usersvc.RefreshTokenService](usersvc.RefreshTokenParameterCtx{}),
					controllers.UserSignOut,
				)
			}

			// Prepare login
			session.GET("prepare",
				controllers.FromQuery[usersvc.PrepareLoginService](usersvc.PrepareLoginParameterCtx{}),
				controllers.UserPrepareLogin,
			)

			oauthRouter := session.Group("oauth")
			{
				oauthRouter.GET("app/:app_id",
					controllers.FromUri[oauth.GetAppRegistrationService](oauth.GetAppRegistrationParamCtx{}),
					controllers.GetAppRegistration,
				)
				oauthRouter.POST("consent",
					middleware.LoginRequired(),
					controllers.FromJSON[oauth.GrantService](oauth.GrantParamCtx{}),
					controllers.GrantAppConsent,
				)
				oauthRouter.POST("consent/deny",
					middleware.LoginRequired(),
					controllers.FromJSON[oauth.GrantService](oauth.GrantParamCtx{}),
					controllers.DenyAppConsent,
				)
				oauthRouter.POST("token",
					controllers.FromForm[oauth.ExchangeTokenService](oauth.ExchangeTokenParamCtx{}),
					controllers.ExchangeToken,
				)
				oauthRouter.GET("userinfo",
					middleware.LoginRequired(),
					controllers.FromQuery[oauth.UserInfoService](oauth.UserInfoParamCtx{}),
					controllers.OpenIDUserInfo,
				)
				oauthRouter.DELETE("grant/:app_id",
					middleware.LoginRequired(),
					middleware.RequiredScopes(types.ScopeUserSecurityInfoWrite),
					controllers.FromUri[oauth.DeleteOAuthGrantService](oauth.DeleteOAuthGrantParamCtx{}),
					controllers.DeleteOAuthGrant,
				)
			}

			authn := session.Group("authn")
			{
				// WebAuthn login prepare
				authn.PUT("",
					middleware.IsFunctionEnabled(func(c *gin.Context) bool {
						return dep.SettingProvider().AuthnEnabled(c)
					}),
					controllers.StartLoginAuthn,
				)
				// WebAuthn finish login
				authn.POST("",
					middleware.IsFunctionEnabled(func(c *gin.Context) bool {
						return dep.SettingProvider().AuthnEnabled(c)
					}),
					controllers.FromJSON[usersvc.FinishPasskeyLoginService](usersvc.FinishPasskeyLoginParameterCtx{}),
					controllers.FinishLoginAuthn,
					controllers.UserIssueToken,
				)
			}
		}

		// user related routes
		user := v4.Group("user")
		{
			// user registration, done
			user.POST("",
				middleware.IsFunctionEnabled(func(c *gin.Context) bool { return usersvc.RegistrationOpen(c) }),
				middleware.CaptchaRequired(func(c *gin.Context) bool {
					return dep.SettingProvider().RegCaptchaEnabled(c)
				}),
				controllers.FromJSON[usersvc.UserRegisterService](usersvc.RegisterParameterCtx{}),
				controllers.UserRegister,
			)
			// reset password via the link in the email
			user.PATCH("reset/:id",
				middleware.HashID(hashid.UserID),
				controllers.FromJSON[usersvc.UserResetService](usersvc.UserResetParameterCtx{}),
				controllers.UserReset,
			)
			// send the password reset email
			user.POST("reset",
				middleware.CaptchaRequired(func(c *gin.Context) bool {
					return dep.SettingProvider().ForgotPasswordCaptchaEnabled(c)
				}),
				controllers.FromJSON[usersvc.UserResetEmailService](usersvc.UserResetEmailParameterCtx{}),
				controllers.UserSendReset,
			)
			// email activation, done
			user.GET("activate/:id",
				middleware.SignRequired(dep.GeneralAuth()),
				middleware.HashID(hashid.UserID),
				controllers.UserActivate,
			)
			// get user avatar
			user.GET("avatar/:id",
				middleware.HashID(hashid.UserID),
				controllers.FromQuery[usersvc.GetAvatarService](usersvc.GetAvatarServiceParamsCtx{}),
				controllers.GetUserAvatar,
			)
			// User info
			user.GET("info/:id", middleware.HashID(hashid.UserID), controllers.UserGet)
			// List user shares
			user.GET("shares/:id",
				middleware.HashID(hashid.UserID),
				controllers.FromQuery[sharesvc.ListShareService](sharesvc.ListShareParamCtx{}),
				controllers.ListPublicShare,
			)
		}

		// requires signature verification
		sign := v4.Group("")
		sign.Use(middleware.SignRequired(dep.GeneralAuth()))
		{
			file := sign.Group("file")
			{
				file.GET("archive/:sessionID/archive.zip",
					controllers.FromUri[explorer.ArchiveService](explorer.ArchiveParamCtx{}),
					controllers.DownloadArchive,
				)
			}
		}

		// Receive calls from slave node
		slave := v4.Group("slave")
		slave.Use(
			middleware.SlaveRPCSignRequired(),
		)
		{
			initSlaveFileRouter(slave)
			// Get credential
			slave.GET("credential/:id",
				controllers.FromUri[node.OauthCredentialService](node.OauthCredentialParamCtx{}),
				controllers.SlaveGetCredential)
			statelessUpload := slave.Group("statelessUpload")
			{
				// Prepare upload
				statelessUpload.PUT("prepare",
					controllers.FromJSON[fs.StatelessPrepareUploadService](node.StatelessPrepareUploadParamCtx{}),
					controllers.StatelessPrepareUpload)
				// Complete upload
				statelessUpload.POST("complete",
					controllers.FromJSON[fs.StatelessCompleteUploadService](node.StatelessCompleteUploadParamCtx{}),
					controllers.StatelessCompleteUpload)
				// On upload failed
				statelessUpload.POST("failed",
					controllers.FromJSON[fs.StatelessOnUploadFailedService](node.StatelessOnUploadFailedParamCtx{}),
					controllers.StatelessOnUploadFailed)
				// Create file
				statelessUpload.POST("create",
					controllers.FromJSON[fs.StatelessCreateFileService](node.StatelessCreateFileParamCtx{}),
					controllers.StatelessCreateFile)
			}
		}

		// Fylvo: the simulated provider of development builds (see pkg/connected/dev.go).
		// In an ordinary build there is none, and nothing is served here.
		if simulated := connected.DevHandler(); simulated != nil {
			v4.Any("fylvo/dev/*page", gin.WrapH(simulated))
		}

		// Fylvo: content of a file in a connected storage. The grant in the path is the
		// permission, checked in full on every request. Served like other file content:
		// in a sandbox, since it can be anything, and readable for players and viewers.
		connectedContent := v4.Group("fylvo/connected/content", middleware.ContentCORS())
		{
			connectedContent.OPTIONS("*option", middleware.ContentCORS())
			connectedContent.GET(":grant/:name", middleware.Sandbox(), controllers.FylvoConnectedContent)
			connectedContent.HEAD(":grant/:name", controllers.FylvoConnectedContent)
		}

		// callback endpoints
		callback := v4.Group("callback")
		{
			// remote policy upload callback
			callback.POST(
				"remote/:sessionID/:key",
				middleware.UseUploadSession(types.PolicyTypeRemote),
				middleware.RemoteCallbackAuth(),
				controllers.ProcessCallback(http.StatusOK, false),
			)
			// OSS callback
			callback.POST(
				"oss/:sessionID/:key",
				middleware.UseUploadSession(types.PolicyTypeOss),
				middleware.OSSCallbackAuth(),
				controllers.OSSCallbackValidate,
				controllers.ProcessCallback(http.StatusBadRequest, false),
			)
			// Upyun policy upload callback
			callback.POST(
				"upyun/:sessionID/:key",
				middleware.UseUploadSession(types.PolicyTypeUpyun),
				controllers.UpyunCallbackAuth,
				controllers.ProcessCallback(http.StatusBadRequest, false),
			)
			onedrive := callback.Group("onedrive")
			{
				// file upload finished
				onedrive.POST(
					":sessionID/:key",
					middleware.UseUploadSession(types.PolicyTypeOd),
					controllers.ProcessCallback(http.StatusOK, false),
				)
			}
			// Google Drive related
			gdrive := callback.Group("googledrive")
			{
				// OAuth finished
				gdrive.GET(
					"auth",
					controllers.GoogleDriveOAuth,
				)
			}
			// Tencent Cloud COS policy upload callback
			callback.GET(
				"cos/:sessionID/:key",
				middleware.UseUploadSession(types.PolicyTypeCos),
				controllers.ProcessCallback(http.StatusBadRequest, false),
			)
			// AWS S3 policy upload callback
			callback.GET(
				"s3/:sessionID/:key",
				middleware.UseUploadSession(types.PolicyTypeS3),
				controllers.ProcessCallback(http.StatusBadRequest, false),
			)
			// Kingsoft ks3 policy upload callback
			callback.GET(
				"ks3/:sessionID/:key",
				middleware.UseUploadSession(types.PolicyTypeKs3),
				controllers.ProcessCallback(http.StatusBadRequest, false),
			)
			// Huawei OBS upload callback
			callback.POST(
				"obs/:sessionID/:key",
				middleware.UseUploadSession(types.PolicyTypeObs),
				controllers.ProcessCallback(http.StatusBadRequest, false),
			)
			// Qiniu callback
			callback.POST(
				"qiniu/:sessionID/:key",
				middleware.UseUploadSession(types.PolicyTypeQiniu),
				controllers.QiniuCallbackValidate,
				controllers.ProcessCallback(http.StatusBadRequest, true),
			)
		}

		// Workflows
		wf := v4.Group("workflow")
		wf.Use(middleware.LoginRequired())
		wf.Use(middleware.RequiredScopes(types.ScopeWorkflowRead))
		{
			// List
			wf.GET("",
				controllers.FromQuery[explorer.ListTaskService](explorer.ListTaskParamCtx{}),
				controllers.ListTasks,
			)
			// GetTaskProgress
			wf.GET("progress/:id",
				middleware.HashID(hashid.TaskID),
				controllers.GetTaskPhaseProgress,
			)
			// Create task to create an archive file
			wf.POST("archive",
				middleware.RequiredScopes(types.ScopeWorkflowWrite),
				controllers.FromJSON[explorer.ArchiveWorkflowService](explorer.CreateArchiveParamCtx{}),
				controllers.CreateArchive,
			)
			// Create task to extract an archive file
			wf.POST("extract",
				middleware.RequiredScopes(types.ScopeWorkflowWrite),
				controllers.FromJSON[explorer.ArchiveWorkflowService](explorer.CreateArchiveParamCtx{}),
				controllers.ExtractArchive,
			)

			remoteDownload := wf.Group("download")
			{
				// Create task to download a file
				remoteDownload.POST("",
					middleware.RequiredScopes(types.ScopeWorkflowWrite),
					controllers.FromJSON[explorer.DownloadWorkflowService](explorer.CreateDownloadParamCtx{}),
					controllers.CreateRemoteDownload,
				)
				// Set download target
				remoteDownload.PATCH(":id",
					middleware.RequiredScopes(types.ScopeWorkflowWrite),
					middleware.HashID(hashid.TaskID),
					controllers.FromJSON[explorer.SetDownloadFilesService](explorer.SetDownloadFilesParamCtx{}),
					controllers.SetDownloadTaskTarget,
				)
				remoteDownload.DELETE(":id",
					middleware.RequiredScopes(types.ScopeWorkflowWrite),
					middleware.HashID(hashid.TaskID),
					controllers.CancelDownloadTask,
				)
			}

			// Fylvo: copy between Fylvo and connected storages, as a task.
			wf.POST("copy",
				middleware.RequiredScopes(types.ScopeWorkflowWrite, types.ScopeFilesWrite),
				controllers.FromJSON[explorer.ConnectedCopyService](explorer.ConnectedCopyParamCtx{}),
				controllers.CreateConnectedCopy,
			)
			wf.DELETE("copy/:id",
				middleware.RequiredScopes(types.ScopeWorkflowWrite),
				middleware.HashID(hashid.TaskID),
				controllers.CancelConnectedCopy,
			)
		}

		// files
		file := v4.Group("file")
		file.Use(middleware.RequiredScopes(types.ScopeFilesRead))
		{
			// List files
			file.GET("",
				controllers.FromQuery[explorer.ListFileService](explorer.ListFileParameterCtx{}),
				controllers.ListDirectory,
			)
			file.GET("archive",
				controllers.FromQuery[explorer.ArchiveListFilesService](explorer.ArchiveListFilesParamCtx{}),
				controllers.ListArchiveFiles,
			)
			// Create file
			file.POST("create",
				middleware.RequiredScopes(types.ScopeFilesWrite),
				controllers.FromJSON[explorer.CreateFileService](explorer.CreateFileParameterCtx{}),
				controllers.CreateFile,
			)
			// Rename file
			file.POST("rename",
				middleware.RequiredScopes(types.ScopeFilesWrite),
				controllers.FromJSON[explorer.RenameFileService](explorer.RenameFileParameterCtx{}),
				controllers.RenameFile,
			)
			// Move or copy files
			file.POST("move",
				middleware.RequiredScopes(types.ScopeFilesWrite),
				controllers.FromJSON[explorer.MoveFileService](explorer.MoveFileParameterCtx{}),
				middleware.ValidateBatchFileCount(dep, explorer.MoveFileParameterCtx{}),
				controllers.MoveFile)
			// Get URL of the file for preview/download
			file.POST("url",
				middleware.ContextHint(),
				controllers.FromJSON[explorer.FileURLService](explorer.FileURLParameterCtx{}),
				middleware.ValidateBatchFileCount(dep, explorer.FileURLParameterCtx{}),
				controllers.FileURL,
			)
			// Update file content
			file.PUT("content",
				middleware.RequiredScopes(types.ScopeFilesWrite),
				controllers.FromQuery[explorer.FileUpdateService](explorer.FileUpdateParameterCtx{}),
				controllers.PutContent)
			// Get entity content for preview/download
			content := file.Group("content")
			content.Use(middleware.ContentCORS())
			{
				content.OPTIONS("*option", middleware.ContentCORS())
				content.GET(":id/:speed/:name",
					middleware.SignRequired(dep.GeneralAuth()),
					middleware.HashID(hashid.EntityID),
					middleware.Sandbox(),
					controllers.FromUri[explorer.EntityDownloadService](explorer.EntityDownloadParameterCtx{}),
					controllers.ServeEntity,
				)
				content.HEAD(":id/:speed/:name",
					middleware.SignRequired(dep.GeneralAuth()),
					middleware.HashID(hashid.EntityID),
					controllers.FromUri[explorer.EntityDownloadService](explorer.EntityDownloadParameterCtx{}),
					controllers.ServeEntity,
				)
			}
			// get thumb
			file.GET("thumb",
				middleware.ContextHint(),
				controllers.FromQuery[explorer.FileThumbService](explorer.FileThumbParameterCtx{}),
				controllers.Thumb,
			)
			// Delete files
			file.DELETE("",
				middleware.RequiredScopes(types.ScopeFilesWrite),
				controllers.FromJSON[explorer.DeleteFileService](explorer.DeleteFileParameterCtx{}),
				middleware.ValidateBatchFileCount(dep, explorer.DeleteFileParameterCtx{}),
				controllers.Delete,
			)
			// Empty trash bin
			file.DELETE("trash",
				middleware.RequiredScopes(types.ScopeFilesWrite),
				controllers.EmptyTrash,
			)
			// Force unlock
			file.DELETE("lock",
				middleware.RequiredScopes(types.ScopeFilesWrite),
				controllers.FromJSON[explorer.UnlockFileService](explorer.UnlockFileParameterCtx{}),
				controllers.Unlock,
			)
			// Restore files
			file.POST("restore",
				middleware.RequiredScopes(types.ScopeFilesWrite),
				controllers.FromJSON[explorer.DeleteFileService](explorer.DeleteFileParameterCtx{}),
				middleware.ValidateBatchFileCount(dep, explorer.DeleteFileParameterCtx{}),
				controllers.Restore,
			)
			// Patch metadata
			file.PATCH("metadata",
				middleware.RequiredScopes(types.ScopeFilesWrite),
				controllers.FromJSON[explorer.PatchMetadataService](explorer.PatchMetadataParameterCtx{}),
				middleware.ValidateBatchFileCount(dep, explorer.PatchMetadataParameterCtx{}),
				controllers.PatchMetadata,
			)
			// Upload related
			upload := file.Group("upload", middleware.RequiredScopes(types.ScopeFilesWrite))
			{
				// Create upload session
				upload.PUT("",
					controllers.FromJSON[explorer.CreateUploadSessionService](explorer.CreateUploadSessionParameterCtx{}),
					controllers.CreateUploadSession,
				)
				// Upload file data
				upload.POST(":sessionId/:index",
					controllers.FromUri[explorer.UploadService](explorer.UploadParameterCtx{}),
					controllers.FileUpload,
				)
				upload.DELETE("",
					controllers.FromJSON[explorer.DeleteUploadSessionService](explorer.DeleteUploadSessionParameterCtx{}),
					controllers.DeleteUploadSession,
				)
			}
			// Pin file
			pin := file.Group("pin", middleware.RequiredScopes(types.ScopeFilesWrite))
			{
				// Pin file
				pin.PUT("",
					controllers.FromJSON[explorer.PinFileService](explorer.PinFileParameterCtx{}),
					controllers.Pin,
				)
				// Unpin file
				pin.DELETE("",
					controllers.FromJSON[explorer.PinFileService](explorer.PinFileParameterCtx{}),
					controllers.Unpin,
				)
			}
			// Get file info
			file.GET("info",
				controllers.FromQuery[explorer.GetFileInfoService](explorer.GetFileInfoParameterCtx{}),
				controllers.GetFileInfo,
			)
			// Fylvo: history of a file or folder, for its owner
			file.GET("activity",
				middleware.LoginRequired(),
				controllers.FromQuery[fylvosvc.FileActivityService](fylvosvc.FileActivityParamCtx{}),
				controllers.FylvoFileActivity,
			)
			// Version management
			version := file.Group("version", middleware.RequiredScopes(types.ScopeFilesWrite))
			{
				// Set current version
				version.POST("current",
					controllers.FromJSON[explorer.SetCurrentVersionService](explorer.SetCurrentVersionParamCtx{}),
					controllers.SetCurrentVersion,
				)
				// Delete a version from a file
				version.DELETE("",
					controllers.FromJSON[explorer.DeleteVersionService](explorer.DeleteVersionParamCtx{}),
					controllers.DeleteVersion,
				)
			}
			file.PUT("viewerSession",
				middleware.RequiredScopes(types.ScopeFilesWrite),
				controllers.FromJSON[explorer.CreateViewerSessionService](explorer.CreateViewerSessionParamCtx{}),
				controllers.CreateViewerSession,
			)
			// Create task to import files
			wf.POST("import",
				middleware.IsAdmin(),
				middleware.RequiredScopes(types.ScopeWorkflowWrite, types.ScopeAdminWrite),
				controllers.FromJSON[explorer.ImportWorkflowService](explorer.CreateImportParamCtx{}),
				controllers.ImportFiles,
			)
			// Create task to import files
			wf.POST("rebuildFtsIndex",
				middleware.IsAdmin(),
				middleware.RequiredScopes(types.ScopeWorkflowWrite, types.ScopeAdminWrite),
				controllers.FromJSON[explorer.RebuildFTSIndexWorkflowService](explorer.CreateRebuildFTSIndexParamCtx{}),
				controllers.RebuildFTSIndex,
			)

			// get file external link
			source := file.Group("source")
			{
				source.PUT("",
					controllers.FromJSON[explorer.GetDirectLinkService](explorer.GetDirectLinkParamCtx{}),
					middleware.ValidateBatchFileCount(dep, explorer.GetDirectLinkParamCtx{}),
					controllers.GetSource,
				)
				source.DELETE(":id",
					middleware.HashID(hashid.SourceLinkID),
					controllers.DeleteDirectLink,
				)
			}
			// Patch view
			file.PATCH("view",
				middleware.RequiredScopes(types.ScopeFilesWrite),
				controllers.FromJSON[explorer.PatchViewService](explorer.PatchViewParameterCtx{}),
				controllers.PatchView,
			)

			// Server event push
			file.GET("events",
				middleware.LoginRequired(),
				middleware.IsFunctionEnabled(func(c *gin.Context) bool {
					return dep.SettingProvider().EventHubEnabled(c)
				}),
				controllers.FromQuery[explorer.ExplorerEventService](explorer.ExplorerEventParamCtx{}),
				controllers.HandleExplorerEventsPush,
			)

			// Full text search
			file.GET("search",
				middleware.LoginRequired(),
				middleware.IsFunctionEnabled(func(c *gin.Context) bool {
					return dep.SettingProvider().FTSEnabled(c)
				}),
				controllers.FromQuery[explorer.FulltextSearchService](explorer.FulltextSearchParamCtx{}),
				controllers.FulltextSearch,
			)
		}

		// sharing related
		share := v4.Group("share")
		share.Use(middleware.RequiredScopes(types.ScopeSharesRead))
		{
			// Create share link
			share.PUT("",
				middleware.LoginRequired(),
				middleware.RequiredScopes(types.ScopeSharesWrite),
				controllers.FromJSON[sharesvc.ShareCreateService](sharesvc.ShareCreateParamCtx{}),
				controllers.CreateShare,
			)
			// Edit existing share link
			share.POST(":id",
				middleware.LoginRequired(),
				middleware.RequiredScopes(types.ScopeSharesWrite),
				middleware.HashID(hashid.ShareID),
				controllers.FromJSON[sharesvc.ShareCreateService](sharesvc.ShareCreateParamCtx{}),
				controllers.EditShare,
			)
			// Get share link info
			share.GET("info/:id",
				middleware.HashID(hashid.ShareID),
				controllers.FromQuery[sharesvc.ShareInfoService](sharesvc.ShareInfoParamCtx{}),
				controllers.GetShare,
			)
			// List my shares
			share.GET("",
				middleware.LoginRequired(),
				controllers.FromQuery[sharesvc.ListShareService](sharesvc.ListShareParamCtx{}),
				controllers.ListShare,
			)
			// delete share
			share.DELETE(":id",
				middleware.LoginRequired(),
				middleware.RequiredScopes(types.ScopeSharesWrite),
				middleware.HashID(hashid.ShareID),
				controllers.DeleteShare,
			)
			share.DELETE("",
				middleware.LoginRequired(),
				middleware.RequiredScopes(types.ScopeSharesWrite),
				controllers.FromJSON[sharesvc.BatchDeleteShareService](sharesvc.BatchDeleteParamCtx{}),
				controllers.BatchDeleteShare,
			)
			//// get README text file content
			//share.GET("readme/:id",
			//	middleware.CheckShareUnlocked(),
			//	controllers.PreviewShareReadme,
			//)
			//// report share
			//share.POST("report/:id",
			//	middleware.CheckShareUnlocked(),
			//	controllers.ReportShare,
			//)
		}

		// requires login
		auth := v4.Group("")
		auth.Use(middleware.LoginRequired())
		{
			// Fylvo: connected storages of the account. Browsing them needs the file scope,
			// connecting and disconnecting the scope for the security of the account.
			fylvoConnected := auth.Group("fylvo/connected")
			{
				fylvoConnected.GET("",
					middleware.RequiredScopes(types.ScopeFilesRead),
					controllers.FylvoConnectedList,
				)
				fylvoConnected.GET(":id/capacity",
					middleware.RequiredScopes(types.ScopeFilesRead),
					controllers.FylvoConnectedCapacity,
				)
				fylvoConnected.GET("upload/:session",
					middleware.RequiredScopes(types.ScopeFilesRead),
					controllers.FylvoConnectedUploadState,
				)
				fylvoConnected.GET("office/:session",
					middleware.RequiredScopes(types.ScopeFilesRead),
					controllers.FylvoConnectedOfficeState,
				)
				manage := fylvoConnected.Group("", middleware.RequiredScopes(types.ScopeUserSecurityInfoWrite))
				manage.POST("authorize",
					controllers.FromJSON[fylvosvc.ConnectedAuthorizeService](fylvosvc.ConnectedAuthorizeParamCtx{}),
					controllers.FylvoConnectedAuthorize,
				)
				manage.POST("finish",
					controllers.FromJSON[fylvosvc.ConnectedFinishService](fylvosvc.ConnectedFinishParamCtx{}),
					controllers.FylvoConnectedFinish,
				)
				manage.PATCH(":id",
					controllers.FromJSON[fylvosvc.ConnectedRenameService](fylvosvc.ConnectedRenameParamCtx{}),
					controllers.FylvoConnectedRename,
				)
				manage.DELETE(":id", controllers.FylvoConnectedDisconnect)
			}

			// Fylvo: in-app updates of the Android app, for signed-in users only.
			fylvoAndroid := auth.Group("fylvo/android")
			{
				fylvoAndroid.GET("latest", controllers.FylvoAndroidLatest)
				fylvoAndroid.GET("apk/:sha256", controllers.FylvoAndroidApk)
			}
			// Fylvo: account preferences, favorites and tags (design/customize-plan.md). Every
			// answer holds file IDs or addresses, so all need Files.Read.
			fylvoPrefs := auth.Group("fylvo", middleware.RequiredScopes(types.ScopeFilesRead))
			{
				fylvoPrefs.GET("prefs", middleware.RequiredScopes(types.ScopeUserInfoRead), controllers.FylvoGetPrefs)
				fylvoPrefs.PATCH("prefs", middleware.RequiredScopes(types.ScopeUserInfoWrite, types.ScopeFilesWrite),
					controllers.FylvoPatchPrefs)
				fylvoPrefs.DELETE("prefs", middleware.RequiredScopes(types.ScopeUserInfoWrite, types.ScopeFilesWrite),
					controllers.FylvoResetPrefs)
				fylvoPrefs.GET("favorites", controllers.FylvoListFavorites)
				fylvoPrefs.PUT("favorites", middleware.RequiredScopes(types.ScopeFilesWrite),
					controllers.FromJSON[fylvosvc.FavoritesAddService](fylvosvc.FavoritesAddParamCtx{}),
					controllers.FylvoAddFavorites)
				fylvoPrefs.DELETE("favorites", middleware.RequiredScopes(types.ScopeFilesWrite),
					controllers.FromJSON[fylvosvc.FavoritesRemoveService](fylvosvc.FavoritesRemoveParamCtx{}),
					controllers.FylvoRemoveFavorites)
				fylvoPrefs.GET("tags", controllers.FylvoListTags)
			}
			// Fylvo: in-app updates of Fylvo Desktop, per platform, for signed-in users only.
			fylvoDesktop := auth.Group("fylvo/desktop/:platform")
			{
				fylvoDesktop.GET("latest", controllers.FylvoDesktopLatest)
				fylvoDesktop.GET("package/:sha256", controllers.FylvoDesktopPackage)
			}

			// admin
			admin := auth.Group("admin", middleware.IsAdmin())
			admin.Use(middleware.RequiredScopes(types.ScopeAdminRead))
			{
				admin.GET("summary",
					controllers.FromQuery[adminsvc.SummaryService](adminsvc.SummaryParamCtx{}),
					controllers.AdminSummary,
				)
				// Fylvo: the whole activity log
				admin.GET("activity",
					controllers.FromQuery[fylvosvc.AdminActivityService](fylvosvc.AdminActivityParamCtx{}),
					controllers.FylvoAdminActivity,
				)
				// Fylvo: the OAuth clients of the installation for connected storages
				admin.GET("fylvo/connected", controllers.FylvoConnectedAdmin)
				admin.PUT("fylvo/connected",
					middleware.RequiredScopes(types.ScopeAdminWrite),
					controllers.FromJSON[fylvosvc.ConnectedAdminService](fylvosvc.ConnectedAdminParamCtx{}),
					controllers.FylvoConnectedAdminSet,
				)

				settings := admin.Group("settings")
				{
					// Get settings
					settings.POST("",
						controllers.FromJSON[adminsvc.GetSettingService](adminsvc.GetSettingParamCtx{}),
						controllers.AdminGetSettings,
					)
					// Patch settings
					settings.PATCH("",
						middleware.RequiredScopes(types.ScopeAdminWrite),
						controllers.FromJSON[adminsvc.SetSettingService](adminsvc.SetSettingParamCtx{}),
						controllers.AdminSetSettings,
					)
				}

				// group management
				group := admin.Group("group")
				{
					// list groups
					group.POST("",
						controllers.FromJSON[adminsvc.AdminListService](adminsvc.AdminListServiceParamsCtx{}),
						controllers.AdminListGroups,
					)
					// get group
					group.GET(":id",
						controllers.FromUri[adminsvc.SingleGroupService](adminsvc.SingleGroupParamCtx{}),
						controllers.AdminGetGroup,
					)
					// create group
					group.PUT("",
						middleware.RequiredScopes(types.ScopeAdminWrite),
						controllers.FromJSON[adminsvc.UpsertGroupService](adminsvc.UpsertGroupParamCtx{}),
						controllers.AdminCreateGroup,
					)
					// update group
					group.PUT(":id",
						middleware.RequiredScopes(types.ScopeAdminWrite),
						controllers.FromJSON[adminsvc.UpsertGroupService](adminsvc.UpsertGroupParamCtx{}),
						controllers.AdminUpdateGroup,
					)
					// delete group
					group.DELETE(":id",
						middleware.RequiredScopes(types.ScopeAdminWrite),
						controllers.FromUri[adminsvc.SingleGroupService](adminsvc.SingleGroupParamCtx{}),
						controllers.AdminDeleteGroup,
					)
				}

				tool := admin.Group("tool")
				{
					tool.GET("wopi",
						middleware.RequiredScopes(types.ScopeAdminWrite),
						controllers.FromQuery[adminsvc.FetchWOPIDiscoveryService](adminsvc.FetchWOPIDiscoveryParamCtx{}),
						controllers.AdminFetchWopi,
					)
					tool.POST("thumbExecutable",
						middleware.RequiredScopes(types.ScopeAdminWrite),
						controllers.FromJSON[adminsvc.ThumbGeneratorTestService](adminsvc.ThumbGeneratorTestParamCtx{}),
						controllers.AdminTestThumbGenerator)
					tool.POST("mail",
						middleware.RequiredScopes(types.ScopeAdminWrite),
						controllers.FromJSON[adminsvc.TestSMTPService](adminsvc.TestSMTPParamCtx{}),
						controllers.AdminSendTestMail,
					)
					tool.DELETE("entityUrlCache",
						middleware.RequiredScopes(types.ScopeAdminWrite),
						controllers.AdminClearEntityUrlCache,
					)
				}

				queue := admin.Group("queue")
				{
					queue.GET("metrics", controllers.AdminGetQueueMetrics)
					// List tasks
					queue.POST("",
						controllers.FromJSON[adminsvc.AdminListService](adminsvc.AdminListServiceParamsCtx{}),
						controllers.AdminListTasks,
					)
					// Get task
					queue.GET(":id",
						controllers.FromUri[adminsvc.SingleTaskService](adminsvc.SingleTaskParamCtx{}),
						controllers.AdminGetTask,
					)
					// Batch delete task
					queue.POST("batch/delete",
						middleware.RequiredScopes(types.ScopeAdminWrite),
						controllers.FromJSON[adminsvc.BatchTaskService](adminsvc.BatchTaskParamCtx{}),
						controllers.AdminBatchDeleteTask,
					)
					// Cleanup tasks
					queue.POST("cleanup",
						middleware.RequiredScopes(types.ScopeAdminWrite),
						controllers.FromJSON[adminsvc.CleanupTaskService](adminsvc.CleanupTaskParameterCtx{}),
						controllers.AdminCleanupTask,
					)
					// // list tasks
					// queue.POST("list", controllers.AdminListTask)
					// // new file import task
					// queue.POST("import", controllers.AdminCreateImportTask)
				}

				// storage policy management
				policy := admin.Group("policy")
				{
					// list storage policies
					policy.POST("",
						controllers.FromJSON[adminsvc.AdminListService](adminsvc.AdminListServiceParamsCtx{}),
						controllers.AdminListPolicies,
					)
					// get storage policy details
					policy.GET(":id",
						controllers.FromUri[adminsvc.SingleStoragePolicyService](adminsvc.GetStoragePolicyParamCtx{}),
						controllers.AdminGetPolicy,
					)
					// create storage policy
					policy.PUT("",
						middleware.RequiredScopes(types.ScopeAdminWrite),
						controllers.FromJSON[adminsvc.CreateStoragePolicyService](adminsvc.CreateStoragePolicyParamCtx{}),
						controllers.AdminCreatePolicy,
					)
					// update storage policy
					policy.PUT(":id",
						middleware.RequiredScopes(types.ScopeAdminWrite),
						controllers.FromJSON[adminsvc.UpdateStoragePolicyService](adminsvc.UpdateStoragePolicyParamCtx{}),
						controllers.AdminUpdatePolicy,
					)
					// create CORS policy
					policy.POST("cors",
						middleware.RequiredScopes(types.ScopeAdminWrite),
						controllers.FromJSON[adminsvc.CreateStoragePolicyCorsService](adminsvc.CreateStoragePolicyCorsParamCtx{}),
						controllers.AdminCreateStoragePolicyCors,
					)
					// // get OneDrive OAuth URL
					oauth := policy.Group("oauth")
					{
						// get OneDrive OAuth URL
						oauth.POST("signin",
							middleware.RequiredScopes(types.ScopeAdminWrite),
							controllers.FromJSON[adminsvc.GetOauthRedirectService](adminsvc.GetOauthRedirectParamCtx{}),
							controllers.AdminOdOAuthURL,
						)
						// get OAuth callback URL
						oauth.GET("redirect", controllers.AdminGetPolicyOAuthCallbackURL)
						oauth.GET("status/:id",
							controllers.FromUri[adminsvc.SingleStoragePolicyService](adminsvc.GetStoragePolicyParamCtx{}),
							controllers.AdminGetPolicyOAuthStatus,
						)
						oauth.POST("callback",
							middleware.RequiredScopes(types.ScopeAdminWrite),
							controllers.FromJSON[adminsvc.FinishOauthCallbackService](adminsvc.FinishOauthCallbackParamCtx{}),
							controllers.AdminFinishOauthCallback,
						)
						oauth.GET("root/:id",
							controllers.FromUri[adminsvc.SingleStoragePolicyService](adminsvc.GetStoragePolicyParamCtx{}),
							controllers.AdminGetSharePointDriverRoot,
						)
					}

					// // get storage policy
					// policy.GET(":id", controllers.AdminGetPolicy)
					// delete storage policy
					policy.DELETE(":id",
						middleware.RequiredScopes(types.ScopeAdminWrite),
						controllers.FromUri[adminsvc.SingleStoragePolicyService](adminsvc.GetStoragePolicyParamCtx{}),
						controllers.AdminDeletePolicy,
					)
				}

				node := admin.Group("node")
				{
					node.POST("",
						controllers.FromJSON[adminsvc.AdminListService](adminsvc.AdminListServiceParamsCtx{}),
						controllers.AdminListNodes,
					)
					node.GET(":id",
						controllers.FromUri[adminsvc.SingleNodeService](adminsvc.SingleNodeParamCtx{}),
						controllers.AdminGetNode,
					)
					node.POST("test",
						middleware.RequiredScopes(types.ScopeAdminWrite),
						controllers.FromJSON[adminsvc.TestNodeService](adminsvc.TestNodeParamCtx{}),
						controllers.AdminTestSlave,
					)
					node.POST("test/downloader",
						controllers.FromJSON[adminsvc.TestNodeDownloaderService](adminsvc.TestNodeDownloaderParamCtx{}),
						middleware.RequiredScopes(types.ScopeAdminWrite),
						controllers.AdminTestDownloader,
					)
					node.PUT("",
						middleware.RequiredScopes(types.ScopeAdminWrite),
						controllers.FromJSON[adminsvc.UpsertNodeService](adminsvc.UpsertNodeParamCtx{}),
						controllers.AdminCreateNode,
					)
					node.PUT(":id",
						middleware.RequiredScopes(types.ScopeAdminWrite),
						controllers.FromJSON[adminsvc.UpsertNodeService](adminsvc.UpsertNodeParamCtx{}),
						controllers.AdminUpdateNode,
					)
					node.DELETE(":id",
						middleware.RequiredScopes(types.ScopeAdminWrite),
						controllers.FromUri[adminsvc.SingleNodeService](adminsvc.SingleNodeParamCtx{}),
						controllers.AdminDeleteNode,
					)
				}

				oauthClient := admin.Group("oauthClient")
				{
					// List OAuth clients
					oauthClient.POST("",
						controllers.FromJSON[adminsvc.AdminListService](adminsvc.AdminListServiceParamsCtx{}),
						controllers.AdminListOAuthClients,
					)
					// Get OAuth client
					oauthClient.GET(":id",
						controllers.FromUri[adminsvc.SingleOAuthClientService](adminsvc.SingleOAuthClientParamCtx{}),
						controllers.AdminGetOAuthClient,
					)
					// Create OAuth client
					oauthClient.PUT("",
						middleware.RequiredScopes(types.ScopeAdminWrite),
						controllers.FromJSON[adminsvc.UpsertOAuthClientService](adminsvc.UpsertOAuthClientParamCtx{}),
						controllers.AdminCreateOAuthClient,
					)
					// Update OAuth client
					oauthClient.PUT(":id",
						middleware.RequiredScopes(types.ScopeAdminWrite),
						controllers.FromJSON[adminsvc.UpsertOAuthClientService](adminsvc.UpsertOAuthClientParamCtx{}),
						controllers.AdminUpdateOAuthClient,
					)
					// Delete OAuth client
					oauthClient.DELETE(":id",
						middleware.RequiredScopes(types.ScopeAdminWrite),
						controllers.FromUri[adminsvc.SingleOAuthClientService](adminsvc.SingleOAuthClientParamCtx{}),
						controllers.AdminDeleteOAuthClient,
					)
					// Batch delete OAuth clients
					oauthClient.POST("batch/delete",
						middleware.RequiredScopes(types.ScopeAdminWrite),
						controllers.FromJSON[adminsvc.BatchOAuthClientService](adminsvc.BatchOAuthClientParamCtx{}),
						controllers.AdminBatchDeleteOAuthClient,
					)
				}

				user := admin.Group("user")
				{
					// list users
					user.POST("",
						controllers.FromJSON[adminsvc.AdminListService](adminsvc.AdminListServiceParamsCtx{}),
						controllers.AdminListUsers,
					)
					// get user
					user.GET(":id",
						controllers.FromUri[adminsvc.SingleUserService](adminsvc.SingleUserParamCtx{}),
						controllers.AdminGetUser,
					)
					// update user
					user.PUT(":id",
						middleware.RequiredScopes(types.ScopeAdminWrite),
						controllers.FromJSON[adminsvc.UpsertUserService](adminsvc.UpsertUserParamCtx{}),
						controllers.AdminUpdateUser,
					)
					// create user
					user.PUT("",
						middleware.RequiredScopes(types.ScopeAdminWrite),
						controllers.FromJSON[adminsvc.UpsertUserService](adminsvc.UpsertUserParamCtx{}),
						controllers.AdminCreateUser,
					)
					batch := user.Group("batch")
					{
						// delete users in batch
						batch.POST("delete",
							middleware.RequiredScopes(types.ScopeAdminWrite),
							controllers.FromJSON[adminsvc.BatchUserService](adminsvc.BatchUserParamCtx{}),
							controllers.AdminDeleteUser,
						)
					}
					user.POST(":id/calibrate",
						middleware.RequiredScopes(types.ScopeAdminWrite),
						controllers.FromUri[adminsvc.SingleUserService](adminsvc.SingleUserParamCtx{}),
						controllers.AdminCalibrateStorage,
					)
				}

				file := admin.Group("file")
				{
					// list files
					file.POST("",
						controllers.FromJSON[adminsvc.AdminListService](adminsvc.AdminListServiceParamsCtx{}),
						controllers.AdminListFiles,
					)
					// get file
					file.GET(":id",
						controllers.FromUri[adminsvc.SingleFileService](adminsvc.SingleFileParamCtx{}),
						controllers.AdminGetFile,
					)
					// update file
					file.PUT(":id",
						middleware.RequiredScopes(types.ScopeAdminWrite),
						controllers.FromJSON[adminsvc.UpsertFileService](adminsvc.UpsertFileParamCtx{}),
						controllers.AdminUpdateFile,
					)
					// get file URL
					file.GET("url/:id",
						controllers.FromUri[adminsvc.SingleFileService](adminsvc.SingleFileParamCtx{}),
						controllers.AdminGetFileUrl,
					)
					// delete files in batch
					file.POST("batch/delete",
						middleware.RequiredScopes(types.ScopeAdminWrite),
						controllers.FromJSON[adminsvc.BatchFileService](adminsvc.BatchFileParamCtx{}),
						controllers.AdminBatchDeleteFile,
					)
				}

				entity := admin.Group("entity")
				{
					// List blobs
					entity.POST("",
						controllers.FromJSON[adminsvc.AdminListService](adminsvc.AdminListServiceParamsCtx{}),
						controllers.AdminListEntities,
					)
					// Get entity
					entity.GET(":id",
						controllers.FromUri[adminsvc.SingleEntityService](adminsvc.SingleEntityParamCtx{}),
						controllers.AdminGetEntity,
					)
					// Batch delete entity
					entity.POST("batch/delete",
						middleware.RequiredScopes(types.ScopeAdminWrite),
						controllers.FromJSON[adminsvc.BatchEntityService](adminsvc.BatchEntityParamCtx{}),
						controllers.AdminBatchDeleteEntity,
					)
					// Get entity url
					entity.GET("url/:id",
						controllers.FromUri[adminsvc.SingleEntityService](adminsvc.SingleEntityParamCtx{}),
						controllers.AdminGetEntityUrl,
					)
				}

				share := admin.Group("share")
				{
					// List shares
					share.POST("",
						controllers.FromJSON[adminsvc.AdminListService](adminsvc.AdminListServiceParamsCtx{}),
						controllers.AdminListShares,
					)
					// Get share
					share.GET(":id",
						controllers.FromUri[adminsvc.SingleShareService](adminsvc.SingleShareParamCtx{}),
						controllers.AdminGetShare,
					)
					// Batch delete shares
					share.POST("batch/delete",
						middleware.RequiredScopes(types.ScopeAdminWrite),
						controllers.FromJSON[adminsvc.BatchShareService](adminsvc.BatchShareParamCtx{}),
						controllers.AdminBatchDeleteShare,
					)
				}
			}

			// user
			user := auth.Group("user")
			{
				// current logged-in user info
				user.GET("me", middleware.RequiredScopes(types.ScopeUserInfoRead), controllers.UserMe)
				// storage info
				user.GET("capacity", middleware.RequiredScopes(types.ScopeUserInfoRead), controllers.UserStorage)
				// Search user by keywords
				user.GET("search",
					middleware.RequiredScopes(types.ScopeUserInfoRead),
					controllers.FromQuery[usersvc.SearchUserService](usersvc.SearchUserParamCtx{}),
					controllers.UserSearch,
				)

				// WebAuthn registration related
				authn := user.Group("authn",
					middleware.RequiredScopes(types.ScopeUserSecurityInfoWrite),
					middleware.IsFunctionEnabled(func(c *gin.Context) bool {
						return dep.SettingProvider().AuthnEnabled(c)
					}))
				{
					authn.PUT("", controllers.StartRegAuthn)
					authn.POST("",
						controllers.FromJSON[usersvc.FinishPasskeyRegisterService](usersvc.FinishPasskeyRegisterParameterCtx{}),
						controllers.FinishRegAuthn,
					)
					authn.DELETE("",
						controllers.FromQuery[usersvc.DeletePasskeyService](usersvc.DeletePasskeyParameterCtx{}),
						controllers.UserDeletePasskey,
					)
				}

				// user settings
				setting := user.Group("setting")
				setting.Use(middleware.RequiredScopes(types.ScopeUserInfoRead))
				{
					// get current user settings
					setting.GET("", controllers.UserSetting)
					// upload avatar from file
					setting.PUT("avatar", middleware.RequiredScopes(types.ScopeUserInfoWrite), controllers.UploadAvatar)
					// change user settings
					setting.PATCH("",
						middleware.RequiredScopes(types.ScopeUserInfoWrite),
						controllers.FromJSON[usersvc.PatchUserSetting](usersvc.PatchUserSettingParamsCtx{}),
						controllers.UpdateOption,
					)
					// get two-factor authentication setup info
					setting.GET("2fa", controllers.UserInit2FA)
					// Fylvo: recent sign-ins of the own account
					setting.GET("activity",
						middleware.RequiredScopes(types.ScopeUserSecurityInfoRead),
						controllers.FromQuery[fylvosvc.OwnActivityService](fylvosvc.OwnActivityParamCtx{}),
						controllers.FylvoOwnActivity,
					)
				}
			}

			// WebDAV and devices
			devices := auth.Group("devices")
			devices.Use(middleware.RequiredScopes(types.ScopeDavAccountRead))
			{
				dav := devices.Group("dav")
				{
					// List WebDAV accounts
					dav.GET("",
						controllers.FromQuery[setting.ListDavAccountsService](setting.ListDavAccountParamCtx{}),
						controllers.ListDavAccounts,
					)
					// Create WebDAV account
					dav.PUT("",
						middleware.RequiredScopes(types.ScopeDavAccountWrite),
						controllers.FromJSON[setting.CreateDavAccountService](setting.CreateDavAccountParamCtx{}),
						controllers.CreateDAVAccounts,
					)
					// Create WebDAV account
					dav.PATCH(":id",
						middleware.RequiredScopes(types.ScopeDavAccountWrite),
						middleware.HashID(hashid.DavAccountID),
						controllers.FromJSON[setting.CreateDavAccountService](setting.CreateDavAccountParamCtx{}),
						controllers.UpdateDAVAccounts,
					)
					// Delete WebDAV account
					dav.DELETE(":id",
						middleware.RequiredScopes(types.ScopeDavAccountWrite),
						middleware.HashID(hashid.DavAccountID),
						controllers.DeleteDAVAccounts,
					)
				}
				//// get account info
				//devices.GET("dav", controllers.GetWebDAVAccounts)
				//// delete directory mount
				//devices.DELETE("mount/:id",
				//	middleware.HashID(hashid.FolderID),
				//	controllers.DeleteWebDAVMounts,
				//)
				//// create directory mount
				//devices.POST("mount", controllers.CreateWebDAVMounts)
				//// update account readability
				//devices.PATCH("accounts", controllers.UpdateWebDAVAccountsReadonly)
			}

		}

	}

	// initialize WebDAV related routes
	initWebDAV(r.Group("dav"))
	return r
}

// initWebDAV initializes WebDAV related routes
func initWebDAV(group *gin.RouterGroup) {
	{
		group.Use(middleware.CacheControl(), middleware.WebDAVAuth())
		group.Any("/*path", webdav.ServeHTTP)
		group.Any("", webdav.ServeHTTP)
		group.Handle("PROPFIND", "/*path", webdav.ServeHTTP)
		group.Handle("PROPFIND", "", webdav.ServeHTTP)
		group.Handle("MKCOL", "/*path", webdav.ServeHTTP)
		group.Handle("LOCK", "/*path", webdav.ServeHTTP)
		group.Handle("UNLOCK", "/*path", webdav.ServeHTTP)
		group.Handle("PROPPATCH", "/*path", webdav.ServeHTTP)
		group.Handle("COPY", "/*path", webdav.ServeHTTP)
		group.Handle("MOVE", "/*path", webdav.ServeHTTP)

	}
}
