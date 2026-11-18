package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeConfig(t *testing.T, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(path, []byte(data), 0o600))
	return path
}

// Without --config, a missing default file gives the defaults.
func TestLoadMissingDefaultFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	cfg, warnings, err := Load("")

	require.NoError(t, err)
	assert.Empty(t, warnings)
	assert.Equal(t, DefaultConfig(), cfg)
}

// L4: a file that the user named with --config must exist.
func TestLoadMissingNamedFileIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.toml")

	_, _, err := Load(path)

	require.ErrorIs(t, err, os.ErrNotExist)
	assert.Contains(t, err.Error(), path)
}

// L4: each unknown key gives a warning. The known keys still apply.
func TestLoadWarnsAboutUnknownKeys(t *testing.T) {
	path := writeConfig(t, "top = 1\n\n[display]\ntheme = \"nord\"\nnerd_font = false\n\n[colors]\nbg = \"red\"\n")

	cfg, warnings, err := Load(path)

	require.NoError(t, err)
	assert.Equal(t, "nord", cfg.Display.Theme)
	assert.True(t, cfg.Display.NerdFonts, "the unknown nerd_font does not change nerd_fonts")
	assert.ElementsMatch(t, []string{
		`unknown key "top" in ` + path,
		`unknown key "display.nerd_font" in ` + path,
		`unknown key "colors" in ` + path,
	}, warnings)
}

func TestLoadKnownKeysGiveNoWarnings(t *testing.T) {
	path := writeConfig(t, "[aws]\nprofile = \"dev\"\n\n[display]\nnerd_fonts = false\n\n[log]\nfile = \"/tmp/x.log\"\n")

	cfg, warnings, err := Load(path)

	require.NoError(t, err)
	assert.Empty(t, warnings)
	assert.Equal(t, "dev", cfg.AWS.Profile)
	assert.False(t, cfg.Display.NerdFonts)
}

// The default file has only known keys.
func TestDefaultFileHasOnlyKnownKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lazycloud", "config.toml")
	written, err := WriteDefault(path)
	require.NoError(t, err)
	require.True(t, written)

	cfg, warnings, err := Load(path)

	require.NoError(t, err)
	assert.Empty(t, warnings)
	assert.Equal(t, DefaultConfig(), cfg)
}

// L4: WriteDefault does not change a file that exists, and says so.
func TestWriteDefaultKeepsAnExistingFile(t *testing.T) {
	path := writeConfig(t, "# mine\n")

	written, err := WriteDefault(path)

	require.NoError(t, err)
	assert.False(t, written)
	data, err := os.ReadFile(path) //nolint:gosec // test file
	require.NoError(t, err)
	assert.Equal(t, "# mine\n", string(data))
}

// L4: the nerd fonts flag can turn the icons on and off. nil leaves the
// setting of the file.
func TestApplyFlagsNerdFonts(t *testing.T) {
	on, off := true, false
	for name, tc := range map[string]struct {
		file bool
		flag *bool
		want bool
	}{
		"not set keeps on":  {file: true, flag: nil, want: true},
		"not set keeps off": {file: false, flag: nil, want: false},
		"flag turns off":    {file: true, flag: &off, want: false},
		"flag turns on":     {file: false, flag: &on, want: true},
	} {
		t.Run(name, func(t *testing.T) {
			c := DefaultConfig()
			c.Display.NerdFonts = tc.file
			c.ApplyFlags("", "", "", "", "", tc.flag)
			assert.Equal(t, tc.want, c.Display.NerdFonts)
		})
	}
}
