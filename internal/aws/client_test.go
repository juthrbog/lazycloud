package aws

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewClientRegionResolution(t *testing.T) {
	tests := []struct {
		name          string
		profile       string
		region        string // explicit override passed to NewClient
		defaultRegion string // AWS_DEFAULT_REGION
		want          string
	}{
		{name: "profile region used when no override", profile: "withregion", want: "eu-west-2"},
		{name: "explicit override wins over profile", profile: "withregion", region: "us-west-1", want: "us-west-1"},
		{name: "env wins over profile", profile: "withregion", defaultRegion: "ap-south-1", want: "ap-south-1"},
		{name: "fallback when nothing is configured", profile: "noregion", want: FallbackRegion},
	}

	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config")
	require.NoError(t, os.WriteFile(cfgFile, []byte(
		"[profile withregion]\nregion = eu-west-2\n\n[profile noregion]\noutput = json\n",
	), 0o600))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AWS_CONFIG_FILE", cfgFile)
			t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "missing"))
			t.Setenv("AWS_PROFILE", "")
			t.Setenv("AWS_REGION", "")
			t.Setenv("AWS_DEFAULT_REGION", tt.defaultRegion)

			c, err := NewClient(tt.profile, tt.region, "")
			require.NoError(t, err)
			assert.Equal(t, tt.want, c.Region)
			assert.Equal(t, tt.want, c.Config.Region, "client and SDK config must agree")
		})
	}
}
