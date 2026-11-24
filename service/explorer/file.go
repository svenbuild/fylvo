package explorer

import (
	"context"
	"encoding/gob"
	"fmt"
	"net/http"
	"time"

	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/auth"
	"github.com/cloudreve/Cloudreve/v4/pkg/cluster/routes"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs/dbfs"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/manager"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/manager/entitysource"
	"github.com/cloudreve/Cloudreve/v4/pkg/hashid"
	"github.com/cloudreve/Cloudreve/v4/pkg/request"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/cloudreve/Cloudreve/v4/pkg/setting"
	"github.com/cloudreve/Cloudreve/v4/pkg/util"
	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
	"github.com/samber/lo"
)

// SingleFileService 对单文件进行操作的五福，path为文件完整路径
type SingleFileService struct {
	Path string `uri:"path" json:"path" binding:"required,min=1,max=65535"`
}

// FileIDService 通过文件ID对文件进行操作的服务
type FileIDService struct {
}

// FileAnonymousGetService 匿名（外链）获取文件服务
type FileAnonymousGetService struct {
	ID   uint   `uri:"id" binding:"required,min=1"`
	Name string `uri:"name" binding:"required"`
}

func init() {
	gob.Register(ArchiveDownloadSession{})
}

// ArchiveService 文件流式打包下載服务
type (
	ArchiveService struct {
		ID string `uri:"sessionID" binding:"required"`
	}
	ArchiveParamCtx struct{}
)

// DownloadArchived 通过预签名 URL 打包下载
func (service *ArchiveService) DownloadArchived(c *gin.Context) error {
	dep := dependency.FromContext(c)
	archiveSessionRaw, found := dep.KV().Get(ArchiveDownloadSessionPrefix + service.ID)
	if !found {
		return serializer.NewError(serializer.CodeNotFound, "Archive session not exist", nil)
	}

	// Switch to user context
	archiveSession := archiveSessionRaw.(ArchiveDownloadSession)
	requester, err := dep.UserClient().GetLoginUserByID(c, archiveSession.RequesterID)
	if err != nil {
		return serializer.NewError(serializer.CodeNotFound, "Requester not found", err)
	}
	// Fylvo: the session ends with a new password, like a sign-in.
	if !inventory.SessionStillValid(requester, archiveSession.PasswordFingerprint) {
		return serializer.NewError(serializer.CodeNotFound, "Archive session not exist", nil)
	}

	util.WithValue(c, inventory.UserCtx{}, requester)

	fm := manager.NewFileManager(dep, requester)
	defer fm.Recycle()

	// 开始打包
	c.Header("Content-Disposition", "attachment;")
	c.Header("Content-Type", "application/zip")

	if _, err := fm.CreateArchive(c, archiveSession.Uris, c.Writer, fs.WithArchiveDownload()); err != nil {
		return serializer.NewError(serializer.CodeIOFailed, "Failed to create archive", err)
	}

	return nil
}

type (
	GetDirectLinkParamCtx struct{}
	GetDirectLinkService  struct {
		Uris []string `json:"uris" binding:"required,min=1"`
	}
)

func (s *GetDirectLinkService) GetUris() []string {
	return s.Uris
}

// Sources 批量获取对象的外链
func (s *GetDirectLinkService) Get(c *gin.Context) ([]DirectLinkResponse, error) {
	dep := dependency.FromContext(c)
	u := inventory.UserFromContext(c)

	if u.Edges.Group.Settings.SourceBatchSize == 0 {
		return nil, serializer.NewError(serializer.CodeGroupNotAllowed, "", nil)
	}

	if len(s.Uris) > u.Edges.Group.Settings.SourceBatchSize {
		return nil, serializer.NewError(serializer.CodeBatchSourceSize, "", nil)
	}

	m := manager.NewFileManager(dep, u)
	defer m.Recycle()

	uris, err := fs.NewUriFromStrings(s.Uris...)
	if err != nil {
		return nil, serializer.NewError(serializer.CodeParamErr, "unknown uri", err)
	}

	res, err := m.GetDirectLink(c, uris...)
	return BuildDirectLinkResponse(res), err
}

