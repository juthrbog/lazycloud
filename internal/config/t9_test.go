package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// T9: a file that is not valid TOML is an error that names the line.
func TestLoadBadTOMLIsAnError(t *testing.T) {
	path := writeConfig(t, "[aws]\nprofile = \"dev\"\nregion = \n")

	_, _, err := Load(path)

	var decErr *toml.DecodeError
	require.ErrorAs(t, err, &decErr)
	row, _ := decErr.Position()
	assert.Equal(t, 3, row)
}

// T9: a value of the wrong type is an error.
func TestLoadWrongTypeIsAnError(t *testing.T) {
	path := writeConfig(t, "[display]\nnerd_fonts = \"yes\"\n")

	_, _, err := Load(path)

	require.Error(t, err)
}

// T9: a partial file changes only the keys that it has. The other keys keep
// their default values.
func TestLoadPartialFileKeepsTheDefaults(t *testing.T) {
	path := writeConfig(t, "[aws]\nprofile = \"dev\"\n")

	cfg, warnings, err := Load(path)

	require.NoError(t, err)
	assert.Empty(t, warnings)
	want := DefaultConfig()
	want.AWS.Profile = "dev"
	assert.Equal(t, want, cfg)
}

// T9: WriteDefault makes the parent folders and a file that only the user
// can read, because the file can name an endpoint and a profile. The file
// loads with the default values.
func TestWriteDefaultMakesAPrivateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a", "b", "config.toml")

	written, err := WriteDefault(path)

	require.NoError(t, err)
	assert.True(t, written)
	info, err := os.Stat(path)
	require.NoError(t, err)
	if runtime.GOOS != "windows" {
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
	cfg, warnings, err := Load(path)
	require.NoError(t, err)
	assert.Empty(t, warnings)
	assert.Equal(t, DefaultConfig(), cfg)
}

// T9: WriteDefault with an empty path writes the file at DefaultPath.
func TestWriteDefaultUsesTheDefaultPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	written, err := WriteDefault("")

	require.NoError(t, err)
	assert.True(t, written)
	assert.FileExists(t, DefaultPath())
}

// T9: for the profile and the endpoint, a flag wins over the env, and the
// env wins over the file.
func TestProfileAndEndpointPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name        string
		file        string
		env         string
		flag        string
		wantProfile string
	}{
		{name: "file only", file: "f", wantProfile: "f"},
		{name: "env wins over file", file: "f", env: "e", wantProfile: "e"},
		{name: "flag wins over env", file: "f", env: "e", flag: "x", wantProfile: "x"},
		{name: "flag wins over file", file: "f", flag: "x", wantProfile: "x"},
		{name: "nothing set stays empty", wantProfile: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AWS_REGION", "")
			t.Setenv("AWS_DEFAULT_REGION", "")
			t.Setenv("AWS_PROFILE", tc.env)
			t.Setenv("AWS_ENDPOINT_URL", endpoint(tc.env))

			c := DefaultConfig()
			c.AWS.Profile = tc.file
			c.AWS.Endpoint = endpoint(tc.file)
			c.ApplyEnv()
			c.ApplyFlags(tc.flag, "", endpoint(tc.flag), "", "", nil)

			assert.Equal(t, tc.wantProfile, c.AWS.Profile)
			assert.Equal(t, endpoint(tc.wantProfile), c.AWS.Endpoint)
		})
	}
}

// endpoint gives a URL for a name, and an empty string for no name.
func endpoint(name string) string {
	if name == "" {
		return ""
	}
	return "http://" + name + ".example:4566"
}

// T9: the theme and log file flags win over the file. Empty flags keep the
// values of the file.
func TestApplyFlagsThemeAndLogFile(t *testing.T) {
	c := DefaultConfig()
	c.Display.Theme = "nord"
	c.Log.File = "/var/log/a.log"

	c.ApplyFlags("", "", "", "", "", nil)
	assert.Equal(t, "nord", c.Display.Theme)
	assert.Equal(t, "/var/log/a.log", c.Log.File)

	c.ApplyFlags("", "", "", "/tmp/b.log", "dracula", nil)
	assert.Equal(t, "dracula", c.Display.Theme)
	assert.Equal(t, "/tmp/b.log", c.Log.File)
}
