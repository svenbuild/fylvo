package model

import (
	"fmt"
	"time"

	"github.com/jinzhu/gorm"

	"github.com/cloudreve/Cloudreve/v4/application/migrator/conf"
	"github.com/cloudreve/Cloudreve/v4/pkg/util"
	_ "github.com/jinzhu/gorm/dialects/mssql"
	_ "github.com/jinzhu/gorm/dialects/mysql"
	_ "github.com/jinzhu/gorm/dialects/postgres"
)

// DB database connection singleton
var DB *gorm.DB

// Init initializes the MySQL connection
func Init() error {
	var (
		db         *gorm.DB
		err        error
		confDBType string = conf.DatabaseConfig.Type
	)

	// stay compatible with the "sqlite3" setting in existing configs
	if confDBType == "sqlite3" {
		confDBType = "sqlite"
	}
	
	// stay compatible with the "mariadb" database
	if confDBType == "mariadb" {
		confDBType = "mysql"
	}

	switch confDBType {
	case "UNSET", "sqlite":
		// use SQLite when no database is set or sqlite is set explicitly
		db, err = gorm.Open("sqlite3", util.RelativePath(conf.DatabaseConfig.DBFile))
	case "postgres":
		db, err = gorm.Open(confDBType, fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%d sslmode=disable",
			conf.DatabaseConfig.Host,
			conf.DatabaseConfig.User,
			conf.DatabaseConfig.Password,
			conf.DatabaseConfig.Name,
			conf.DatabaseConfig.Port))
	case "mysql", "mssql":
		var host string
		if conf.DatabaseConfig.UnixSocket {
			host = fmt.Sprintf("unix(%s)",
				conf.DatabaseConfig.Host)
		} else {
			host = fmt.Sprintf("(%s:%d)",
				conf.DatabaseConfig.Host,
				conf.DatabaseConfig.Port)
		}

		db, err = gorm.Open(confDBType, fmt.Sprintf("%s:%s@%s/%s?charset=%s&parseTime=True&loc=Local",
			conf.DatabaseConfig.User,
			conf.DatabaseConfig.Password,
			host,
			conf.DatabaseConfig.Name,
			conf.DatabaseConfig.Charset))
	default:
		return fmt.Errorf("unsupported database type %q", confDBType)
	}

	//db.SetLogger(util.Log())
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}

	// handle table prefix
	gorm.DefaultTableNameHandler = func(db *gorm.DB, defaultTableName string) string {
		return conf.DatabaseConfig.TablePrefix + defaultTableName
	}

	// Debug mode, log all SQL
	db.LogMode(true)

	//set up the connection pool
	db.DB().SetMaxIdleConns(50)
	if confDBType == "sqlite" || confDBType == "UNSET" {
		db.DB().SetMaxOpenConns(1)
	} else {
		db.DB().SetMaxOpenConns(100)
	}

	//timeout
	db.DB().SetConnMaxLifetime(time.Second * 30)

	DB = db

	return nil
}
