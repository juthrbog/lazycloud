package aws

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSSMSessionCmd(t *testing.T) {
	c := &Client{Region: "us-west-2", Profile: "staging"}
	s := c.SSMSessionCmd("i-abc123", "my-server (i-abc123)")

	assert.Equal(t, []string{
		"aws", "ssm", "start-session", "--target", "i-abc123",
		"--region", "us-west-2", "--profile", "staging",
	}, s.Args())
	assert.Contains(t, s.Banner(), "SSM Session: my-server (i-abc123)")
}

func TestSSMSessionCmdMinimal(t *testing.T) {
	c := &Client{}
	s := c.SSMSessionCmd("i-xyz789", "i-xyz789")

	assert.Equal(t, []string{"aws", "ssm", "start-session", "--target", "i-xyz789"}, s.Args())
}

// A Name tag is attacker-controlled. It must stay inert text in the banner and
// never reach a shell or the CLI argv.
func TestSSMSessionCmdHostileLabel(t *testing.T) {
	c := &Client{}
	hostile := `web' ; touch /tmp/pwned ; echo '%s`
	osc52 := "\x1b]52;c;aGk=\x07" // clipboard write escape sequence
	s := c.SSMSessionCmd("i-abc123", hostile+osc52)

	assert.Equal(t, []string{"aws", "ssm", "start-session", "--target", "i-abc123"}, s.Args())
	assert.Contains(t, s.Banner(), hostile, "label is printed verbatim")
	assert.NotContains(t, s.Banner(), "\x1b]52", "control characters are stripped")
	assert.NotContains(t, s.Banner(), "\x07")
}

func TestSSMSessionRunPrintsBannerFirst(t *testing.T) {
	s := (&Client{}).SSMSessionCmd("i-abc123", "my-server")
	// Stand in for the aws CLI: re-run this test binary with no tests selected.
	s.cmd = exec.Command(os.Args[0], "-test.run=^$") //nolint:gosec // re-runs the test binary itself

	var out bytes.Buffer
	s.SetStdout(&out)
	require.NoError(t, s.Run())
	assert.True(t, strings.HasPrefix(out.String(), s.Banner()))
}