func DeleteDirectLink(c *gin.Context) error {
	dep := dependency.FromContext(c)
	user := inventory.UserFromContext(c)
	m := manager.NewFileManager(dep, user)
	defer m.Recycle()

	linkId := hashid.FromContext(c)
	linkClient := dep.DirectLinkClient()
	ctx := context.WithValue(c, inventory.LoadDirectLinkFile{}, true)
	link, err := linkClient.GetByID(ctx, linkId)
	if err != nil || link.Edges.File == nil {
		return serializer.NewError(serializer.CodeNotFound, "Direct link not found", err)
	}

	if link.Edges.File.OwnerID != user.ID {
		return serializer.NewError(serializer.CodeNotFound, "Direct link not found", err)
	}

	if err := linkClient.Delete(c, link.ID); err != nil {
		return serializer.NewError(serializer.CodeDBError, "Failed to delete direct link", err)
	}

	return nil
}

type (
	// ListFileParameterCtx define key fore ListFileService
	ListFileParameterCtx struct{}

	// ListFileService stores parameters for list file by URI
	ListFileService struct {
		Uri            string `uri:"uri" form:"uri" json:"uri" binding:"required"`
		Page           int    `uri:"page" form:"page" json:"page" binding:"min=0"`
		PageSize       int    `uri:"page_size" form:"page_size" json:"page_size"`
		OrderBy        string `uri:"order_by" form:"order_by" json:"order_by"`
		OrderDirection string `uri:"order_direction" form:"order_direction" json:"order_direction"`
		NextPageToken  string `uri:"next_page_token" form:"next_page_token" json:"next_page_token"`
		// Recursive asks for everything below the folder, not only what is in it. For
		// folders of connected storages (Fylvo); the search of Fylvo's own files goes
		// by the parameters in the address alone.
		Recursive bool `uri:"recursive" form:"recursive" json:"recursive"`
	}
)

// List all files for given path
func (service *ListFileService) List(c *gin.Context) (*ListResponse, error) {
	// Fylvo: a folder of a connected storage is listed by its own engine.
	if uri, ok := connectedURI(service.Uri); ok {
		return connectedList(c, uri, service)
	}

	dep := dependency.FromContext(c)
	user := inventory.UserFromContext(c)
	m := manager.NewFileManager(dep, user)
	defer m.Recycle()

	uri, err := fs.NewUriFromString(service.Uri)
	if err != nil {
		return nil, serializer.NewError(serializer.CodeParamErr, "unknown uri", err)
	}

	pageSize := service.PageSize
	streamed := false
	hasher := dep.HashIDEncoder()
	parent, res, err := m.List(c, uri, &manager.ListArgs{
		Page:           service.Page,
		PageSize:       pageSize,
		Order:          service.OrderBy,
		OrderDirection: service.OrderDirection,
		PageToken:      service.NextPageToken,
		StreamResponseCallback: func(parent fs.File, files []fs.File) {
			if !streamed {
				WriteEventSourceHeader(c)
				streamed = true
			}

			WriteEventSource(c, "file", lo.Map(files, func(file fs.File, index int) *FileResponse {
				return BuildFileResponse(c, user, file, hasher, nil)
			}))
		},
	})
	if err != nil {
		return nil, err
	}

	listResponse := BuildListResponse(c, user, parent, res, hasher)
	if streamed {
		WriteEventSource(c, "list", listResponse)
		return nil, ErrSSETakeOver
	}

	return listResponse, nil
}

type (
	CreateFileParameterCtx struct{}
	CreateFileService      struct {
		Uri           string            `json:"uri" binding:"required"`
		Type          string            `json:"type" binding:"required,eq=file|eq=folder"`
		Metadata      map[string]string `json:"metadata"`
		ErrOnConflict bool              `json:"err_on_conflict"`
	}
)

