package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// Config holds application configuration.
// Precedence: flags > env vars > config file > defaults.
type Config struct {
	AWS     AWSConfig     `toml:"aws"`
	Display DisplayConfig `toml:"display"`
	Log     LogConfig     `toml:"log"`
	Rules   []Rule        `toml:"rules"` // see rules.go
}

// AWSConfig holds AWS-related settings.
type AWSConfig struct {
	Profile string `toml:"profile"`
	// Region is an explicit override. Empty means "let the AWS SDK resolve it"
	// from the profile in ~/.aws/config (the resolved value lives on aws.Client).
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
		Display: DisplayConfig{
			Theme:     "auto",
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

// Load reads the config file at path and returns the merged config. An
// empty path means DefaultPath, and then a missing file gives the defaults.
// A path that the user named, with --config, must exist.
//
// The warnings name each key in the file that the config does not know,
// for example "nerd_font" in place of "nerd_fonts". The other keys still
// apply.
func Load(path string) (cfg Config, warnings []string, err error) {
	cfg = DefaultConfig()

	named := path != ""
	if !named {
		path = DefaultPath()
	}

	data, err := os.ReadFile(path) //nolint:gosec // config path is from DefaultPath() or CLI flag, not user input
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && !named {
			return cfg, nil, nil
		}
		return cfg, nil, err
	}

	if err := toml.Unmarshal(data, &cfg); err != nil {
		return cfg, nil, err
	}
	warnings = unknownKeys(data, path)
	return cfg, append(warnings, cfg.checkRules(path)...), nil
}

// unknownKeys returns a warning for each key in data that Config does not
// have.
func unknownKeys(data []byte, path string) []string {
	var strict Config
	err := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(&strict)
	var missing *toml.StrictMissingError
	if !errors.As(err, &missing) {
		return nil
	}
	warnings := make([]string, 0, len(missing.Errors))
	for _, e := range missing.Errors {
		warnings = append(warnings, fmt.Sprintf("unknown key %q in %s", strings.Join(e.Key(), "."), path))
	}
	return warnings
}

// WriteDefault writes a default config file with comments to the given path.
// Creates parent directories as needed. It does not change a file that
// exists, and then returns false.
func WriteDefault(path string) (written bool, err error) {
	if path == "" {
		path = DefaultPath()
	}

	if _, err := os.Stat(path); err == nil {
		return false, nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return false, err
	}

	contents := `# LazyCloud Configuration
# https://github.com/juthrbog/lazycloud

[aws]
# Default AWS profile (overridden by --profile flag or AWS_PROFILE env var)
# profile = "default"

# AWS region override (overridden by --region flag or AWS_REGION / AWS_DEFAULT_REGION
# env vars). Leave unset to use the region of the active profile in ~/.aws/config.
# region = "us-east-1"

# Endpoint override, for example for LocalStack (overridden by --endpoint flag
# or AWS_ENDPOINT_URL env var). With an endpoint, the app uses your usual AWS
# credentials, or the dummy credentials "test" when you have none set up.
# endpoint = ""

[display]
# Color theme: auto, catppuccin, catppuccin-latte, dracula, nord, tokyonight,
# tokyonight-day. auto uses catppuccin on a dark terminal and catppuccin-latte
# on a light one.
theme = "auto"

# Use Nerd Font icons (set to false for plain Unicode fallbacks)
nerd_fonts = true

[log]
# Path to debug log file (empty = no logging)
# file = "/tmp/lazycloud.log"

# The app always starts in ReadOnly mode. Use the --read-write flag, or the
# mode picker (W) in the app, to allow changes to AWS resources.

# Rules lock profiles to ReadOnly, so W and --read-write cannot switch them
# to ReadWrite. A session is locked when any matching rule has
# read_only = true. The first matching rule with a label sets the label in
# the header. profiles are globs: * matches any text. "default" also
# matches the default profile.
#
# [[rules]]
# name = "prod"                          # shown in messages
# profiles = ["prod-*", "*-production"]
# read_only = true
# label = "PROD"                         # at most 8 characters
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		return false, err
	}
	return true, nil
}

// ApplyEnv overlays environment variables onto the config.
func (c *Config) ApplyEnv() {
	if v := os.Getenv("AWS_PROFILE"); v != "" {
		c.AWS.Profile = v
	}
	// AWS_REGION wins over AWS_DEFAULT_REGION, matching the AWS SDK and CLI.
	if v := os.Getenv("AWS_DEFAULT_REGION"); v != "" {
		c.AWS.Region = v
	}
	if v := os.Getenv("AWS_REGION"); v != "" {
		c.AWS.Region = v
	}
	if v := os.Getenv("AWS_ENDPOINT_URL"); v != "" {
		c.AWS.Endpoint = v
	}
}

// ApplyFlags overlays CLI flag values onto the config.
// Empty strings and a nil nerdFonts are treated as "not set" and don't
// override.
func (c *Config) ApplyFlags(profile, region, endpoint, logFile, theme string, nerdFonts *bool) {
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
	if nerdFonts != nil {
		c.Display.NerdFonts = *nerdFonts
	}
}
