package remote

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/auth"
	"github.com/cloudreve/Cloudreve/v4/pkg/boolset"
	"github.com/cloudreve/Cloudreve/v4/pkg/cluster/routes"
	"github.com/cloudreve/Cloudreve/v4/pkg/conf"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/driver"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/cloudreve/Cloudreve/v4/pkg/request"
	"github.com/cloudreve/Cloudreve/v4/pkg/setting"
)

var (
	features = &boolset.BooleanSet{}
)

// Driver remote storage policy adapter
type Driver struct {
	Client       request.Client
	Policy       *ent.StoragePolicy
	AuthInstance auth.Auth

	uploadClient Client
	config       conf.ConfigProvider
	settings     setting.Provider
}

// New initializes a new Driver from policy
func New(ctx context.Context, policy *ent.StoragePolicy, settings setting.Provider,
	config conf.ConfigProvider, l logging.Logger) (*Driver, error) {
	client, err := NewClient(ctx, policy, settings, config, l)
	if err != nil {
		return nil, err
	}

	return &Driver{
		Policy:       policy,
		Client:       request.NewClient(config),
		AuthInstance: auth.HMACAuth{[]byte(policy.Edges.Node.SlaveKey)},
		uploadClient: client,
		settings:     settings,
		config:       config,
	}, nil
}

// List list files
func (handler *Driver) List(ctx context.Context, base string, onProgress driver.ListProgressFunc, recursive bool) ([]fs.PhysicalObject, error) {
	res, err := handler.uploadClient.List(ctx, base, recursive)
	if err != nil {
		return nil, err
	}

	onProgress(len(res))
	return res, nil
}

// Open gets the file content
func (handler *Driver) Open(ctx context.Context, path string) (*os.File, error) {
	return nil, errors.New("not implemented")
}

func (handler *Driver) LocalPath(ctx context.Context, path string) string {
	return ""
}

// Put saves the file stream to the given path
func (handler *Driver) Put(ctx context.Context, file *fs.UploadRequest) error {
	defer file.Close()

	return handler.uploadClient.Upload(ctx, file)
}

// Delete deletes one or more files,
// Returns the files that were not deleted and the last error encountered
func (handler *Driver) Delete(ctx context.Context, files ...string) ([]string, error) {
	failed, err := handler.uploadClient.DeleteFiles(ctx, files...)
	if err != nil {
		return failed, err
	}
	return []string{}, nil
}

// Thumb gets the file thumbnail
func (handler *Driver) Thumb(ctx context.Context, expire *time.Time, ext string, e fs.Entity) (string, error) {
	serverURL, err := url.Parse(handler.Policy.Edges.Node.Server)
	if err != nil {
		return "", fmt.Errorf("parse server url failed: %w", err)
	}

	thumbURL := routes.SlaveThumbUrl(serverURL, e.Source(), ext)
	signedThumbURL, err := auth.SignURI(ctx, handler.AuthInstance, thumbURL.String(), expire)
	if err != nil {
		return "", err
	}

	return signedThumbURL.String(), nil
}

// Source gets the external URL
func (handler *Driver) Source(ctx context.Context, e fs.Entity, args *driver.GetSourceArgs) (string, error) {
	server, err := url.Parse(handler.Policy.Edges.Node.Server)
	if err != nil {
		return "", err
	}

	nodeId := 0
	if handler.config.System().Mode == conf.SlaveMode {
		nodeId = handler.Policy.NodeID
	}

	base := routes.SlaveFileContentUrl(
		server,
		e.Source(),
		args.DisplayName,
		args.IsDownload,
		args.Speed,
		nodeId,
	)
	internalProxyed, err := auth.SignURI(ctx, handler.AuthInstance, base.String(), args.Expire)
	if err != nil {
		return "", fmt.Errorf("failed to sign internal slave content URL: %w", err)
	}

	return internalProxyed.String(), nil
}

// Token gets the upload policy and auth Token
func (handler *Driver) Token(ctx context.Context, uploadSession *fs.UploadSession, file *fs.UploadRequest) (*fs.UploadCredential, error) {
	siteURL := handler.settings.SiteURL(setting.UseFirstSiteUrl(ctx))
	// Create the upload session on the slave node
	uploadSession.Callback = routes.MasterSlaveCallbackUrl(siteURL, types.PolicyTypeRemote, uploadSession.Props.UploadSessionID, uploadSession.CallbackSecret).String()
	if err := handler.uploadClient.CreateUploadSession(ctx, uploadSession, false); err != nil {
		return nil, err
	}

	// Get the upload URL
	uploadURL, sign, err := handler.uploadClient.GetUploadURL(ctx, uploadSession.Props.ExpireAt, uploadSession.Props.UploadSessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to sign upload url: %w", err)
	}

	// When disk pre-allocation is disabled, concurrent chunk uploads must be 1
	// to avoid disk fragmentation and write contention on slave storage.
	if !handler.Policy.Settings.PreAllocate {
		settings := *handler.Policy.Settings
		settings.ChunkConcurrency = 1
		handler.Policy.Settings = &settings
	}

	return &fs.UploadCredential{
		SessionID:  uploadSession.Props.UploadSessionID,
		ChunkSize:  handler.Policy.Settings.ChunkSize,
		UploadURLs: []string{uploadURL},
		Credential: sign,
	}, nil
}

// Cancels the upload credential
func (handler *Driver) CancelToken(ctx context.Context, uploadSession *fs.UploadSession) error {
	return handler.uploadClient.DeleteUploadSession(ctx, uploadSession.Props.UploadSessionID)
}

func (handler *Driver) CompleteUpload(ctx context.Context, session *fs.UploadSession) error {
	return nil
}

func (handler *Driver) Capabilities() *driver.Capabilities {
	return &driver.Capabilities{
		StaticFeatures:         features,
		MediaMetaSupportedExts: handler.Policy.Settings.MediaMetaExts,
		MediaMetaProxy:         handler.Policy.Settings.MediaMetaGeneratorProxy,
		ThumbSupportedExts:     handler.Policy.Settings.ThumbExts,
		ThumbProxy:             handler.Policy.Settings.ThumbGeneratorProxy,
		ThumbMaxSize:           handler.Policy.Settings.ThumbMaxSize,
		ThumbSupportAllExts:    handler.Policy.Settings.ThumbSupportAllExts,
	}
}

func (handler *Driver) MediaMeta(ctx context.Context, path, ext, language string) ([]driver.MediaMeta, error) {
	return handler.uploadClient.MediaMeta(ctx, path, ext, language)
}
