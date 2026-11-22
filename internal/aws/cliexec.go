package aws

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode"
)

// CLIExec prints a banner and then runs the AWS CLI in the terminal. It
// satisfies tea.ExecCommand, so the app and the views can pass it to
// tea.Exec. It runs SSM sessions and sign-ins.
//
// The banner is written from Go and the CLI is exec'd directly, never
// through a shell. The banner can hold a resource name, such as the Name
// tag of an instance, which anyone with ec2:CreateTags controls, so it
// must never reach a shell parser.
type CLIExec struct {
	banner string
	cmd    *exec.Cmd
	stdin  io.Reader
	// failText is printed after a failed exit, and Run then waits for
	// Enter, so the error of the CLI stays on screen. Empty means no pause.
	failText string
	// tail keeps the end of the error output of the CLI, so the caller can
	// look for a known error after the exit. nil means no copy.
	tail *tailBuffer
}

// Args returns the argv of the AWS CLI command, starting with "aws".
func (e *CLIExec) Args() []string { return e.cmd.Args }

// Banner returns the text printed before the command starts.
func (e *CLIExec) Banner() string { return e.banner }

// Env returns the environment of the command, or nil for the environment
// of the app.
func (e *CLIExec) Env() []string { return e.cmd.Env }

// SetStdin implements tea.ExecCommand.
func (e *CLIExec) SetStdin(r io.Reader) { e.stdin, e.cmd.Stdin = r, r }

// SetStdout implements tea.ExecCommand.
func (e *CLIExec) SetStdout(w io.Writer) { e.cmd.Stdout = w }

// SetStderr implements tea.ExecCommand. With a tail, the error output also
// goes to the tail, and the user still sees it.
func (e *CLIExec) SetStderr(w io.Writer) {
	if e.tail != nil {
		w = io.MultiWriter(w, e.tail)
	}
	e.cmd.Stderr = w
}

// Run prints the banner and runs the AWS CLI until it ends. After a failed
// exit that the user did not cancel, a command with a fail text prints it
// and waits for Enter.
func (e *CLIExec) Run() error {
	if e.cmd.Stdout != nil {
		_, _ = io.WriteString(e.cmd.Stdout, e.banner)
	}
	err := e.cmd.Run()
	// The CLI exited with success, but a process that it started, such as
	// a browser, still holds the error output open. See signInWaitDelay.
	if errors.Is(err, exec.ErrWaitDelay) {
		err = nil
	}
	if err != nil && e.failText != "" && !Cancelled(err) {
		if e.cmd.Stdout != nil {
			_, _ = fmt.Fprintf(e.cmd.Stdout, "\n"+e.failText+"\n", exitCode(err))
		}
		if e.stdin != nil {
			_, _ = bufio.NewReader(e.stdin).ReadString('\n')
		}
	}
	return err
}

// ErrorOutput returns the end of the error output of the CLI, up to 4 KiB,
// for a command that keeps it.
func (e *CLIExec) ErrorOutput() string {
	if e.tail == nil {
		return ""
	}
	return e.tail.String()
}

// Cancelled reports whether the user cancelled the CLI with Ctrl+C: it
// exited with code 130, or a signal ended it.
func Cancelled(err error) bool {
	exitErr, ok := errors.AsType[*exec.ExitError](err)
	if !ok {
		return false
	}
	return exitErr.ExitCode() == 130 || exitErr.ExitCode() == -1
}

// exitCode returns the exit code of the CLI, or -1 when it did not exit.
func exitCode(err error) int {
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return exitErr.ExitCode()
	}
	return -1
}

// SSMSessionCmd returns a command that starts an SSM session for the given instance.
// Requires the AWS CLI and session-manager-plugin to be installed.
// The label is displayed as a banner before the session starts (e.g. "my-server (i-abc123)").
func (c *Client) SSMSessionCmd(instanceID, label string) *CLIExec {
	args := c.sessionArgs("ssm", "start-session", "--target="+instanceID)
	// The user ends the session, so no context cancels it.
	return &CLIExec{
		banner: fmt.Sprintf("\033[1;36m── SSM Session: %s ──\033[0m\n", stripControl(label)),
		cmd:    exec.CommandContext(context.Background(), "aws", args...), //nolint:gosec // fixed binary, no shell; args are argv elements, never parsed. Keep the label out of args.
	}
}

