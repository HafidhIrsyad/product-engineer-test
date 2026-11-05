// Package config loads application settings with viper.
// Priority: environment variables (MGMI_*) > config.yaml > defaults (ARCHITECTURE.md §5.3).
package config

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// EnvLocal enables console logs and SQL query logs.
const EnvLocal = "local"

type Config struct {
	App      App      `mapstructure:"app"`
	HTTP     HTTP     `mapstructure:"http"`
	Database Database `mapstructure:"database"`
	Log      Log      `mapstructure:"log"`
	Sweeper  Sweeper  `mapstructure:"sweeper"`
}

type App struct {
	Env string `mapstructure:"env"`
}

type HTTP struct {
	Port           int           `mapstructure:"port"`
	RequestTimeout time.Duration `mapstructure:"request_timeout"`
}

type Database struct {
	URL string `mapstructure:"url"`
}

type Log struct {
	Level string `mapstructure:"level"`
}

type Sweeper struct {
	Interval time.Duration `mapstructure:"interval"`
}

// IsLocal reports whether the app runs in the local environment.
func (c Config) IsLocal() bool { return c.App.Env == EnvLocal }

// Load reads config.yaml (optional) and MGMI_* environment variables.
func Load() (Config, error) {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")
	v.SetEnvPrefix("MGMI")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_")) // http.port → MGMI_HTTP_PORT
	v.AutomaticEnv()

	// Every key MUST have a default: without one, Unmarshal ignores the key's env var.
	v.SetDefault("app.env", EnvLocal)
	v.SetDefault("http.port", 8080)
	v.SetDefault("http.request_timeout", "10s")
	v.SetDefault("database.url", "")
	v.SetDefault("log.level", "info")
	v.SetDefault("sweeper.interval", "30s")

	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) {
			return Config{}, fmt.Errorf("read config.yaml: %w", err)
		}
		// No config.yaml: environment variables and defaults are enough (e.g. docker-compose).
	}

	var c Config
	if err := v.Unmarshal(&c); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	if c.Database.URL == "" {
		return Config{}, errors.New("database.url (MGMI_DATABASE_URL) is required")
	}
	return c, nil
}