func (service *CreateFileService) Create(c *gin.Context) (*FileResponse, error) {
	// Fylvo: a connected storage creates at its provider.
	if uri, ok := connectedURI(service.Uri); ok {
		return connectedCreate(c, uri, service)
	}
	dep := dependency.FromContext(c)
	user := inventory.UserFromContext(c)
	m := manager.NewFileManager(dep, user)
	defer m.Recycle()

	uri, err := fs.NewUriFromString(service.Uri)
	if err != nil {
		return nil, serializer.NewError(serializer.CodeParamErr, "unknown uri", err)
	}

	fileType := types.FileTypeFromString(service.Type)
	opts := []fs.Option{
		fs.WithMetadata(service.Metadata),
	}
	if service.ErrOnConflict {
		opts = append(opts, dbfs.WithErrorOnConflict())
	}
	file, err := m.Create(c, uri, fileType, opts...)
	if err != nil {
		return nil, err
	}

	return BuildFileResponse(c, user, file, dep.HashIDEncoder(), nil), nil
}

type (
	RenameFileParameterCtx struct{}
	RenameFileService      struct {
		Uri     string `json:"uri" binding:"required"`
		NewName string `json:"new_name" binding:"required,min=1,max=255"`
		// Ids is for addresses of a connected storage (Fylvo): the id of the row as last listed.
		Ids connectedIDs `json:"ids"`
	}
)

func (service *RenameFileService) Rename(c *gin.Context) (*FileResponse, error) {
	if uri, ok := connectedURI(service.Uri); ok {
		return connectedRename(c, uri, service)
	}
	dep := dependency.FromContext(c)
	user := inventory.UserFromContext(c)
	m := manager.NewFileManager(dep, user)
	defer m.Recycle()

	uri, err := fs.NewUriFromString(service.Uri)
	if err != nil {
		return nil, serializer.NewError(serializer.CodeParamErr, "unknown uri", err)
	}

	file, err := m.Rename(c, uri, service.NewName)
	if err != nil {
		return nil, err
	}

	return BuildFileResponse(c, user, file, dep.HashIDEncoder(), nil), nil
}

type (
	MoveFileParameterCtx struct{}
	MoveFileService      struct {
		Uris []string `json:"uris" binding:"required,min=1"`
		Dst  string   `json:"dst" binding:"required"`
		Copy bool     `json:"copy"`
		// Ids is for addresses of a connected storage (Fylvo): the ids of the rows as last listed.
		Ids connectedIDs `json:"ids"`
	}
)

func (s *MoveFileService) GetUris() []string {
	return s.Uris
}

func (s *MoveFileService) Move(c *gin.Context) error {
	// Fylvo: inside one connected storage the provider moves. Between a connected
	// storage and anything else nothing is moved or copied.
	connectedUris, err := connectedURIs(s.Uris)
	if err != nil {
		return err
	}
	connectedDst, dstIsConnected := connectedURI(s.Dst)
	if len(connectedUris) > 0 || dstIsConnected {
		return connectedMove(c, connectedUris, connectedDst, s)
	}

	dep := dependency.FromContext(c)
	user := inventory.UserFromContext(c)
	m := manager.NewFileManager(dep, user)
	defer m.Recycle()

	uris, err := fs.NewUriFromStrings(s.Uris...)
	if err != nil {
		return serializer.NewError(serializer.CodeParamErr, "unknown uri", err)
	}

	dst, err := fs.NewUriFromString(s.Dst)
	if err != nil {
		return serializer.NewError(serializer.CodeParamErr, "unknown destination uri", err)
	}

	return m.MoveOrCopy(c, uris, dst, s.Copy)
}

type (
	FileUpdateParameterCtx struct{}
	FileUpdateService      struct {
		Uri      string `form:"uri" binding:"required"`
		Previous string `form:"previous"`
		// Fylvo: fail with 40904 instead of creating the file when there is none to replace.
		PreviousRequired bool `form:"previous_required"`
	}
)

