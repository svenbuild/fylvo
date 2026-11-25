package conf

import (
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/go-ini/ini"
	"github.com/go-playground/validator/v10"
)

// database is the database config
type database struct {
	Type        string
	User        string
	Password    string
	Host        string
	Name        string
	TablePrefix string
	DBFile      string
	Port        int
	Charset     string
	UnixSocket  bool
}

// system is the general system config
type system struct {
	Mode          string `validate:"eq=master|eq=slave"`
	Listen        string `validate:"required"`
	Debug         bool
	SessionSecret string
	HashIDSalt    string
	GracePeriod   int `validate:"gte=0"`
	ProxyHeader   string
}

type ssl struct {
	CertPath string `validate:"omitempty,required"`
	KeyPath  string `validate:"omitempty,required"`
	Listen   string `validate:"required"`
}

type unix struct {
	Listen string
	Perm   uint32
}

// slave is the config for running as a slave storage node
type slave struct {
	Secret          string `validate:"omitempty,gte=64"`
	CallbackTimeout int    `validate:"omitempty,gte=1"`
	SignatureTTL    int    `validate:"omitempty,gte=1"`
}

// redis is the redis config
type redis struct {
	Network  string
	Server   string
	User     string
	Password string
	DB       string
}

// CORS config
type cors struct {
	AllowOrigins     []string
	AllowMethods     []string
	AllowHeaders     []string
	AllowCredentials bool
	ExposeHeaders    []string
	SameSite         string
	Secure           bool
}

var cfg *ini.File

// Init initializes the config file
func Init(l logging.Logger, path string) error {
	var err error

	cfg, err = ini.Load(path)
	if err != nil {
		l.Error("Failed to parse config file %q: %s", path, err)
		return err
	}

	sections := map[string]interface{}{
		"Database":   DatabaseConfig,
		"System":     SystemConfig,
		"SSL":        SSLConfig,
		"UnixSocket": UnixConfig,
		"Redis":      RedisConfig,
		"CORS":       CORSConfig,
		"Slave":      SlaveConfig,
	}
	for sectionName, sectionStruct := range sections {
		err = mapSection(sectionName, sectionStruct)
		if err != nil {
			l.Error("Failed to parse config section %q: %s", sectionName, err)
			return err
		}
	}

	// map the database config override
	for _, key := range cfg.Section("OptionOverwrite").Keys() {
		OptionOverwrite[key.Name()] = key.Value()
	}

	return nil
}

// mapSection maps a config file section onto a struct
func mapSection(section string, confStruct interface{}) error {
	err := cfg.Section(section).MapTo(confStruct)
	if err != nil {
		return err
	}

	// validate
	validate := validator.New()
	err = validate.Struct(confStruct)
	if err != nil {
		return err
	}

	return nil
}
