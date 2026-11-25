package conf

// RedisConfig is the Redis server config
var RedisConfig = &redis{
	Network:  "tcp",
	Server:   "",
	Password: "",
	DB:       "0",
}

// DatabaseConfig is the database config
var DatabaseConfig = &database{
	Type:       "UNSET",
	Charset:    "utf8",
	DBFile:     "cloudreve.db",
	Port:       3306,
	UnixSocket: false,
}

// SystemConfig is the general system config
var SystemConfig = &system{
	Debug:       false,
	Mode:        "master",
	Listen:      ":5212",
	ProxyHeader: "",
}

// CORSConfig is the CORS config
var CORSConfig = &cors{
	AllowOrigins:     []string{"UNSET"},
	AllowMethods:     []string{"PUT", "POST", "GET", "OPTIONS"},
	AllowHeaders:     []string{"Cookie", "X-Cr-Policy", "Authorization", "Content-Length", "Content-Type", "X-Cr-Path", "X-Cr-FileName"},
	AllowCredentials: false,
	ExposeHeaders:    nil,
	SameSite:         "Default",
	Secure:           false,
}

// SlaveConfig is the slave node config
var SlaveConfig = &slave{
	CallbackTimeout: 20,
	SignatureTTL:    60,
}

var SSLConfig = &ssl{
	Listen:   ":443",
	CertPath: "",
	KeyPath:  "",
}

var UnixConfig = &unix{
	Listen: "",
}

var OptionOverwrite = map[string]interface{}{}