func (service *FileUpdateService) PutContent(c *gin.Context, ls fs.LockSession) (*FileResponse, error) {
	dep := dependency.FromContext(c)
	settings := dep.SettingProvider()
	// Fylvo: a connected storage saves through an upload session at its provider.
	if uri, ok := connectedURI(service.Uri); ok {
		return connectedPutContent(c, uri, service)
	}
	if meantForConnected(service.Uri) {
		// Fylvo: meant for a connected storage, but no address of one (it is refused
		// below). Whoever throws away the rest of its body does not wait for a client
		// that stands still, as for every other save into a connected storage.
		defer func() { _ = http.NewResponseController(c.Writer).SetReadDeadline(time.Now().Add(connectedIdle)) }()
	}
	// 取得文件大小
	rc, fileSize, err := request.SniffContentLength(c.Request)
	if err != nil {
		return nil, serializer.NewError(serializer.CodeParamErr, "invalid content length", err)
	}

	if fileSize > settings.MaxOnlineEditSize(c) {
		return nil, fs.ErrFileSizeTooBig
	}

	uri, err := fs.NewUriFromString(service.Uri)
	if err != nil {
		return nil, serializer.NewError(serializer.CodeParamErr, "unknown uri", err)
	}

	fileData := &fs.UploadRequest{
		Props: &fs.UploadProps{
			Uri:              uri,
			PreviousVersion:  service.Previous,
			PreviousRequired: service.PreviousRequired,
			Size:             fileSize,
		},
		File: rc,
		Mode: fs.ModeOverwrite,
	}

	user := inventory.UserFromContext(c)
	m := manager.NewFileManager(dep, user)
	defer m.Recycle()

	// Update file
	var ctx context.Context = c
	if ls != nil {
		ctx = fs.LockSessionToContext(c, ls)
	}
	res, err := m.Update(ctx, fileData)
	if err != nil {
		return nil, fmt.Errorf("failed to update file: %w", err)
	}

	return BuildFileResponse(c, user, res, dep.HashIDEncoder(), nil), nil
}

type (
	FileURLParameterCtx struct{}
	FileURLService      struct {
		Uris              []string `json:"uris" binding:"required"`
		Download          bool     `json:"download"`
		Redirect          bool     `json:"redirect"` // Only works if Uris count is 1.
		Entity            string   `json:"entity"`   // Only works if Uris count is 1.
		UsePrimarySiteURL bool     `json:"use_primary_site_url"`
		SkipError         bool     `json:"skip_error"`
		Archive           bool     `json:"archive"`
		NoCache           bool     `json:"no_cache"`
		// Export is the format a document that exists only at the provider of a
		// connected storage is read in (Fylvo): "docx", "xlsx", "pptx", "svg" or "pdf",
		// as the kind of document allows. Empty reads it in its usual format.
		Export string `json:"export"`
	}
	FileURLResponse struct {
		Urls    []manager.EntityUrl `json:"urls"`
		Expires *time.Time          `json:"expires"`
	}
	ArchiveDownloadSession struct {
		Uris        []*fs.URI `json:"uris"`
		RequesterID int       `json:"requester_id"`
		// Fylvo: inventory.PasswordFingerprint of the requester when the session was made.
		PasswordFingerprint string `json:"password_fingerprint"`
	}
)

const (
	ArchiveDownloadSessionPrefix = "archive_"
)

func (s *FileURLService) GetUris() []string {
	return s.Uris
}

