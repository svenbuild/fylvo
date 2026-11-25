package conf

import "github.com/cloudreve/Cloudreve/v4/pkg/util"

type DBType string

var (
	SQLiteDB   DBType = "sqlite"
	SQLite3DB  DBType = "sqlite3"
	MySqlDB    DBType = "mysql"
	MsSqlDB    DBType = "mssql"
	PostgresDB DBType = "postgres"
	MariaDB    DBType = "mariadb"
)

// Database is the database config
type Database struct {
	Type        DBType
	User        string
	Password    string
	Host        string
	Name        string
	TablePrefix string
	DBFile      string
	Port        int
	Charset     string
	UnixSocket  bool
	// allow configuring the database connection directly via DATABASE_URL
	DatabaseURL string
	// SSLMode allows connecting to the database with SSL, users can add certificates etc. in the sslmode string
	SSLMode string
}

type SysMode string

var (
	MasterMode SysMode = "master"
	SlaveMode  SysMode = "slave"
)

// System is the general system config
type System struct {
	Mode          SysMode `validate:"eq=master|eq=slave"`
	Listen        string  `validate:"required"`
	Debug         bool
	SessionSecret string
	HashIDSalt    string // deprecated
	GracePeriod   int    `validate:"gte=0"`
	ProxyHeader   string
	LogLevel      string `validate:"oneof=debug info warning error"`
	Pprof         string // Address to listen for pprof, e.g. "localhost:6060". Empty to disable.
}

type SSL struct {
	CertPath string `validate:"omitempty,required"`
	KeyPath  string `validate:"omitempty,required"`
	Listen   string `validate:"required"`
}

type Unix struct {
	Listen string
	Perm   uint32
}

// Slave settings for running as a slave storage node
type Slave struct {
	Secret          string `validate:"omitempty,gte=64"`
	CallbackTimeout int    `validate:"omitempty,gte=1"`
	SignatureTTL    int    `validate:"omitempty,gte=1"`
}

// Redis config
type Redis struct {
	Network       string
	Server        string
	User          string
	Password      string
	DB            string
	UseTLS        bool
	TLSSkipVerify bool
}

// CORS settings
type Cors struct {
	AllowOrigins     []string
	AllowMethods     []string
	AllowHeaders     []string
	AllowCredentials bool
	ExposeHeaders    []string
	SameSite         string
	Secure           bool
}

// RedisConfig Redis server settings
var RedisConfig = &Redis{
	Network:       "tcp",
	Server:        "",
	Password:      "",
	DB:            "0",
	UseTLS:        false,
	TLSSkipVerify: true,
}

// DatabaseConfig database settings
var DatabaseConfig = &Database{
	Charset:     "utf8mb4",
	DBFile:      defaultDBFile(),
	Port:        3306,
	UnixSocket:  false,
	DatabaseURL: "",
}

// SystemConfig general system settings
var SystemConfig = &System{
	Debug:       false,
	Mode:        MasterMode,
	Listen:      ":5212",
	ProxyHeader: "",
	LogLevel:    "info",
}

// CORSConfig CORS settings
var CORSConfig = &Cors{
	AllowOrigins:     []string{"UNSET"},
	AllowMethods:     []string{"PUT", "POST", "GET", "OPTIONS"},
	AllowHeaders:     []string{"Cookie", "X-Cr-Policy", "Authorization", "Content-Length", "Content-Type", "X-Cr-Path", "X-Cr-FileName"},
	AllowCredentials: false,
	ExposeHeaders:    nil,
	SameSite:         "Default",
	Secure:           false,
}

// SlaveConfig slave settings
var SlaveConfig = &Slave{
	CallbackTimeout: 20,
	SignatureTTL:    600,
}

var SSLConfig = &SSL{
	Listen:   ":443",
	CertPath: "",
	KeyPath:  "",
}

var UnixConfig = &Unix{
	Listen: "",
}

var OptionOverwrite = map[string]interface{}{}

// Fylvo: new installations keep their database in fylvo.db. Installations from before the rename
// have cloudreve.db and keep using it, also when a fylvo.db appeared next to it (for example from a
// start while cloudreve.db was missing during a restore): the old file holds the real data.
func defaultDBFile() string {
	legacy := util.DataPath("cloudreve.db")
	if util.Exists(legacy) {
		return legacy
	}

	return util.DataPath("fylvo.db")
}