// ECSExecCmd returns a command that opens a shell (/bin/sh) in a container
// of a task with ECS Exec. Requires the AWS CLI and session-manager-plugin
// to be installed. The label is displayed as a banner before the session
// starts. See docs/design/ecs.md, "Exec" (decision D6). When the CLI
// fails, its error stays on the screen until the user presses Enter.
func (c *Client) ECSExecCmd(cluster, task, container, label string) *CLIExec {
	args := c.sessionArgs("ecs", "execute-command", "--cluster="+cluster, "--task="+task,
		"--container="+container, "--interactive", "--command=/bin/sh")
	// The user ends the session, so no context cancels it.
	return &CLIExec{
		banner:   fmt.Sprintf("\033[1;36m── ECS Exec: %s ──\033[0m\nType exit to return.\n", stripControl(label)),
		cmd:      exec.CommandContext(context.Background(), "aws", args...), //nolint:gosec // fixed binary, no shell; args are argv elements, never parsed. Keep the label out of args.
		failText: "Exec failed (exit %d). Press Enter to return.",
	}
}

// sessionArgs adds the Region, the profile and the endpoint of the client
// to the arguments of a session command. Without the endpoint, a session
// started against LocalStack would go to AWS (H7 of docs/design/ecs.md).
// Each value is in the same argument as its flag, so a value that starts
// with "-" is not read as a flag.
func (c *Client) sessionArgs(args ...string) []string {
	if c.Region != "" {
		args = append(args, "--region="+c.Region)
	}
	if c.Profile != "" {
		args = append(args, "--profile="+c.Profile)
	}
	if c.Endpoint != "" {
		args = append(args, "--endpoint-url="+c.Endpoint)
	}
	return args
}

// signInWaitDelay limits the wait for the error output after the CLI
// exits. The tail makes the error output of the CLI a pipe, and a browser
// that the CLI starts gets the pipe too. Without a limit, Run waits until
// the user closes the browser.
const signInWaitDelay = 2 * time.Second

// signInEnvDrop are the variables that the sign-in does not pass to the
// CLI. Keys in the environment, or AWS_PROFILE, would change which
// profile the CLI signs in.
var signInEnvDrop = []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_PROFILE"}

// LoginCmd returns the command that signs in to the root of the profile:
// aws sso login for SSO, aws login for an aws login session. It returns
// nil for a profile that does not sign in.
//
// Over SSH no browser opens on the machine of the user, so the command
// asks for the device code flow (SSO) or the remote flow (aws login).
func LoginCmd(info ProfileInfo) *CLIExec {
	var args []string
	switch info.Kind {
	case KindSSO, KindSSOLegacy:
		args = []string{"sso", "login"}
		if os.Getenv("SSH_CONNECTION") != "" {
			args = append(args, "--use-device-code")
		}
	case KindLogin:
		args = []string{"login"}
		if os.Getenv("SSH_CONNECTION") != "" {
			args = append(args, "--remote")
		}
	default:
		return nil
	}
	root := info.Root
	if root == "" {
		root = info.Name
	}
	// One argv element with =, so a profile name that starts with - cannot
	// become a flag.
	args = append(args, "--profile="+root)

	cmd := exec.CommandContext(context.Background(), "aws", args...) //nolint:gosec // fixed binary, no shell; the profile is one argv element
	cmd.Env = signInEnv(os.Environ())
	cmd.WaitDelay = signInWaitDelay

	banner := fmt.Sprintf("\033[1;36m── Sign in: aws %s ──\033[0m\n", stripControl(strings.Join(args, " ")))
	if info.Account != "" {
		banner += fmt.Sprintf("Profile %s, account %s.\n", stripControl(root), stripControl(info.Account))
	}
	banner += "A browser opens. Ctrl+C cancels.\n\n"
	return &CLIExec{
		banner:   banner,
		cmd:      cmd,
		failText: "Sign-in failed (exit %d). Press Enter to return.",
		tail:     &tailBuffer{max: 4096},
	}
}

func signInEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		drop := false
		for _, d := range signInEnvDrop {
			if strings.EqualFold(name, d) {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, kv)
		}
	}
	return out
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if over := len(b.buf) - b.max; over > 0 {
		b.buf = append(b.buf[:0], b.buf[over:]...)
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

// stripControl removes control characters (including ESC) so that resource
// names cannot inject terminal escape sequences.
func stripControl(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

// AWSCLIAvailable returns true if the aws CLI is on PATH.
func AWSCLIAvailable() bool {
	_, err := exec.LookPath("aws")
	return err == nil
}