// GetArchiveDownloadSession generates temporary download session for archive download.
func (s *FileURLService) GetArchiveDownloadSession(c *gin.Context) (*FileURLResponse, error) {
	dep := dependency.FromContext(c)
	settings := dep.SettingProvider()
	user := inventory.UserFromContext(c)

	uris, err := fs.NewUriFromStrings(s.Uris...)
	if err != nil {
		return nil, serializer.NewError(serializer.CodeParamErr, "unknown uri", err)
	}

	if !user.Edges.Group.Permissions.Enabled(int(types.GroupPermissionArchiveDownload)) {
		return nil, serializer.NewError(serializer.CodeGroupNotAllowed, "", nil)
	}

	// Create archive download session
	archiveSession := &ArchiveDownloadSession{
		Uris:        uris,
		RequesterID: user.ID,

		PasswordFingerprint: inventory.PasswordFingerprint(user),
	}
	sessionId := uuid.Must(uuid.NewV4()).String()
	ttl := settings.ArchiveDownloadSessionTTL(c)
	expire := time.Now().Add(time.Duration(ttl) * time.Second)
	if err := dep.KV().Set(ArchiveDownloadSessionPrefix+sessionId, *archiveSession, ttl); err != nil {
		return nil, serializer.NewError(serializer.CodeInternalSetting, "failed to create archive download session", err)
	}

	base := settings.SiteURL(c)
	downloadUrl := routes.MasterArchiveDownloadUrl(base, sessionId)
	finalUrl, err := auth.SignURI(c, dep.GeneralAuth(), downloadUrl.String(), &expire)
	if err != nil {
		return nil, serializer.NewError(serializer.CodeInternalSetting, "failed to sign archive download url", err)
	}

	return &FileURLResponse{
		Urls:    []manager.EntityUrl{{Url: finalUrl.String()}},
		Expires: &expire,
	}, nil
}

func (s *FileURLService) Get(c *gin.Context) (*FileURLResponse, error) {
	// Fylvo: files of a connected storage get their own download addresses. This comes
	// first: an archive session must never be made for addresses nobody checked.
	if connectedUris, err := connectedURIs(s.Uris); err != nil {
		return nil, err
	} else if connectedUris != nil {
		return connectedURLs(c, connectedUris, s)
	}

	if s.Archive {
		return s.GetArchiveDownloadSession(c)
	}

	dep := dependency.FromContext(c)
	settings := dep.SettingProvider()
	user := inventory.UserFromContext(c)
	m := manager.NewFileManager(dep, user)
	defer m.Recycle()

	uris, err := fs.NewUriFromStrings(s.Uris...)
	if err != nil {
		return nil, serializer.NewError(serializer.CodeParamErr, "unknown uri", err)
	}

	// Request entity URL
	expire := time.Now().Add(settings.EntityUrlValidDuration(c))
	urlReq := lo.Map(uris, func(uri *fs.URI, _ int) manager.GetEntityUrlArgs {
		return manager.GetEntityUrlArgs{
			URI:               uri,
			PreferredEntityID: s.Entity,
		}
	})

	var ctx context.Context = c
	if s.UsePrimarySiteURL {
		ctx = setting.UseFirstSiteUrl(ctx)
	}

	res, earliestExpire, err := m.GetEntityUrls(ctx, urlReq,
		fs.WithDownloadSpeed(int64(user.Edges.Group.SpeedLimit)),
		fs.WithIsDownload(s.Download),
		fs.WithNoCache(s.NoCache),
		fs.WithUrlExpire(&expire),
		fs.WithSkipError(s.SkipError),
	)
	if err != nil && !s.SkipError {
		return nil, fmt.Errorf("failed to get entity url: %w", err)
	}

	//if !s.NoCache && earliestExpire != nil {
	//	// Set cache header
	//	cacheTTL := int(earliestExpire.Sub(time.Now()).Seconds() - float64(settings.EntityUrlCacheMargin(c)))
	//	if cacheTTL > 0 {
	//		c.Header("Cache-Control", fmt.Sprintf("private, max-age=%d", cacheTTL))
	//	}
	//}

	if s.Redirect && len(uris) == 1 {
		c.Redirect(http.StatusFound, res[0].Url)
		return nil, nil
	}

	return &FileURLResponse{
		Urls:    res,
		Expires: earliestExpire,
	}, nil
}

type (
	FileThumbParameterCtx struct{}
	FileThumbService      struct {
		Uri string `form:"uri" binding:"required"`
		// Entity is the version of the file the client shows: the "primary_entity" of
		// its row. For files of connected storages (Fylvo), where the content can change
		// at the provider between a listing and this call.
		Entity string `form:"entity"`
	}
	FileThumbResponse struct {
		Url     string     `json:"url"`
		Expires *time.Time `json:"expires"`
	}
)

