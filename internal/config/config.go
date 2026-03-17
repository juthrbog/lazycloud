package config

import (
	"os"
	"path/filepath"

	toml "github.com/pelletier/go-toml/v2"
)

// Config holds application configuration.
// Precedence: flags > env vars > config file > defaults.
type Config struct {
	AWS     AWSConfig     `toml:"aws"`
	Display DisplayConfig `toml:"display"`
	Log     LogConfig     `toml:"log"`
}

// AWSConfig holds AWS-related settings.
type AWSConfig struct {
	Profile  string `toml:"profile"`
	Region   string `toml:"region"`
	Endpoint string `toml:"endpoint"`
}

// DisplayConfig holds UI-related settings.
type DisplayConfig struct {
	Theme     string `toml:"theme"`
	NerdFonts bool   `toml:"nerd_fonts"`
}

// LogConfig holds logging settings.
type LogConfig struct {
	File string `toml:"file"`
}

// DefaultConfig returns the default configuration.
func DefaultConfig() Config {
	return Config{
		AWS: AWSConfig{
			Region: "us-east-1",
		},
		Display: DisplayConfig{
			Theme:     "catppuccin",
			NerdFonts: true,
		},
	}
}

// DefaultPath returns the default config file path.
// Uses $XDG_CONFIG_HOME/lazycloud/config.toml, falling back to ~/.config/lazycloud/config.toml.
func DefaultPath() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "lazycloud", "config.toml")
}

// Load reads the config file at path and returns the merged config.
// Missing file is not an error — defaults are returned.
func Load(path string) (Config, error) {
	cfg := DefaultConfig()

	if path == "" {
		path = DefaultPath()
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}

	if err := toml.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}

	return cfg, nil
}

// WriteDefault writes a default config file with comments to the given path.
// Creates parent directories as needed. Does not overwrite existing files.
func WriteDefault(path string) error {
	if path == "" {
		path = DefaultPath()
	}

	if _, err := os.Stat(path); err == nil {
		return nil // file exists, don't overwrite
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	contents := `# LazyCloud Configuration
# https://github.com/juthrbog/lazycloud

[aws]
# Default AWS profile (overridden by --profile flag or AWS_PROFILE env var)
# profile = "default"

# Default AWS region (overridden by --region flag or AWS_REGION env var)
# region = "us-east-1"

# Endpoint override for LocalStack (overridden by --endpoint flag or AWS_ENDPOINT_URL env var)
# endpoint = ""

[display]
# Color theme: catppuccin, dracula, nord, tokyonight
theme = "catppuccin"

# Use Nerd Font icons (set to false for plain Unicode fallbacks)
nerd_fonts = true

[log]
# Path to debug log file (empty = no logging)
# file = "/tmp/lazycloud.log"
`
	return os.WriteFile(path, []byte(contents), 0o644)
}

// ApplyEnv overlays environment variables onto the config.
func (c *Config) ApplyEnv() {
	if v := os.Getenv("AWS_PROFILE"); v != "" {
		c.AWS.Profile = v
	}
	if v := os.Getenv("AWS_REGION"); v != "" {
		c.AWS.Region = v
	}
	if v := os.Getenv("AWS_ENDPOINT_URL"); v != "" {
		c.AWS.Endpoint = v
	}
}

// ApplyFlags overlays CLI flag values onto the config.
// Empty strings are treated as "not set" and don't override.
func (c *Config) ApplyFlags(profile, region, endpoint, logFile, theme string, noNerdFonts bool) {
	if profile != "" {
		c.AWS.Profile = profile
	}
	if region != "" {
		c.AWS.Region = region
	}
	if endpoint != "" {
		c.AWS.Endpoint = endpoint
	}
	if logFile != "" {
		c.Log.File = logFile
	}
	if theme != "" {
		c.Display.Theme = theme
	}
	if noNerdFonts {
		c.Display.NerdFonts = false
	}
}
