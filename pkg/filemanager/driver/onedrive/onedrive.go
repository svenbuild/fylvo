package onedrive

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/boolset"
	"github.com/cloudreve/Cloudreve/v4/pkg/cluster/routes"
	"github.com/cloudreve/Cloudreve/v4/pkg/conf"
	"github.com/cloudreve/Cloudreve/v4/pkg/credmanager"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/driver"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/cloudreve/Cloudreve/v4/pkg/request"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/cloudreve/Cloudreve/v4/pkg/setting"
)

// Driver is the OneDrive adapter
type (
	Driver struct {
		policy    *ent.StoragePolicy
		client    Client
		settings  setting.Provider
		config    conf.ConfigProvider
		l         logging.Logger
		chunkSize int64
	}
	ListPathRealRootCtx struct{}
)

var (
	features = &boolset.BooleanSet{}
)

const (
	streamSaverParam = "stream_saver"
)

func init() {
	boolset.Sets(map[driver.HandlerCapability]bool{
		driver.HandlerCapabilityUploadSentinelRequired: true,
	}, features)
}

// NewDriver creates a new Driver instance from the storage policy
func New(ctx context.Context, policy *ent.StoragePolicy, settings setting.Provider,
	config conf.ConfigProvider, l logging.Logger, cred credmanager.CredManager) (*Driver, error) {
	chunkSize := policy.Settings.ChunkSize
	if policy.Settings.ChunkSize == 0 {
		chunkSize = 50 << 20 // 50MB
	}

	c := NewClient(policy, request.NewClient(config, request.WithLogger(l)), cred, l, settings, chunkSize)

	return &Driver{
		policy:    policy,
		client:    c,
		settings:  settings,
		l:         l,
		config:    config,
		chunkSize: chunkSize,
	}, nil
}

// List lists items
func (handler *Driver) List(ctx context.Context, base string, onProgress driver.ListProgressFunc, recursive bool) ([]fs.PhysicalObject, error) {
	base = strings.TrimPrefix(base, "/")
	// list child items
	objects, _ := handler.client.ListChildren(ctx, base)

	// get the real root directory to start listing from
	rootPath := base
	if realBase, ok := ctx.Value(ListPathRealRootCtx{}).(string); ok {
		rootPath = realBase
	} else {
		ctx = context.WithValue(ctx, ListPathRealRootCtx{}, base)
	}

	// collect the results
	res := make([]fs.PhysicalObject, 0, len(objects))
	for _, object := range objects {
		source := path.Join(base, object.Name)
		rel, err := filepath.Rel(rootPath, source)
		if err != nil {
			continue
		}
		res = append(res, fs.PhysicalObject{
			Name:         object.Name,
			RelativePath: filepath.ToSlash(rel),
			Source:       source,
			Size:         object.Size,
			IsDir:        object.Folder != nil,
			LastModify:   time.Now(),
		})
	}

	onProgress(len(objects))

	// list subdirectories recursively
	if recursive {
		for _, object := range objects {
			if object.Folder != nil {
				sub, _ := handler.List(ctx, path.Join(base, object.Name), onProgress, recursive)
				res = append(res, sub...)
			}
		}
	}

	return res, nil
}

func (handler *Driver) Open(ctx context.Context, path string) (*os.File, error) {
	return nil, errors.New("not implemented")
}

// Put saves the file stream to the given location
func (handler *Driver) Put(ctx context.Context, file *fs.UploadRequest) error {
	defer file.Close()

	return handler.client.Upload(ctx, file)
}

// Delete removes one or more files,
// returns the files that were not deleted and the last error encountered
func (handler *Driver) Delete(ctx context.Context, files ...string) ([]string, error) {
	return handler.client.BatchDelete(ctx, files)
}

// Thumb gets the file thumbnail
func (handler *Driver) Thumb(ctx context.Context, expire *time.Time, ext string, e fs.Entity) (string, error) {
	res, err := handler.client.GetThumbURL(ctx, e.Source())
	if err != nil {
		var apiErr *RespError
		if errors.As(err, &apiErr); err == ErrThumbSizeNotFound || (apiErr != nil && apiErr.APIError.Code == notFoundError) {
			// OneDrive cannot generate thumbnail for this file
			return "", fmt.Errorf("thumb not supported in OneDrive: %w", err)
		}
	}

	return res, nil
}