// Get redirect to thumb file.
func (s *FileThumbService) Get(c *gin.Context) (*FileThumbResponse, error) {
	// Fylvo: the preview of a file in a connected storage is the provider's own.
	if connectedUri, ok := connectedURI(s.Uri); ok {
		return connectedThumb(c, connectedUri, s.Entity)
	}

	dep := dependency.FromContext(c)
	user := inventory.UserFromContext(c)
	m := manager.NewFileManager(dep, user)
	defer m.Recycle()

	uri, err := fs.NewUriFromString(s.Uri)
	if err != nil {
		return nil, serializer.NewError(serializer.CodeParamErr, "unknown uri", err)
	}

	// Get thumbnail
	thumb, err := m.Thumbnail(c, uri)
	if err != nil {
		return nil, fmt.Errorf("failed to get thumbnail: %w", err)
	}

	expire := time.Now().Add(dep.SettingProvider().EntityUrlValidDuration(c))
	// Fylvo: a stored thumbnail served by this server gets the same address for a whole day,
	// so browsers and apps can keep the image (see StableThumbExpire).
	if thumb.Entity().Type() == types.EntityTypeThumbnail && thumb.ShouldInternalProxy() {
		expire = StableThumbExpire(time.Now())
	}
	thumbUrl, err := thumb.Url(c, entitysource.WithExpire(&expire))
	if err != nil {
		return nil, fmt.Errorf("failed to get thumbnail url: %w", err)
	}

	return &FileThumbResponse{
		Url:     thumbUrl.Url,
		Expires: thumbUrl.ExpireAt,
	}, nil
}

type (
	DeleteFileParameterCtx struct{}
	DeleteFileService      struct {
		Uris           []string `json:"uris" binding:"required,min=1"`
		UnlinkOnly     bool     `json:"unlink"`
		SkipSoftDelete bool     `json:"skip_soft_delete"`
		// Ids is for addresses of a connected storage (Fylvo): the ids of the rows as last listed.
		Ids connectedIDs `json:"ids"`
	}
)

func (s *DeleteFileService) GetUris() []string {
	return s.Uris
}

func (s *DeleteFileService) Delete(c *gin.Context) error {
	// Fylvo: entries of a connected storage go into the trash of the provider.
	connectedUris, err := connectedURIs(s.Uris)
	if err != nil {
		return err
	}
	if len(connectedUris) > 0 {
		return connectedDelete(c, connectedUris, s)
	}

	dep := dependency.FromContext(c)
	user := inventory.UserFromContext(c)
	m := manager.NewFileManager(dep, user)
	defer m.Recycle()

	uris, err := fs.NewUriFromStrings(s.Uris...)
	if err != nil {
		return serializer.NewError(serializer.CodeParamErr, "unknown uri", err)
	}

	if s.UnlinkOnly && !user.Edges.Group.Permissions.Enabled(int(types.GroupPermissionAdvanceDelete)) {
		return serializer.NewError(serializer.CodeNoPermissionErr, "advance delete permission is required", nil)
	}

	// Delete file
	if err = m.Delete(c, uris, fs.WithUnlinkOnly(s.UnlinkOnly), fs.WithSkipSoftDelete(s.SkipSoftDelete)); err != nil {
		return fmt.Errorf("failed to delete file: %w", err)
	}

	return nil
}

func (s *DeleteFileService) Restore(c *gin.Context) error {
	dep := dependency.FromContext(c)
	user := inventory.UserFromContext(c)
	m := manager.NewFileManager(dep, user)
	defer m.Recycle()

	uris, err := fs.NewUriFromStrings(s.Uris...)
	if err != nil {
		return serializer.NewError(serializer.CodeParamErr, "unknown uri", err)
	}

	// Delete file
	if err = m.Restore(c, uris...); err != nil {
		return fmt.Errorf("failed to restore file: %w", err)

	}

	return nil
}

// EmptyTrash hard-deletes every top-level item in the current user's trash bin.
func EmptyTrash(c *gin.Context) error {
	dep := dependency.FromContext(c)
	user := inventory.UserFromContext(c)
	m := manager.NewFileManager(dep, user)
	defer m.Recycle()

	if err := m.EmptyTrash(c); err != nil {
		return fmt.Errorf("failed to empty trash: %w", err)
	}

	return nil
}

