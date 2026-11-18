package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// L4: --no-nerd-fonts=false turns the icons on, so a flag can undo
// nerd_fonts = false in the file. Without the flag, the file decides.
func TestParseFlagsNerdFonts(t *testing.T) {
	for name, tc := range map[string]struct {
		args []string
		want *bool
	}{
		"not set":              {args: nil, want: nil},
		"--no-nerd-fonts":      {args: []string{"--no-nerd-fonts"}, want: new(false)},
		"--no-nerd-fonts=true": {args: []string{"--no-nerd-fonts=true"}, want: new(false)},
		"--no-nerd-fonts=false": {
			args: []string{"--no-nerd-fonts=false"}, want: new(true),
		},
	} {
		t.Run(name, func(t *testing.T) {
			o, err := parseFlags(tc.args, io.Discard)
			require.NoError(t, err)
			assert.Equal(t, tc.want, o.nerdFonts)
		})
	}
}

// L4: --init-config writes the file once. A second run says that the file
// exists and does not change it.
func TestInitConfigReportsAnExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lazycloud", "config.toml")
	var out, errOut bytes.Buffer

	require.Equal(t, 0, run([]string{"--init-config", "--config", path}, &out, &errOut))
	assert.Equal(t, "Config written to "+path+"\n", out.String())
	require.NoError(t, os.WriteFile(path, []byte("# mine\n"), 0o600))

	out.Reset()
	require.Equal(t, 0, run([]string{"--init-config", "--config", path}, &out, &errOut))
	assert.Equal(t, "Config file "+path+" already exists. It was not changed.\n", out.String())
	data, err := os.ReadFile(path) //nolint:gosec // test file
	require.NoError(t, err)
	assert.Equal(t, "# mine\n", string(data))
	assert.Empty(t, errOut.String())
}

// L4: a missing file named with --config stops the app with an error.
func TestMissingConfigFileIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.toml")
	var out, errOut bytes.Buffer

	assert.Equal(t, 1, run([]string{"--config", path}, &out, &errOut))
	assert.Contains(t, errOut.String(), "Error loading config")
	assert.Contains(t, errOut.String(), path)
}

// A config file with an unknown key and a bad theme prints the warning,
// then stops at the theme, before the app starts.
func TestUnknownKeyWarningIsPrinted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(path, []byte("[display]\nnerd_font = false\ntheme = \"nope\"\n"), 0o600))
	var out, errOut bytes.Buffer

	assert.Equal(t, 1, run([]string{"--config", path}, &out, &errOut))
	assert.Contains(t, errOut.String(), `Warning: unknown key "display.nerd_font" in `+path)
	assert.Contains(t, errOut.String(), "Unknown theme: nope")
}

func TestHelpAndBadFlags(t *testing.T) {
	assert.Equal(t, 0, run([]string{"-h"}, io.Discard, io.Discard))
	assert.Equal(t, 2, run([]string{"--nope"}, io.Discard, io.Discard))
}

func TestVersion(t *testing.T) {
	var out bytes.Buffer
	assert.Equal(t, 0, run([]string{"--version"}, &out, io.Discard))
	assert.Contains(t, out.String(), "lazycloud ")
}