// Source gets the external link URL
func (handler *Driver) Source(ctx context.Context, e fs.Entity, args *driver.GetSourceArgs) (string, error) {
	// cache miss, fetch again
	res, err := handler.client.Meta(ctx, "", e.Source())
	if err != nil {
		return "", err
	}

	return res.DownloadURL, nil
}

// Token gets the upload session URL
func (handler *Driver) Token(ctx context.Context, uploadSession *fs.UploadSession, file *fs.UploadRequest) (*fs.UploadCredential, error) {
	// generate the callback URL
	siteURL := handler.settings.SiteURL(setting.UseFirstSiteUrl(ctx))
	uploadSession.Callback = routes.MasterSlaveCallbackUrl(siteURL, types.PolicyTypeOd, uploadSession.Props.UploadSessionID, uploadSession.CallbackSecret).String()

	uploadURL, err := handler.client.CreateUploadSession(ctx, file.Props.SavePath, WithConflictBehavior("fail"))
	if err != nil {
		return nil, err
	}

	// watch the callback and upload
	//go handler.client.MonitorUpload(uploadURL, uploadSession.Key, fileInfo.SavePath, fileInfo.Size, ttl)

	uploadSession.ChunkSize = handler.chunkSize
	uploadSession.UploadURL = uploadURL
	return &fs.UploadCredential{
		ChunkSize:  handler.chunkSize,
		UploadURLs: []string{uploadURL},
	}, nil
}

// cancel the upload credential
func (handler *Driver) CancelToken(ctx context.Context, uploadSession *fs.UploadSession) error {
	err := handler.client.DeleteUploadSession(ctx, uploadSession.UploadURL)
	// Create empty placeholder file to stop upload
	if err == nil {
		_, err := handler.client.SimpleUpload(ctx, uploadSession.Props.SavePath, strings.NewReader(""), 0, WithConflictBehavior("replace"))
		if err != nil {
			handler.l.Warning("Failed to create placeholder file %q:%s", uploadSession.Props.SavePath, err)
		}
	}

	return err
}

func (handler *Driver) CompleteUpload(ctx context.Context, session *fs.UploadSession) error {
	if session.SentinelTaskID == 0 {
		return nil
	}

	// Make sure uploaded file size is correct
	res, err := handler.client.Meta(ctx, "", session.Props.SavePath)
	if err != nil {
		// Create empty placeholder file to stop further upload

		return fmt.Errorf("failed to get uploaded file size: %w", err)
	}

	isSharePoint := strings.Contains(handler.policy.Settings.OdDriver, "sharepoint.com") ||
		strings.Contains(handler.policy.Settings.OdDriver, "sharepoint.cn")
	sizeMismatch := res.Size != session.Props.Size
	// SharePoint adds metadata to Office documents, which makes file sizes differ, so allow 1 MB of tolerance here
	// See: https://github.com/OneDrive/onedrive-api-docs/issues/935
	if isSharePoint && sizeMismatch && (res.Size > session.Props.Size) && (res.Size-session.Props.Size <= 1048576) {
		sizeMismatch = false
	}

	if sizeMismatch {
		return serializer.NewError(
			serializer.CodeMetaMismatch,
			fmt.Sprintf("File size not match, expected: %d, actual: %d", session.Props.Size, res.Size),
			nil,
		)
	}

	return nil
}

func (handler *Driver) Capabilities() *driver.Capabilities {
	return &driver.Capabilities{
		StaticFeatures:         features,
		ThumbSupportedExts:     handler.policy.Settings.ThumbExts,
		ThumbSupportAllExts:    handler.policy.Settings.ThumbSupportAllExts,
		ThumbMaxSize:           handler.policy.Settings.ThumbMaxSize,
		ThumbProxy:             handler.policy.Settings.ThumbGeneratorProxy,
		MediaMetaProxy:         handler.policy.Settings.MediaMetaGeneratorProxy,
		BrowserRelayedDownload: handler.policy.Settings.StreamSaver,
	}
}

func (handler *Driver) MediaMeta(ctx context.Context, path, ext, language string) ([]driver.MediaMeta, error) {
	return nil, errors.New("not implemented")
}

func (handler *Driver) LocalPath(ctx context.Context, path string) string {
	return ""
}
