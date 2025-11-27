package database

import (
	"github.com/valentin-kaiser/go-core/apperror"
	core "github.com/valentin-kaiser/go-core/config"
)

// Config holds the configuration for the database connection
// This struct can be used with the config core package.
// You can use this struct or embed it in your own struct.
type Config struct {
	Driver   string `usage:"Database driver. Currently available options are 'mysql', 'mariadb' or 'sqlite'"`
	Host     string `usage:"IP address or hostname of the database server"`
	Port     uint16 `usage:"Port of the database server to connect to"`
	User     string `usage:"Database username"`
	Password string `usage:"Database password"`
	Name     string `usage:"Name of the database or sqlite file"`
	Search   string `usage:"Search path (schema) for Postgres databases"`
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	if c.Driver == "" {
		return apperror.NewError("database driver is required")
	}

	if c.Driver != "sqlite" && c.Driver != "mysql" && c.Driver != "mariadb" && c.Driver != "postgres" {
		return apperror.NewError("unsupported database driver: " + c.Driver)
	}

	switch c.Driver {
	case "sqlite":
		if c.Name == "" {
			return apperror.NewError("database name (sqlite file) is required")
		}
	default:
		if c.Host == "" {
			return apperror.NewError("database host is required")
		}
		if c.Port == 0 {
			return apperror.NewError("database port is required")
		}
		if c.User == "" {
			return apperror.NewError("database user is required")
		}
		if c.Password == "" {
			return apperror.NewError("database password is required")
		}
		if c.Name == "" {
			return apperror.NewError("database name is required")
		}
	}
	return nil
}

// Changed checks if the configuration has changed
func (c *Config) Changed(n *Config) bool {
	return core.Changed(c, n)
}
