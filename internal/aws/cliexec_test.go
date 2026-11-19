package aws

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// FakeAWS puts a fake aws CLI on PATH that runs script with sh. The tests
// of the sign-in run it instead of the real CLI.
func fakeAWS(t *testing.T, script string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake aws CLI is a shell script")
	}
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "aws"), []byte("#!/bin/sh\n"+script+"\n"), 0o700)) //nolint:gosec // the test runs this script
	// The fake comes first. The system folders give sh its tools.
	t.Setenv("PATH", dir+string(os.PathListSeparator)+"/usr/bin"+string(os.PathListSeparator)+"/bin")
}

func TestLoginCmdArgs(t *testing.T) {
	t.Setenv("SSH_CONNECTION", "")
	for _, tc := range []struct {
		info ProfileInfo
		want []string
	}{
		{ProfileInfo{Name: "prod-admin", Kind: KindSSO, Root: "dev"}, []string{"aws", "sso", "login", "--profile=dev"}},
		{ProfileInfo{Name: "old", Kind: KindSSOLegacy, Root: "old"}, []string{"aws", "sso", "login", "--profile=old"}},
		{ProfileInfo{Name: "web", Kind: KindLogin, Root: "web"}, []string{"aws", "login", "--profile=web"}},
		// A name that starts with - stays one argument.
		{ProfileInfo{Name: "-x", Kind: KindSSO, Root: "--debug"}, []string{"aws", "sso", "login", "--profile=--debug"}},
		{ProfileInfo{Name: "n", Kind: KindSSO}, []string{"aws", "sso", "login", "--profile=n"}},
	} {
		c := LoginCmd(tc.info)
		require.NotNil(t, c, tc.info.Name)
		assert.Equal(t, tc.want, c.Args(), tc.info.Name)
	}
	for _, k := range []CredKind{KindKeys, KindProcess, KindWebIdentity, KindCredSource, KindDefault, KindInvalid} {
		assert.Nil(t, LoginCmd(ProfileInfo{Name: "x", Kind: k}), k.String())
	}
}

// Over SSH, the sign-in uses the flow that needs no local browser.
func TestLoginCmdOverSSH(t *testing.T) {
	t.Setenv("SSH_CONNECTION", "10.0.0.1 5000 10.0.0.2 22")

	assert.Equal(t, []string{"aws", "sso", "login", "--use-device-code", "--profile=dev"},
		LoginCmd(ProfileInfo{Kind: KindSSO, Root: "dev"}).Args())
	assert.Equal(t, []string{"aws", "login", "--remote", "--profile=web"},
		LoginCmd(ProfileInfo{Kind: KindLogin, Root: "web"}).Args())
}

// The CLI gets no keys and no AWS_PROFILE from the app, but the paths of
// the AWS files stay.
func TestLoginCmdEnv(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIA")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "s")
	t.Setenv("AWS_SESSION_TOKEN", "t")
	t.Setenv("AWS_PROFILE", "other")
	t.Setenv("AWS_CONFIG_FILE", "/tmp/cfg")
	t.Setenv("AWS_LOGIN_CACHE_DIRECTORY", "/tmp/login")

	env := strings.Join(LoginCmd(ProfileInfo{Kind: KindSSO, Root: "dev"}).Env(), "\n")

	for _, v := range []string{"AWS_ACCESS_KEY_ID=", "AWS_SECRET_ACCESS_KEY=", "AWS_SESSION_TOKEN=", "AWS_PROFILE="} {
		assert.NotContains(t, env, v)
	}
	assert.Contains(t, env, "AWS_CONFIG_FILE=/tmp/cfg")
	assert.Contains(t, env, "AWS_LOGIN_CACHE_DIRECTORY=/tmp/login")
}

func TestLoginCmdBanner(t *testing.T) {
	c := LoginCmd(ProfileInfo{Kind: KindSSO, Root: "dev\x1b]52;c;aGk=\x07", Account: "111111111111"})

	assert.Contains(t, c.Banner(), "Sign in: aws sso login --profile=dev")
	assert.Contains(t, c.Banner(), "account 111111111111")
	assert.Contains(t, c.Banner(), "A browser opens. Ctrl+C cancels.")
	assert.NotContains(t, c.Banner(), "\x1b]52", "control characters are stripped")
	assert.NotContains(t, LoginCmd(ProfileInfo{Kind: KindLogin, Root: "web"}).Banner(), "account")
}

