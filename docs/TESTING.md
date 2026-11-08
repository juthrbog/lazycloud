# Testing Guidelines

## Stack

- **Test framework:** Go `testing` package + [testify](https://github.com/stretchr/testify) (`assert`, `require`, `mock`)
- **TUI integration:** [teatest v2](https://github.com/charmbracelet/x/tree/main/exp/teatest) (`charmbracelet/x/exp/teatest/v2`)
- **AWS mocking:** Hand-written mocks in `internal/aws/awstest/` implementing service interfaces
- **LocalStack:** Docker-based AWS emulator for manual checks of the app (see `DEVELOPMENT.md`). The project has no automated integration tests.

## Three-Tier Testing Pyramid

### Tier 1: Update Unit Tests (bulk of tests)

Test the `Update()` method directly by sending messages and asserting state changes. This is the most effective approach for Elm Architecture — the model is a pure state machine.

**Pattern:**
```go
func TestEC2List_ManageRunningInstance(t *testing.T) {
    view, _ := newTestEC2List()
    loadInstances(view, []aws.Instance{testRunningInstance})

    _, cmd := view.Update(keyPress('m'))
    require.NotNil(t, cmd)

    result := cmd()
    picker, ok := result.(msg.RequestActionPickerMsg)
    require.True(t, ok)
    assert.Equal(t, []string{"Stop", "Reboot", "Terminate"}, picker.Options)
}
```

**When to use:** Always the default. Every view's key handling, message handling, and state transitions should be tested this way.

**Examples:** `internal/views/ec2_list_test.go`, `internal/views/s3_list_test.go`, `internal/app/app_test.go`

### Tier 2: Command Chain Tests

Execute the `tea.Cmd` returned by `Update()`, type-assert the resulting `tea.Msg`, and feed it back through `Update()` to test multi-step flows.

**Pattern:**
```go
func TestFeaturePickerResultNavigatesToView(t *testing.T) {
    m := newTestModel(140, 40)

    // Simulate picker selection
    result, cmd := m.Update(ui.PickerResultMsg{ID: "feature", Selected: 1, Value: "ami_list"})
    m = result.(Model)
    require.NotNil(t, cmd)

    // Execute the NavigateMsg cmd and feed back
    navMsg := cmd()
    result, _ = m.Update(navMsg)
    m = result.(Model)

    assert.Equal(t, "AMIs", m.nav.Current().Title())
}
```

**When to use:** For flows that span multiple Update cycles — navigation, async data loading, toast dismiss chains, command bar execution.

**Background operations:** a command that starts a long operation returns `msg.StartOpMsg`, not the result. In app tests, `runOp(t, m, cmd)` delivers it as the runtime does and returns the command that runs the operation. In view tests, `collectMsgs` runs the operation at once.

**Contexts:** to test that a call stops or goes on when the user leaves a view, give the mock a `.Run` that records `a.Get(0).(context.Context)`. For a call that must stop, block on `ctx.Done()` and run the command in its own goroutine, because `collectMsgs` in `internal/app` gives up after 300 ms. See `internal/app/ops_test.go`.

**Examples:** `internal/app/app_test.go` (`TestFeaturePickerResultNavigatesToView`, `TestCommandBarResultNavigates`)

### Tier 3: teatest E2E Tests

Run a full `tea.Program` with a virtual terminal. Use for smoke-testing that views render correctly and respond to real key sequences.

**Pattern:**
```go
func TestTeatest_S3ListLoadsBuckets(t *testing.T) {
    mockS3 := new(awstest.MockS3Service)
    mockS3.On("ListBuckets", mock.Anything).Return(testBuckets, nil)

    view := views.NewS3List(mockS3, "us-east-1")
    tm := teatest.NewTestModel(t, view,
        teatest.WithInitialTermSize(80, 24),
    )

    teatest.WaitFor(t, tm.Output(), func(bts []byte) bool {
        return strings.Contains(string(bts), "my-bucket")
    }, teatest.WithDuration(2*time.Second))

    tm.Quit()
    tm.WaitFinished(t, teatest.WithFinalTimeout(2*time.Second))
}
```

**When to use:** Sparingly. Good for verifying that views load and render basic content. Not for testing every key binding or state transition (Tier 1 is better for that).

**Limitations:**
- Experimental API (may change between versions)
- Slower than direct Update calls (~50-277ms vs sub-ms)
- `WaitFor` is timing-sensitive — use generous timeouts
- No intermediate model inspection — can only assert on rendered output
- Opaque failures — hard to debug when assertions on terminal bytes fail

**Examples:** `internal/views/s3_teatest_test.go`

## AWS Mock Pattern

### Service Interfaces

Every AWS service defines an interface in `internal/aws/`:

```go
// internal/aws/ec2.go
type EC2Service interface {
    ListInstances(ctx context.Context) ([]Instance, error)
    GetInstanceDetail(ctx context.Context, instanceID string) (*InstanceDetail, error)
    StartInstance(ctx context.Context, instanceID string) error
    // ...
}
```

### Hand-Written Mocks

Mocks in `internal/aws/awstest/` use testify/mock:

```go
// internal/aws/awstest/mock_ec2.go
type MockEC2Service struct {
    mock.Mock
}

var _ aws.EC2Service = (*MockEC2Service)(nil) // compile-time check

func (m *MockEC2Service) ListInstances(ctx context.Context) ([]aws.Instance, error) {
    args := m.Called(ctx)
    return args.Get(0).([]aws.Instance), args.Error(1)
}
```

### Usage in Tests

```go
mockSvc := new(awstest.MockEC2Service)
mockSvc.On("ListInstancesPage", mock.Anything, (*string)(nil)).
    Return(&aws.InstancePage{Instances: []aws.Instance{testInstance}}, nil)

view := views.NewEC2List(mockSvc, nil)
// ... test code ...

mockSvc.AssertExpectations(t) // verify all expected calls were made
```

### When to Use Generated Mocks

Use hand-written mocks. The EC2 interface has 21 methods, S3 has 18, and SQS has 13. Each mock method is a few lines that call `m.Called`. If a mock becomes hard to keep in step with its interface, consider [mockery](https://github.com/vektra/mockery).

## Test Helper Conventions

### View Test Constructors

Every view test file defines a constructor that returns the view and its mock:

```go
func newTestEC2List() (*EC2List, *awstest.MockEC2Service) {
    m := new(awstest.MockEC2Service)
    view := NewEC2List(m, nil)
    view.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
    return view, m
}
```

Always send a `WindowSizeMsg` after creation — views need dimensions to function.

### Data Loaders

Simulate async data loading by sending the loaded message directly:

```go
func loadInstances(view *EC2List, instances []aws.Instance) {
    view.Update(ec2PageLoadedMsg{instances: instances, hasMorePages: false, pageNum: 1})
}
```

This skips the async `tea.Cmd` and puts the view directly into the loaded state.

### Key Press Helper

```go
func keyPress(r rune) tea.KeyPressMsg {
    return tea.KeyPressMsg{Code: r, Text: string(r)}
}
```

Defined in each view test file that needs it. Could be extracted to a shared test helper if the pattern spreads.

### UI Test Init

UI component tests must initialize styles:

```go
func init() {
    RebuildStyles()
}
```

Without this, style rendering produces zero-width output.

## What to Test vs. What to Skip

### Always Test

- **Key binding dispatch** — press key, assert correct message/state change
- **Mode-aware behavior** — verify `ModeReadWrite` hints are present with correct mode, verify mutations blocked in ReadOnly
- **Navigation flows** — push/pop, breadcrumbs, cross-resource
- **Error handling** — API errors surface as error state or toast, not panic
- **Registry consistency** — every nav command has a view factory entry (`TestRegistryNavCommandsCoveredByResolveView`)
- **Theme completeness** — every theme defines all required fields (`TestAllThemesHaveRequiredFields`)

### Skip or Defer

- **Rendered ANSI output** — asserting on exact terminal bytes is brittle and breaks on theme/style changes. Test state instead.
- **Visual layout** — use manual testing or teatest smoke tests, not unit tests
- **Third-party component internals** — don't test that bubbles/table sorts correctly; test that your wrapper calls Sort with the right column

## Coverage Priorities

Focus test coverage where bugs are most costly:

1. **Views** (`internal/views/`) — user-facing behavior, key bindings, state machine. Highest priority.
2. **App** (`internal/app/`) — message routing, navigation, panel management. High priority.
3. **UI components** (`internal/ui/`) — reusable components with significant logic (table, command bar, help overlay). Medium priority.
4. **Registry** (`internal/registry/`) — consistency checks. Low effort, high value.
5. **AWS layer** (`internal/aws/`) — test utility functions; SDK calls tested via mocks in view tests. Low priority for direct tests.
6. **Config/EventLog** — low risk, low priority.

## Current Coverage Gaps

### Views Missing Tests

| File | Risk | Recommendation |
|------|------|---------------|
| `views/home.go` | Low | Service selection is tested indirectly via app_test; add direct Enter/filter tests |
| `views/content.go` | Low | Thin wrapper around ContentView; tested indirectly |
| `views/eventlog.go` | Medium | Log viewing/filtering untested; add severity filter and auto-scroll tests |

### UI Components Missing Tests

| File | Risk | Recommendation |
|------|------|---------------|
| `ui/picker.go` | Medium | Fuzzy search ranking, selection, cancel — add unit tests |
| `ui/confirm.go` | Medium | Type-to-confirm logic — add unit tests |
| `ui/contentview.go` | Medium | Visual mode, yank, cursor movement — add unit tests |
| `ui/filter.go` | Low | Simple text input; tested indirectly by views |
| `ui/toast.go` | Low | Auto-dismiss logic; tested indirectly by app_test |
| `ui/header.go` | Low | Rendering only; visual verification |

### Infrastructure Gaps

| Gap | Recommendation |
|-----|---------------|
| No LocalStack integration tests | Add smoke tests for S3 and EC2 behind a build tag (T10 in the review tracker) |
| No golden file tests | Consider for rendered output regression after theme changes |
| `internal/config/` untested | Add config loading/precedence tests |

## CI Considerations

- **Headless testing:** All tests run without a real terminal. teatest uses a virtual terminal internally. No `TERM` configuration needed.
- **Timeouts:** teatest `WaitFor` uses real wall-clock time. Use generous timeouts (2-5s) in CI where runners may be slow.
- **Race detection and test order:** CI runs `go test -race -shuffle=on ./...` on every pull request. Run `task test:race` before you push. The race detector finds data races between `Update` and command goroutines. The random order finds tests that depend on global state, such as the ReadOnly mode, the theme, or the event log, which keeps only its last 500 entries.
- **No real AWS:** App tests build a client for a closed local port (`http://127.0.0.1:1`) with dummy credentials. A test that sets an AWS profile points `AWS_CONFIG_FILE` at a temporary file. No test reads `~/.aws` or reaches an AWS account.
