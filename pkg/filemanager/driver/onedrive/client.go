package onedrive

import (
	"context"
	"errors"
	"io"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/pkg/credmanager"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/cloudreve/Cloudreve/v4/pkg/setting"

	"github.com/cloudreve/Cloudreve/v4/pkg/request"
)

var (
	// ErrAuthEndpoint cannot parse the auth endpoint URL
	ErrAuthEndpoint = errors.New("failed to parse endpoint url")
	// ErrInvalidRefreshToken the upload policy has no valid RefreshToken
	ErrInvalidRefreshToken = errors.New("no valid refresh token in this policy")
	// ErrDeleteFile cannot delete file
	ErrDeleteFile = errors.New("cannot delete file")
	// ErrClientCanceled client canceled the operation
	ErrClientCanceled = errors.New("client canceled")
	// Desired thumb size not available
	ErrThumbSizeNotFound = errors.New("thumb size not found")
)

type Client interface {
	ListChildren(ctx context.Context, path string) ([]FileInfo, error)
	Meta(ctx context.Context, id string, path string) (*FileInfo, error)
	CreateUploadSession(ctx context.Context, dst string, opts ...Option) (string, error)
	GetSiteIDByURL(ctx context.Context, siteUrl string) (string, error)
	GetUploadSessionStatus(ctx context.Context, uploadURL string) (*UploadSessionResponse, error)
	Upload(ctx context.Context, file *fs.UploadRequest) error
	SimpleUpload(ctx context.Context, dst string, body io.Reader, size int64, opts ...Option) (*UploadResult, error)
	DeleteUploadSession(ctx context.Context, uploadURL string) error
	BatchDelete(ctx context.Context, dst []string) ([]string, error)
	GetThumbURL(ctx context.Context, dst string) (string, error)
	OAuthURL(ctx context.Context, scopes []string) string
	ObtainToken(ctx context.Context, opts ...Option) (*Credential, error)
}

// client OneDrive client
type client struct {
	endpoints  *endpoints
	policy     *ent.StoragePolicy
	credential credmanager.Credential

	httpClient request.Client
	cred       credmanager.CredManager
	l          logging.Logger
	settings   setting.Provider

	chunkSize int64
}

// endpoints OneDrive client related settings
type endpoints struct {
	oAuthEndpoints *oauthEndpoint
	endpointURL    string // base URL for API requests
	driverResource string // drive to use
}

// NewClient gets a new client from the storage policy
func NewClient(policy *ent.StoragePolicy, httpClient request.Client, cred credmanager.CredManager,
	l logging.Logger, settings setting.Provider, chunkSize int64) Client {
	client := &client{
		endpoints: &endpoints{
			endpointURL:    policy.Server,
			driverResource: policy.Settings.OdDriver,
		},
		policy:     policy,
		httpClient: httpClient,
		cred:       cred,
		l:          l,
		settings:   settings,
		chunkSize:  chunkSize,
	}

	if client.endpoints.driverResource == "" {
		client.endpoints.driverResource = "me/drive"
	}

	oauthBase := getOAuthEndpoint(policy.Server)
	client.endpoints.oAuthEndpoints = oauthBase

	return client
}