// runLogin runs a sign-in against the fake CLI with an empty stdin, and
// returns its output, the command, and its error.
func runLogin(t *testing.T) (string, *CLIExec, error) {
	t.Helper()
	c := LoginCmd(ProfileInfo{Kind: KindLogin, Root: "web"})
	var out, errOut bytes.Buffer
	c.SetStdin(strings.NewReader(""))
	c.SetStdout(&out)
	c.SetStderr(&errOut)
	err := c.Run()
	return out.String() + errOut.String(), c, err
}

func TestSignInThatWorks(t *testing.T) {
	fakeAWS(t, `echo "signed in $*"`)

	out, c, err := runLogin(t)

	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(out, c.Banner()), "the banner comes first")
	assert.Contains(t, out, "signed in login --profile=web")
	assert.NotContains(t, out, "Press Enter")
}

// A browser that the CLI starts can keep the error output open after the
// CLI exits. The sign-in still ends soon after the CLI, with no error.
func TestSignInDoesNotWaitForAProcessThatTheCLIStarted(t *testing.T) {
	fakeAWS(t, `sleep 30 &
echo "signed in"`)
	start := time.Now()

	out, _, err := runLogin(t)

	require.NoError(t, err)
	assert.Contains(t, out, "signed in")
	assert.Less(t, time.Since(start), 10*time.Second)
}

// A failed sign-in keeps the error of the CLI on screen until Enter, and
// keeps the end of the error output for the app.
func TestSignInThatFails(t *testing.T) {
	fakeAWS(t, `echo "aws: error: argument command: Invalid choice, valid choices are: invalid choice" >&2; exit 2`)

	// A file, so the CLI gets the file itself and os/exec does not read
	// it. Only the pause reads the Enter.
	in, err := os.CreateTemp(t.TempDir(), "stdin")
	require.NoError(t, err)
	t.Cleanup(func() { _ = in.Close() })
	_, err = in.WriteString("\n")
	require.NoError(t, err)
	_, err = in.Seek(0, io.SeekStart)
	require.NoError(t, err)
	c := LoginCmd(ProfileInfo{Kind: KindLogin, Root: "web"})
	var stdout, stderr bytes.Buffer
	c.SetStdin(in)
	c.SetStdout(&stdout)
	c.SetStderr(&stderr)
	err = c.Run()
	out := stdout.String() + stderr.String()

	require.Error(t, err)
	assert.False(t, Cancelled(err))
	pos, _ := in.Seek(0, io.SeekCurrent)
	assert.Equal(t, int64(1), pos, "the pause reads the Enter")
	assert.Contains(t, out, "Sign-in failed (exit 2). Press Enter to return.")
	assert.Contains(t, out, "invalid choice", "the user sees the error")
	assert.Contains(t, c.ErrorOutput(), "invalid choice", "the app can read it")
}

// A cancel with Ctrl+C returns at once, with no pause.
func TestSignInThatIsCancelled(t *testing.T) {
	fakeAWS(t, `exit 130`)

	out, _, err := runLogin(t)

	require.Error(t, err)
	assert.True(t, Cancelled(err))
	assert.NotContains(t, out, "Press Enter")
}

// A signal also counts as a cancel.
func TestSignInEndedByASignal(t *testing.T) {
	fakeAWS(t, `kill -INT $$`)

	_, _, err := runLogin(t)

	require.Error(t, err)
	assert.True(t, Cancelled(err))
}

func TestCancelledNeedsAnExit(t *testing.T) {
	assert.False(t, Cancelled(nil))
	assert.False(t, Cancelled(errors.New("exec: not found")))
}

// The SSM session keeps no error output and does not pause.
func TestSSMSessionDoesNotPause(t *testing.T) {
	fakeAWS(t, `echo boom >&2; exit 1`)
	c := (&Client{}).SSMSessionCmd("i-1", "web")
	var out bytes.Buffer
	c.SetStdout(&out)
	c.SetStderr(&out)

	require.Error(t, c.Run())
	assert.Equal(t, c.Banner()+"boom\n", out.String(), "nothing after the output of the CLI")
	assert.Empty(t, c.ErrorOutput())
}

func TestTailBufferKeepsTheEnd(t *testing.T) {
	one := &tailBuffer{max: 5}
	_, _ = one.Write([]byte("abcdef"))
	assert.Equal(t, "bcdef", one.String(), "one byte over")

	b := &tailBuffer{max: 5}
	_, _ = b.Write([]byte("abc"))
	_, _ = b.Write([]byte("defg"))
	assert.Equal(t, "cdefg", b.String())
	n, err := b.Write([]byte("hijklmno"))
	require.NoError(t, err)
	assert.Equal(t, 8, n)
	assert.Equal(t, "klmno", b.String())
}
