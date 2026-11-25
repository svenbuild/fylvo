package conf

import (
	"fmt"
	"os"
	"strings"

	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/cloudreve/Cloudreve/v4/pkg/util"
	"github.com/go-ini/ini"
	"github.com/go-playground/validator/v10"
)

const (
	envConfOverrideKey = "CR_CONF_"
)

type ConfigProvider interface {
	Database() *Database
	System() *System
	SSL() *SSL
	Unix() *Unix
	Slave() *Slave
	Redis() *Redis
	Cors() *Cors
	OptionOverwrite() map[string]any
}

// NewIniConfigProvider initializes a new Ini config file provider. A default config file
// will be created if the given path does not exist.
func NewIniConfigProvider(configPath string, l logging.Logger) (ConfigProvider, error) {
	if configPath == "" || !util.Exists(configPath) {
		l.Info("Config file %q not found, creating a new one.", configPath)
		// create the initial config file
		confContent := util.Replace(map[string]string{
			"{SessionSecret}": util.RandStringRunesCrypto(64),
		}, defaultConf)
		f, err := util.CreatNestedFile(configPath)
		if err != nil {
			return nil, fmt.Errorf("failed to create config file: %w", err)
		}

		// write the config file
		_, err = f.WriteString(confContent)
		if err != nil {
			return nil, fmt.Errorf("failed to write config file: %w", err)
		}

		f.Close()
	}

	cfg, err := ini.Load(configPath, []byte(getOverrideConfFromEnv(l)))
	if err != nil {
		return nil, fmt.Errorf("failed to parse config file %q: %w", configPath, err)
	}

	provider := &iniConfigProvider{
		database:        *DatabaseConfig,
		system:          *SystemConfig,
		ssl:             *SSLConfig,
		unix:            *UnixConfig,
		slave:           *SlaveConfig,
		redis:           *RedisConfig,
		cors:            *CORSConfig,
		optionOverwrite: make(map[string]interface{}),
	}

	sections := map[string]interface{}{
		"Database":   &provider.database,
		"System":     &provider.system,
		"SSL":        &provider.ssl,
		"UnixSocket": &provider.unix,
		"Redis":      &provider.redis,
		"CORS":       &provider.cors,
		"Slave":      &provider.slave,
	}
	for sectionName, sectionStruct := range sections {
		err = mapSection(cfg, sectionName, sectionStruct)
		if err != nil {
			return nil, fmt.Errorf("failed to parse config section %q: %w", sectionName, err)
		}
	}

	// map the database config override
	for _, key := range cfg.Section("OptionOverwrite").Keys() {
		provider.optionOverwrite[key.Name()] = key.Value()
	}

	return provider, nil
}

type iniConfigProvider struct {
	database        Database
	system          System
	ssl             SSL
	unix            Unix
	slave           Slave
	redis           Redis
	cors            Cors
	optionOverwrite map[string]any
}

func (i *iniConfigProvider) Database() *Database {
	return &i.database
}

func (i *iniConfigProvider) System() *System {
	return &i.system
}

func (i *iniConfigProvider) SSL() *SSL {
	return &i.ssl
}

func (i *iniConfigProvider) Unix() *Unix {
	return &i.unix
}

func (i *iniConfigProvider) Slave() *Slave {
	return &i.slave
}

func (i *iniConfigProvider) Redis() *Redis {
	return &i.redis
}

func (i *iniConfigProvider) Cors() *Cors {
	return &i.cors
}

func (i *iniConfigProvider) OptionOverwrite() map[string]any {
	return i.optionOverwrite
}

const defaultConf = `[System]
Debug = false
Mode = master
Listen = :5212
SessionSecret = {SessionSecret}
HashIDSalt = {HashIDSalt}
`

// mapSection maps a config file section onto a struct
func mapSection(cfg *ini.File, section string, confStruct interface{}) error {
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

func getOverrideConfFromEnv(l logging.Logger) string {
	confMaps := make(map[string]map[string]string)
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, envConfOverrideKey) {
			continue
		}

		// split by key=value and get key
		kv := strings.SplitN(env, "=", 2)
		if len(kv) < 2 {
			continue
		}
		configKey := strings.TrimPrefix(kv[0], envConfOverrideKey)
		configValue := kv[1]

		// Prefer the native "." separator. Fall back to "__" so that TOML-based
		// deployment platforms (Fly.io, some Docker Compose variants) that
		// mangle dotted env var names can still express Section/Key overrides
		// as CR_CONF_Section__Key.
		sectionKey := strings.SplitN(configKey, ".", 2)
		if len(sectionKey) < 2 {
			sectionKey = strings.SplitN(configKey, "__", 2)
		}
		if len(sectionKey) < 2 || sectionKey[0] == "" || sectionKey[1] == "" {
			l.Warning("Skipping malformed %s override %q: expected %sSection.Key or %sSection__Key format",
				envConfOverrideKey, kv[0], envConfOverrideKey, envConfOverrideKey)
			continue
		}

		if confMaps[sectionKey[0]] == nil {
			confMaps[sectionKey[0]] = make(map[string]string)
		}

		confMaps[sectionKey[0]][sectionKey[1]] = configValue
		// Fylvo: only the key, the value can be a password.
		l.Info("Override config %q from environment", configKey)
	}

	// generate ini content
	var sb strings.Builder
	for section, kvs := range confMaps {
		sb.WriteString(fmt.Sprintf("[%s]\n", section))
		for k, v := range kvs {
			sb.WriteString(fmt.Sprintf("%s = %s\n", k, v))
		}
	}

	return sb.String()
}