type (
	UnlockFileParameterCtx struct{}
	UnlockFileService      struct {
		Tokens []string `json:"tokens" binding:"required,max=16384"`
	}
)

func (s *UnlockFileService) Unlock(c *gin.Context) error {
	dep := dependency.FromContext(c)
	user := inventory.UserFromContext(c)
	m := manager.NewFileManager(dep, user)
	defer m.Recycle()

	// Fylvo: only locks among the own files are released here. A lock in a folder somebody
	// else shared is released through the file it lies on (office editor, WebDAV).
	myUri, err := fs.NewUriFromString(fs.NewMyUri(""))
	if err != nil {
		return serializer.NewError(serializer.CodeParamErr, "failed to unlock file", err)
	}

	root, err := m.Get(c, myUri)
	if err != nil {
		return serializer.NewError(serializer.CodeParamErr, "failed to unlock file", err)
	}

	if err := m.UnlockAt(c, root, root.Uri(false), s.Tokens...); err != nil {
		return serializer.NewError(serializer.CodeParamErr, "failed to unlock file", err)
	}

	return nil
}

type (
	GetFileInfoParameterCtx struct{}
	GetFileInfoService      struct {
		Uri           string `form:"uri"`
		ID            string `form:"id"`
		ExtendedInfo  bool   `form:"extended"`
		FolderSummary bool   `form:"folder_summary"`
	}
)

func (s *GetFileInfoService) Get(c *gin.Context) (*FileResponse, error) {
	dep := dependency.FromContext(c)
	user := inventory.UserFromContext(c)
	m := manager.NewFileManager(dep, user)
	defer m.Recycle()

	if s.ID != "" && s.Uri == "" {
		fileId, err := dep.HashIDEncoder().Decode(s.ID, hashid.FileID)
		if err != nil {
			return nil, serializer.NewError(serializer.CodeParamErr, "unknown file id", err)
		}

		file, err := m.TraverseFile(c, fileId)
		if err != nil {
			return nil, fmt.Errorf("failed to traverse file: %w", err)
		}

		s.Uri = file.Uri(true).String()
	}

	if s.Uri == "" {
		return nil, serializer.NewError(serializer.CodeParamErr, "uri is required", nil)
	}

	// Fylvo: an entry of a connected storage. Looking one up by id does not exist: the
	// ids of such entries are no ids of the database.
	if uri, ok := connectedURI(s.Uri); ok {
		return connectedInfo(c, uri, s.ExtendedInfo)
	}

	uri, err := fs.NewUriFromString(s.Uri)
	if err != nil {
		return nil, serializer.NewError(serializer.CodeParamErr, "unknown uri", err)
	}

	// Fylvo: details of a file need the right to see them. A visitor of a link that hides
	// its folder does not have it.
	opts := []fs.Option{dbfs.WithFilePublicMetadata(), dbfs.WithNotRoot(), dbfs.WithRequiredCapabilities(dbfs.NavigatorCapabilityInfo)}
	if s.ExtendedInfo {
		opts = append(opts, dbfs.WithExtendedInfo(), dbfs.WithEntityUser(), dbfs.WithFileShareIfOwned())
	}
	if s.FolderSummary {
		opts = append(opts, dbfs.WithLoadFolderSummary())
	}

	file, err := m.Get(c, uri, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to get file: %w", err)
	}

	if file == nil {
		return nil, serializer.NewError(serializer.CodeNotFound, "file not found", nil)
	}

	return BuildFileResponse(c, user, file, dep.HashIDEncoder(), nil), nil
}

