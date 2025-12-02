package database_test

import (
	"testing"

	"github.com/valentin-kaiser/go-core/database"
)

// TestConfigValidate_SQLite tests validation for SQLite database configurations
func TestConfigValidate_SQLite(t *testing.T) {
	tests := []struct {
		name    string
		config  database.Config
		wantErr bool
	}{
		{
			name: "valid sqlite config",
			config: database.Config{
				Driver: "sqlite",
				Name:   "test",
			},
			wantErr: false,
		},
		{
			name: "valid sqlite memory config",
			config: database.Config{
				Driver: "sqlite",
				Name:   ":memory:",
			},
			wantErr: false,
		},
		{
			name: "sqlite missing name",
			config: database.Config{
				Driver: "sqlite",
			},
			wantErr: true,
		},
		{
			name: "sqlite empty name",
			config: database.Config{
				Driver: "sqlite",
				Name:   "",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Config.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestConfigValidate_MySQL tests validation for MySQL database configurations
func TestConfigValidate_MySQL(t *testing.T) {
	tests := []struct {
		name    string
		config  database.Config
		wantErr bool
	}{
		{
			name: "valid mysql config",
			config: database.Config{
				Driver:   "mysql",
				Host:     "localhost",
				Port:     3306,
				User:     "root",
				Password: "password",
				Name:     "testdb",
			},
			wantErr: false,
		},
		{
			name: "mysql missing host",
			config: database.Config{
				Driver:   "mysql",
				Port:     3306,
				User:     "root",
				Password: "password",
				Name:     "testdb",
			},
			wantErr: true,
		},
		{
			name: "mysql missing port",
			config: database.Config{
				Driver:   "mysql",
				Host:     "localhost",
				User:     "root",
				Password: "password",
				Name:     "testdb",
			},
			wantErr: true,
		},
		{
			name: "mysql missing user",
			config: database.Config{
				Driver:   "mysql",
				Host:     "localhost",
				Port:     3306,
				Password: "password",
				Name:     "testdb",
			},
			wantErr: true,
		},
		{
			name: "mysql missing password",
			config: database.Config{
				Driver: "mysql",
				Host:   "localhost",
				Port:   3306,
				User:   "root",
				Name:   "testdb",
			},
			wantErr: true,
		},
		{
			name: "mysql missing name",
			config: database.Config{
				Driver:   "mysql",
				Host:     "localhost",
				Port:     3306,
				User:     "root",
				Password: "password",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Config.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestConfigValidate_MariaDB tests validation for MariaDB database configurations
func TestConfigValidate_MariaDB(t *testing.T) {
	tests := []struct {
		name    string
		config  database.Config
		wantErr bool
	}{
		{
			name: "valid mariadb config",
			config: database.Config{
				Driver:   "mariadb",
				Host:     "localhost",
				Port:     3306,
				User:     "root",
				Password: "password",
				Name:     "testdb",
			},
			wantErr: false,
		},
		{
			name: "mariadb missing credentials",
			config: database.Config{
				Driver: "mariadb",
				Host:   "localhost",
				Port:   3306,
				Name:   "testdb",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Config.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestConfigValidate_Postgres tests validation for PostgreSQL database configurations
func TestConfigValidate_Postgres(t *testing.T) {
	tests := []struct {
		name    string
		config  database.Config
		wantErr bool
	}{
		{
			name: "valid postgres config",
			config: database.Config{
				Driver:   "postgres",
				Host:     "localhost",
				Port:     5432,
				User:     "postgres",
				Password: "password",
				Name:     "testdb",
			},
			wantErr: false,
		},
		{
			name: "postgres with search path",
			config: database.Config{
				Driver:   "postgres",
				Host:     "localhost",
				Port:     5432,
				User:     "postgres",
				Password: "password",
				Name:     "testdb",
				Search:   "public",
			},
			wantErr: false,
		},
		{
			name: "postgres missing host",
			config: database.Config{
				Driver:   "postgres",
				Port:     5432,
				User:     "postgres",
				Password: "password",
				Name:     "testdb",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Config.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestConfigValidate_InvalidDriver tests validation for invalid drivers
func TestConfigValidate_InvalidDriver(t *testing.T) {
	tests := []struct {
		name    string
		config  database.Config
		wantErr bool
	}{
		{
			name:    "empty driver",
			config:  database.Config{},
			wantErr: true,
		},
		{
			name: "unsupported driver",
			config: database.Config{
				Driver: "oracle",
			},
			wantErr: true,
		},
		{
			name: "invalid driver",
			config: database.Config{
				Driver: "invalid",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Config.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestConfigChanged tests the Changed method
func TestConfigChanged(t *testing.T) {
	tests := []struct {
		name    string
		old     database.Config
		new     database.Config
		changed bool
	}{
		{
			name: "no change",
			old: database.Config{
				Driver:   "sqlite",
				Name:     "test",
			},
			new: database.Config{
				Driver:   "sqlite",
				Name:     "test",
			},
			changed: false,
		},
		{
			name: "driver changed",
			old: database.Config{
				Driver:   "sqlite",
				Name:     "test",
			},
			new: database.Config{
				Driver:   "mysql",
				Name:     "test",
			},
			changed: true,
		},
		{
			name: "name changed",
			old: database.Config{
				Driver:   "sqlite",
				Name:     "test",
			},
			new: database.Config{
				Driver:   "sqlite",
				Name:     "test2",
			},
			changed: true,
		},
		{
			name: "host changed",
			old: database.Config{
				Driver:   "mysql",
				Host:     "localhost",
				Port:     3306,
				User:     "root",
				Password: "password",
				Name:     "testdb",
			},
			new: database.Config{
				Driver:   "mysql",
				Host:     "remotehost",
				Port:     3306,
				User:     "root",
				Password: "password",
				Name:     "testdb",
			},
			changed: true,
		},
		{
			name: "port changed",
			old: database.Config{
				Driver:   "mysql",
				Host:     "localhost",
				Port:     3306,
				User:     "root",
				Password: "password",
				Name:     "testdb",
			},
			new: database.Config{
				Driver:   "mysql",
				Host:     "localhost",
				Port:     3307,
				User:     "root",
				Password: "password",
				Name:     "testdb",
			},
			changed: true,
		},
		{
			name: "user changed",
			old: database.Config{
				Driver:   "mysql",
				Host:     "localhost",
				Port:     3306,
				User:     "root",
				Password: "password",
				Name:     "testdb",
			},
			new: database.Config{
				Driver:   "mysql",
				Host:     "localhost",
				Port:     3306,
				User:     "admin",
				Password: "password",
				Name:     "testdb",
			},
			changed: true,
		},
		{
			name: "password changed",
			old: database.Config{
				Driver:   "mysql",
				Host:     "localhost",
				Port:     3306,
				User:     "root",
				Password: "password",
				Name:     "testdb",
			},
			new: database.Config{
				Driver:   "mysql",
				Host:     "localhost",
				Port:     3306,
				User:     "root",
				Password: "newpassword",
				Name:     "testdb",
			},
			changed: true,
		},
		{
			name: "search path changed",
			old: database.Config{
				Driver:   "postgres",
				Host:     "localhost",
				Port:     5432,
				User:     "postgres",
				Password: "password",
				Name:     "testdb",
				Search:   "public",
			},
			new: database.Config{
				Driver:   "postgres",
				Host:     "localhost",
				Port:     5432,
				User:     "postgres",
				Password: "password",
				Name:     "testdb",
				Search:   "private",
			},
			changed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.old.Changed(&tt.new)
			if got != tt.changed {
				t.Errorf("Config.Changed() = %v, want %v", got, tt.changed)
			}
		})
	}
}

// BenchmarkConfigValidate benchmarks the Validate method
func BenchmarkConfigValidate(b *testing.B) {
	configs := []database.Config{
		{
			Driver: "sqlite",
			Name:   "test",
		},
		{
			Driver:   "mysql",
			Host:     "localhost",
			Port:     3306,
			User:     "root",
			Password: "password",
			Name:     "testdb",
		},
		{
			Driver:   "postgres",
			Host:     "localhost",
			Port:     5432,
			User:     "postgres",
			Password: "password",
			Name:     "testdb",
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, cfg := range configs {
			_ = cfg.Validate()
		}
	}
}

// BenchmarkConfigChanged benchmarks the Changed method
func BenchmarkConfigChanged(b *testing.B) {
	cfg1 := database.Config{
		Driver:   "mysql",
		Host:     "localhost",
		Port:     3306,
		User:     "root",
		Password: "password",
		Name:     "testdb",
	}
	cfg2 := database.Config{
		Driver:   "mysql",
		Host:     "localhost",
		Port:     3306,
		User:     "root",
		Password: "password",
		Name:     "testdb",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cfg1.Changed(&cfg2)
	}
}
