// Package config manages configuration for pronto, including PR view preferences.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Supported PR view names.
const (
	ViewCondensed = "condensed"
	ViewTerminal  = "terminal"
	ViewVSCode    = "vscode"
	ViewWeb       = "web"
	ViewCustom    = "custom"
)

// NotificationConfig specifies configuration for PR event notifications.
type NotificationConfig struct {
	Popups bool              `json:"popups" mapstructure:"popups" toml:"popups"`
	Sound  bool              `json:"sound"  mapstructure:"sound"  toml:"sound"`
	Sounds map[string]string `json:"sounds" mapstructure:"sounds" toml:"sounds"`
	Images map[string]string `json:"images" mapstructure:"images" toml:"images"`
}

// ServerConfig specifies configuration for the daemon socket API.
type ServerConfig struct {
	PollInterval      string `json:"poll_interval"       mapstructure:"poll_interval"       toml:"poll_interval"`
	PProfAddr         string `json:"pprof_addr"          mapstructure:"pprof_addr"          toml:"pprof_addr"`
	LeakCheckInterval string `json:"leak_check_interval" mapstructure:"leak_check_interval" toml:"leak_check_interval"`
}

// Config represents user configuration loaded from pronto.toml and environment variables.
type Config struct {
	PRView        string             `json:"pr_view"         mapstructure:"pr_view"         toml:"pr_view"`
	PRViewCommand string             `json:"pr_view_command" mapstructure:"pr_view_command" toml:"pr_view_command"`
	Notifications NotificationConfig `json:"notifications"   mapstructure:"notifications"   toml:"notifications"`
	Server        ServerConfig       `json:"server"          mapstructure:"server"          toml:"server"`
}

// Dir returns the resolved config directory path.
// Resolution order mirrors internal/logging:
// 1. PRONTO_CONFIG_DIR
// 2. XDG_CONFIG_HOME + /pronto
// 3. ~/.config/pronto (if ~/.config exists)
// 4. os.UserConfigDir() + /pronto
// 5. ~/.pronto
// 6. .
func Dir() string {
	if dir := os.Getenv("PRONTO_CONFIG_DIR"); dir != "" {
		return dir
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "pronto")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		dotConfig := filepath.Join(home, ".config")
		if stat, err := os.Stat(dotConfig); err == nil && stat.IsDir() {
			return filepath.Join(dotConfig, "pronto")
		}
	}
	if base, err := os.UserConfigDir(); err == nil && base != "" {
		return filepath.Join(base, "pronto")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".pronto")
	}
	return "."
}

// Path returns the resolved absolute or relative path to pronto.toml.
func Path() string {
	return filepath.Join(Dir(), "pronto.toml")
}

// Validate checks that the Config has a valid view name, command if custom, and valid notification triggers.
func (c *Config) Validate() error {
	if c.PRView == "" {
		c.PRView = ViewCondensed
	}
	if viewSpec := spec("pr_view"); !validValue(viewSpec.Valid, c.PRView) {
		return fmt.Errorf("unknown pr_view: %q", c.PRView)
	}
	if c.PRView == ViewCustom && strings.TrimSpace(c.PRViewCommand) == "" {
		return errors.New("pr_view 'custom' requires pr_view_command")
	}
	for trigger := range c.Notifications.Sounds {
		if !validValue(spec("notifications.sounds").Valid, trigger) {
			return fmt.Errorf("unknown notification sound trigger: %q", trigger)
		}
	}
	for trigger := range c.Notifications.Images {
		if !validValue(spec("notifications.images").Valid, trigger) {
			return fmt.Errorf("unknown notification image trigger: %q", trigger)
		}
	}
	if c.Server.PollInterval != "" {
		if _, err := time.ParseDuration(c.Server.PollInterval); err != nil {
			return fmt.Errorf("invalid server.poll_interval %q: %w", c.Server.PollInterval, err)
		}
	}
	if c.Server.LeakCheckInterval != "" {
		if d, err := time.ParseDuration(c.Server.LeakCheckInterval); err != nil {
			return fmt.Errorf("invalid server.leak_check_interval %q: %w", c.Server.LeakCheckInterval, err)
		} else if d < 0 {
			return fmt.Errorf("invalid server.leak_check_interval %q: must not be negative", c.Server.LeakCheckInterval)
		}
	}
	return nil
}

// Load loads configuration from the default resolved config path, applying environment variable overrides.
func Load() (Config, error) {
	return LoadFile(Path())
}

// LoadFile loads configuration from a specific file path, applying environment variable overrides.
func LoadFile(path string) (Config, error) {
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("toml")

	for _, keySpec := range Specs {
		if keySpec.Default != nil {
			v.SetDefault(keySpec.Key, keySpec.Default)
		}
		if keySpec.Env != "" {
			_ = v.BindEnv(keySpec.Key, keySpec.Env)
		}
	}

	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) && !errors.Is(err, os.ErrNotExist) {
			return Config{}, fmt.Errorf("read config file %q: %w", path, err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse config file %q: %w", path, err)
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// ExpandPath expands leading ~ to user's home directory.
func ExpandPath(path string) string {
	if path == "" {
		return ""
	}
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}
