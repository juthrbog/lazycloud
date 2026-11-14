//go:build smoke

package smoke

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/juthrbog/lazycloud/internal/app"
	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/config"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// The terminal size of every test. It is wide enough for the widest column
// set of every list.
const (
	termWidth  = 200
	termHeight = 50
)

// waitTimeout is how long a test waits for text on the screen. LocalStack
// on a CI runner can take some seconds for the first call of a service.
const waitTimeout = 30 * time.Second

// region is the region of the seed data.
const region = "us-east-1"

// endpoint returns the LocalStack URL: SMOKE_ENDPOINT, or the port that
// docker-compose.yml publishes.
func endpoint() string {
	if e := os.Getenv("SMOKE_ENDPOINT"); e != "" {
		return e
	}
	return "http://localhost:4566"
}

// requireLocalStack stops the test unless endpoint has a local host and
// answers the LocalStack health path. The tests create and delete
// resources, so they must never reach a real AWS account.
func requireLocalStack(t *testing.T, ep string) {
	t.Helper()
	u, err := url.Parse(ep)
	if err != nil {
		t.Fatalf("SMOKE_ENDPOINT %q: %v", ep, err)
	}
	switch h := u.Hostname(); h {
	case "localhost", "127.0.0.1", "::1", "localstack":
	default:
		t.Fatalf("SMOKE_ENDPOINT host %q is not local. The smoke tests run only against LocalStack.", h)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(ep, "/")+"/_localstack/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("no LocalStack at %s: %v (start it with task localstack:up)", ep, err)
	}
	defer resp.Body.Close()
	var health struct {
		Services map[string]string `json:"services"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&health) != nil || len(health.Services) == 0 {
		t.Fatalf("no LocalStack health report at %s", ep)
	}
}

// services returns AWS services for the setup and the cleanup of a test.
// They are not guarded by ReadOnly mode.
func services(t *testing.T) aws.Services {
	t.Helper()
	ep := endpoint()
	requireLocalStack(t, ep)
	isolateEnv(t)
	svc, err := aws.NewServices("", region, ep, func() bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

// isolateEnv keeps the test away from the files and the AWS settings of
// the user. A broken AWS_PROFILE in the shell must not fail the test.
func isolateEnv(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("AWS_CONFIG_FILE", home+"/aws-config")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", home+"/aws-credentials")
	t.Setenv("AWS_PROFILE", "")
}

// uniqueName returns a resource name that no other test run uses, for
// example "smoke-sqs-1731600000123". S3 and SQS accept it.
func uniqueName(kind string) string {
	return fmt.Sprintf("smoke-%s-%d", kind, time.Now().UnixMilli())
}

// screen wraps the app model and keeps the text of the last frame. The
// teatest output is a stream of changed cells, so a search in it can miss
// text that the renderer wrote in pieces. The frame text is complete.
type screen struct {
	inner tea.Model
	mu    sync.Mutex
	text  string
}

func (s *screen) Init() tea.Cmd { return s.inner.Init() }

func (s *screen) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	s.inner, cmd = s.inner.Update(m)
	return s, cmd
}

func (s *screen) View() tea.View {
	v := s.inner.View()
	s.mu.Lock()
	s.text = ansi.Strip(v.Content)
	s.mu.Unlock()
	return v
}

func (s *screen) frame() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.text
}

// harness runs the app in a test program.
type harness struct {
	t   *testing.T
	tm  *teatest.TestModel
	scr *screen
	ops interface {
		CancelAll()
		Wait(time.Duration) bool
	}
}

// start runs the app against LocalStack in ReadWrite mode. ep overrides
// the endpoint, for example with a proxy. The app quits when the test ends.
func start(t *testing.T, ep string) *harness {
	t.Helper()
	if ep == "" {
		ep = endpoint()
		requireLocalStack(t, ep)
	}
	isolateEnv(t)

	ui.SetReadOnly(false)
	t.Cleanup(func() { ui.SetReadOnly(true) })

	cfg := config.DefaultConfig()
	cfg.Display.Theme = "catppuccin" // not auto, so the theme does not switch
	cfg.AWS.Region = region
	cfg.AWS.Endpoint = ep

	root := app.New(cfg)
	scr := &screen{inner: root}
	h := &harness{
		t:   t,
		tm:  teatest.NewTestModel(t, scr, teatest.WithInitialTermSize(termWidth, termHeight)),
		scr: scr,
		ops: root.Ops(),
	}
	t.Cleanup(h.quit)
	h.waitFor("LazyCloud", "RW", ": command")
	return h
}

func (h *harness) quit() {
	if err := h.tm.Quit(); err != nil {
		h.t.Errorf("quit: %v", err)
	}
	h.tm.WaitFinished(h.t, teatest.WithFinalTimeout(5*time.Second))
	h.ops.CancelAll()
	h.ops.Wait(5 * time.Second)
}

// screenText returns the text of the last frame.
func (h *harness) screenText() string { return h.scr.frame() }

// until waits until cond is true for the frame text. On a timeout it fails
// the test and prints the last frame.
func (h *harness) until(what string, cond func(string) bool) string {
	h.t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for {
		text := h.screenText()
		if cond(text) {
			return text
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("timed out after %s waiting for %s. Last frame:\n%s", waitTimeout, what, text)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// waitFor waits until the screen shows every one of texts.
func (h *harness) waitFor(texts ...string) {
	h.t.Helper()
	h.until(fmt.Sprintf("%q", texts), func(s string) bool {
		for _, want := range texts {
			if !strings.Contains(s, want) {
				return false
			}
		}
		return true
	})
}

// waitGone waits until the screen does not show text.
func (h *harness) waitGone(text string) {
	h.t.Helper()
	h.until(fmt.Sprintf("%q to go away", text), func(s string) bool {
		return !strings.Contains(s, text)
	})
}

// keys types each rune of s as one key press.
func (h *harness) keys(s string) {
	for _, r := range s {
		h.tm.Send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func (h *harness) enter() { h.tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter}) }

// open runs a command of the command bar, for example "sqs", and waits
// until the list with that title has loaded. Keys sent during the load can
// reach another screen, for example the sort picker.
func (h *harness) open(command, title string) {
	h.t.Helper()
	h.keys(":" + command)
	h.enter()
	h.waitFor(title)
	h.waitLoaded()
}

// countRE finds the "<shown>/<total> <noun>" count in the footer of a list.
var countRE = regexp.MustCompile(`\d+/\d+ [a-zA-Z]`)

// waitLoaded waits until the list shows its count and no load runs.
func (h *harness) waitLoaded() {
	h.t.Helper()
	h.until("the list to load", func(s string) bool {
		return countRE.MatchString(s) && !strings.Contains(strings.ToLower(s), "loading")
	})
}

// filter types text into the filter of the list, closes the filter input
// with enter, and waits until every row of the table contains text. The
// list applies the filter in a command that can come after the next key,
// so a key sent too early acts on the cursor row of the unfiltered list.
func (h *harness) filter(text string) {
	h.t.Helper()
	h.keys("/" + text)
	h.enter()
	h.until(fmt.Sprintf("only rows with %q", text), func(s string) bool {
		rows := tableRows(s)
		if len(rows) == 0 {
			return false
		}
		for _, r := range rows {
			if !strings.Contains(r, text) {
				return false
			}
		}
		return true
	})
}

// tableRows returns the lines between the header separator of the table
// and its bottom border.
func tableRows(frame string) []string {
	var rows []string
	in := false
	for line := range strings.SplitSeq(frame, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "│───"):
			in = true
		case strings.HasPrefix(trimmed, "╰"):
			if in {
				return rows
			}
		case in && strings.TrimSpace(strings.Split(strings.Trim(trimmed, "│"), "││")[0]) != "":
			rows = append(rows, trimmed)
		}
	}
	return rows
}

// pick chooses option in the open action picker.
func (h *harness) pick(option string) {
	h.t.Helper()
	h.waitFor(option)
	h.keys(option)
	h.enter()
}

// formHint is in the footer while a form is open.
const formHint = "shift+tab prev"

// submitForm accepts the open form with its other fields empty. Enter goes
// to the next field, and on the last field it submits the form.
func (h *harness) submitForm() {
	h.t.Helper()
	h.waitFor(formHint)
	for range 10 {
		h.enter()
		time.Sleep(200 * time.Millisecond)
		if !strings.Contains(h.screenText(), formHint) {
			return
		}
	}
	h.t.Fatalf("the form did not close. Last frame:\n%s", h.screenText())
}

// phraseRE finds the phrase of the type-to-confirm dialog.
var phraseRE = regexp.MustCompile(`type '([^']+)' \+ enter to proceed`)

// confirm answers the open confirm dialog with yes: y for a y/n dialog, or
// the phrase that the dialog asks for. want is the resource that the
// dialog must name. A phrase other than "confirm" must be want itself, so
// the test never confirms an action on another resource.
func (h *harness) confirm(want string) {
	h.t.Helper()
	text := h.until("a confirm dialog", func(s string) bool {
		return phraseRE.MatchString(s) || strings.Contains(s, "y yes")
	})
	if !strings.Contains(text, want) {
		h.t.Fatalf("the confirm dialog does not name %q. Frame:\n%s", want, text)
	}
	if m := phraseRE.FindStringSubmatch(text); m != nil {
		if m[1] != "confirm" && m[1] != want {
			h.t.Fatalf("the confirm dialog asks for %q, not %q. Frame:\n%s", m[1], want, text)
		}
		h.keys(m[1])
		h.enter()
		return
	}
	h.keys("y")
}
