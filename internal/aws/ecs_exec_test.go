package aws

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// When the CLI of ECS Exec fails, its error stays on the screen: Run prints
// the exit code and waits for Enter. A success returns at once
// (docs/design/ecs.md, PR 6).
func TestECSExecRunWaitsAfterAFailure(t *testing.T) {
	e := (&Client{}).ECSExecCmd("c", "t", "app", "app")
	// Stand in for the aws CLI.
	e.cmd = exec.CommandContext(t.Context(), "sh", "-c", "echo TargetNotConnectedException >&2; exit 254")
	var out, errOut bytes.Buffer
	e.SetStdout(&out)
	e.SetStderr(&errOut)
	e.SetStdin(strings.NewReader("\n"))
	require.Error(t, e.Run())
	assert.True(t, strings.HasPrefix(out.String(), e.Banner()))
	assert.Contains(t, out.String(), "Exec failed (exit 254). Press Enter to return.")
	assert.Contains(t, errOut.String(), "TargetNotConnectedException", "the user sees the error of the CLI")

	ok := (&Client{}).ECSExecCmd("c", "t", "app", "app")
	ok.cmd = exec.CommandContext(t.Context(), "sh", "-c", "exit 0")
	var okOut bytes.Buffer
	ok.SetStdout(&okOut)
	ok.SetStdin(strings.NewReader("\n"))
	require.NoError(t, ok.Run())
	assert.NotContains(t, okOut.String(), "Exec failed")
}
