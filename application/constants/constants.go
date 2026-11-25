package constants

// These values will be injected at build time, DO NOT EDIT.

// BackendVersion is the current backend version
var BackendVersion = "4.14.0"

// FylvoVersion is the Fylvo release (the git tag without "v"), shown in the admin panel and the
// startup line. BackendVersion stays the version of the Cloudreve base: the database migration,
// the embedded web app and the apps compare against it.
var FylvoVersion = "dev"

// IsPro tells whether this is the Pro version
var IsPro = "false"

var IsProBool = IsPro == "true"

// LastCommit is the last commit id
var LastCommit = "000000"

const (
	APIPrefix      = "/api/v4"
	APIPrefixSlave = "/api/v4/slave"
	CrHeaderPrefix = "X-Cr-"
)

const CloudreveScheme = "cloudreve"

type (
	FileSystemType string
)

const (
	FileSystemMy           = FileSystemType("my")
	FileSystemShare        = FileSystemType("share")
	FileSystemTrash        = FileSystemType("trash")
	FileSystemSharedWithMe = FileSystemType("shared_with_me")
	// FileSystemConnected Fylvo: a storage at another provider that an account connected.
	// Its files have no rows in the database; see pkg/connected.
	FileSystemConnected = FileSystemType("connected")
	FileSystemUnknown   = FileSystemType("unknown")
)
