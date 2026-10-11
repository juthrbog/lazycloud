package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The default must leave the region empty so the AWS profile can decide it.
func TestDefaultConfigHasNoRegion(t *testing.T) {
	assert.Empty(t, DefaultConfig().AWS.Region)
}

func TestApplyEnvRegionPrecedence(t *testing.T) {
	tests := []struct {
		name          string
		fileRegion    string
		region        string // AWS_REGION
		defaultRegion string // AWS_DEFAULT_REGION
		want          string
	}{
		{name: "no env keeps file value", fileRegion: "eu-west-1", want: "eu-west-1"},
		{name: "AWS_DEFAULT_REGION overrides file", fileRegion: "eu-west-1", defaultRegion: "ap-south-1", want: "ap-south-1"},
		{name: "AWS_REGION overrides AWS_DEFAULT_REGION", defaultRegion: "ap-south-1", region: "us-west-2", want: "us-west-2"},
		{name: "nothing set stays empty", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AWS_PROFILE", "")
			t.Setenv("AWS_ENDPOINT_URL", "")
			t.Setenv("AWS_REGION", tt.region)
			t.Setenv("AWS_DEFAULT_REGION", tt.defaultRegion)

			c := DefaultConfig()
			c.AWS.Region = tt.fileRegion
			c.ApplyEnv()
			assert.Equal(t, tt.want, c.AWS.Region)
		})
	}
}

func TestApplyFlagsRegionWinsOverEnv(t *testing.T) {
	t.Setenv("AWS_REGION", "us-west-2")
	c := DefaultConfig()
	c.ApplyEnv()
	c.ApplyFlags("", "eu-central-1", "", "", "", false)
	assert.Equal(t, "eu-central-1", c.AWS.Region)
}
