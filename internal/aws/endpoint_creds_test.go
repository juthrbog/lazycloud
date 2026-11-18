package aws

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// isolatedAWSFiles points the SDK at empty AWS files and clears the AWS
// variables that give credentials. It returns the paths of the config and
// credentials files.
func isolatedAWSFiles(t *testing.T) (cfgFile, credFile string) {
	t.Helper()
	dir := t.TempDir()
	cfgFile = filepath.Join(dir, "config")
	credFile = filepath.Join(dir, "credentials")
	t.Setenv("AWS_CONFIG_FILE", cfgFile)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", credFile)
	for _, v := range []string{
		"AWS_PROFILE", "AWS_DEFAULT_PROFILE", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN",
		"AWS_ACCESS_KEY", "AWS_SECRET_KEY", "AWS_WEB_IDENTITY_TOKEN_FILE",
		"AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_CONTAINER_CREDENTIALS_RELATIVE_URI",
	} {
		t.Setenv(v, "")
	}
	t.Setenv("AWS_REGION", "us-east-1")
	return cfgFile, credFile
}

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(data), 0o600))
}

// L4: hasCredentials finds credentials in the environment and the shared
// files, and finds none in an empty setup.
func TestHasCredentials(t *testing.T) {
	tests := []struct {
		name    string
		profile string
		env     map[string]string
		config  string
		creds   string
		want    bool
	}{
		{name: "nothing", want: false},
		{name: "keys in the environment", env: map[string]string{"AWS_ACCESS_KEY_ID": "AKID", "AWS_SECRET_ACCESS_KEY": "secret"}, want: true},
		{name: "web identity token file", env: map[string]string{"AWS_WEB_IDENTITY_TOKEN_FILE": "/tmp/token"}, want: true},
		{name: "container credentials", env: map[string]string{"AWS_CONTAINER_CREDENTIALS_RELATIVE_URI": "/creds"}, want: true},
		{name: "default profile with keys", creds: "[default]\naws_access_key_id = AKID\naws_secret_access_key = secret\n", want: true}, //nolint:gosec // fake keys for the test
		{name: "named profile with SSO", profile: "dev", config: "[profile dev]\nsso_session = corp\n\n[sso-session corp]\nsso_start_url = https://example.awsapps.com/start\nsso_region = us-east-1\n", want: true},
		{name: "named profile with a role", profile: "ops", config: "[profile ops]\nrole_arn = arn:aws:iam::123456789012:role/ops\ncredential_source = Environment\n", want: true},
		{name: "named profile with a process", profile: "proc", config: "[profile proc]\ncredential_process = /bin/true\n", want: true},
		{name: "AWS_PROFILE names the profile", env: map[string]string{"AWS_PROFILE": "proc"}, config: "[profile proc]\ncredential_process = /bin/true\n", want: true},
		{name: "profile with a region only", profile: "plain", config: "[profile plain]\nregion = eu-west-1\n", want: false},
		{name: "missing profile", profile: "nope", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfgFile, credFile := isolatedAWSFiles(t)
			writeFile(t, cfgFile, tt.config)
			writeFile(t, credFile, tt.creds)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			assert.Equal(t, tt.want, hasCredentials(context.Background(), tt.profile))
		})
	}
}

// L4: with an endpoint and no credentials, the client uses the dummy
// credentials that LocalStack accepts.
func TestEndpointWithoutCredentialsUsesDummyCredentials(t *testing.T) {
	isolatedAWSFiles(t)

	c, err := NewClient("", "", "http://127.0.0.1:4566")
	require.NoError(t, err)

	creds, err := c.Config.Credentials.Retrieve(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "test", creds.AccessKeyID)
	assert.Equal(t, "test", creds.SecretAccessKey)
}

// L4: with an endpoint, such as a VPC endpoint in AWS_ENDPOINT_URL, the
// client uses the credentials of the user.
func TestEndpointWithCredentialsUsesThem(t *testing.T) {
	isolatedAWSFiles(t)
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIDUSER")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "usersecret")

	c, err := NewClient("", "", "https://vpce.example.com")
	require.NoError(t, err)

	creds, err := c.Config.Credentials.Retrieve(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "AKIDUSER", creds.AccessKeyID)
}