func RedirectDirectLink(c *gin.Context, name string, download bool) error {
	dep := dependency.FromContext(c)
	settings := dep.SettingProvider()

	sourceLinkID := hashid.FromContext(c)
	ctx := context.WithValue(c, inventory.LoadDirectLinkFile{}, true)
	ctx = context.WithValue(ctx, inventory.LoadFileEntity{}, true)
	ctx = context.WithValue(ctx, inventory.LoadFileUser{}, true)
	ctx = context.WithValue(ctx, inventory.LoadUserGroup{}, true)
	dl, err := dep.DirectLinkClient().GetByNameID(ctx, sourceLinkID, name)
	if err != nil {
		return serializer.NewError(serializer.CodeNotFound, "direct link not found", err)
	}

	m := manager.NewFileManager(dep, dl.Edges.File.Edges.Owner)
	defer m.Recycle()

	// Request entity URL
	expire := time.Now().Add(settings.EntityUrlValidDuration(c))
	res, earliestExpire, err := m.GetUrlForRedirectedDirectLink(c, dl,
		fs.WithUrlExpire(&expire),
		fs.WithIsDownload(download),
	)
	if err != nil {
		return err
	}

	c.Redirect(http.StatusFound, res)
	c.Header("Cache-Control", fmt.Sprintf("public, max-age=%d", int(earliestExpire.Sub(time.Now()).Seconds())))
	return nil
}

type (
	PatchViewParameterCtx struct{}
	PatchViewService      struct {
		Uri  string              `json:"uri" binding:"required"`
		View *types.ExplorerView `json:"view"`
	}
)

func (s *PatchViewService) Patch(c *gin.Context) error {
	// Fylvo: the view of a connected storage is kept with the connection.
	if uri, ok := connectedURI(s.Uri); ok {
		return connectedPatchView(c, uri, s.View)
	}

	dep := dependency.FromContext(c)
	user := inventory.UserFromContext(c)
	m := manager.NewFileManager(dep, user)
	defer m.Recycle()

	uri, err := fs.NewUriFromString(s.Uri)
	if err != nil {
		return serializer.NewError(serializer.CodeParamErr, "unknown uri", err)
	}

	if err := m.PatchView(c, uri, s.View); err != nil {
		return err
	}

	return nil
}

type (
	ArchiveListFilesParamCtx struct{}
	ArchiveListFilesService  struct {
		Uri          string `form:"uri" json:"uri" binding:"required"`
		Entity       string `form:"entity" json:"entity"`
		TextEncoding string `form:"text_encoding" json:"text_encoding"`
		// Fylvo: password of an archive with encrypted file names. Only sent in the body of a
		// POST (a query string lands in access logs), used for this call, never stored.
		Password string `json:"password"`
	}
)

func (s *ArchiveListFilesService) List(c *gin.Context) (*ArchiveListFilesResponse, error) {
	dep := dependency.FromContext(c)
	user := inventory.UserFromContext(c)
	m := manager.NewFileManager(dep, user)
	defer m.Recycle()
	if !user.Edges.Group.Permissions.Enabled(int(types.GroupPermissionArchiveTask)) {
		return nil, serializer.NewError(serializer.CodeGroupNotAllowed, "Group not allowed to extract archive files", nil)
	}

	uri, err := fs.NewUriFromString(s.Uri)
	if err != nil {
		return nil, serializer.NewError(serializer.CodeParamErr, "unknown uri", err)
	}

	files, err := m.ListArchiveFiles(c, uri, s.Entity, s.TextEncoding, s.Password)
	if err != nil {
		return nil, fmt.Errorf("failed to list archive files: %w", err)
	}

	return BuildArchiveListFilesResponse(files), nil
}

type (
	FulltextSearchParamCtx struct{}
	FulltextSearchService  struct {
		Query  string `form:"query" binding:"required"`
		Offset int    `form:"offset"`
	}
)

func (s *FulltextSearchService) Search(c *gin.Context) (*FullTextSearchResults, error) {
	dep := dependency.FromContext(c)
	user := inventory.UserFromContext(c)
	m := manager.NewFileManager(dep, user)
	defer m.Recycle()

	results, err := m.SearchFullText(c, s.Query, s.Offset)
	if err != nil {
		return nil, serializer.NewError(serializer.CodeInternalSetting, "failed to search full text", err)
	}

	return BuildFullTextSearchResults(c, user, dep.HashIDEncoder(), results), nil
}
