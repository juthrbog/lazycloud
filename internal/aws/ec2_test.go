package aws

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSSMSessionCmd(t *testing.T) {
	c := &Client{Region: "us-west-2", Profile: "staging"}
	cmd := c.SSMSessionCmd("i-abc123", "my-server (i-abc123)")

	assert.Equal(t, "sh", cmd.Path[len(cmd.Path)-2:])
	// The shell command should contain the banner and aws ssm command
	shellArg := cmd.Args[2] // sh -c "<shell command>"
	assert.Contains(t, shellArg, "SSM Session: my-server (i-abc123)")
	assert.Contains(t, shellArg, "aws ssm start-session --target i-abc123")
	assert.Contains(t, shellArg, "--region us-west-2")
	assert.Contains(t, shellArg, "--profile staging")
}

func TestSSMSessionCmdMinimal(t *testing.T) {
	c := &Client{}
	cmd := c.SSMSessionCmd("i-xyz789", "i-xyz789")

	shellArg := cmd.Args[2]
	assert.Contains(t, shellArg, "--target i-xyz789")
	assert.NotContains(t, shellArg, "--region")
	assert.NotContains(t, shellArg, "--profile")
}
