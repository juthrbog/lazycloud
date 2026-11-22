package views

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// Tests for the Lambda function list (docs/design/lambda.md, PR 2).

var testFunction = aws.Function{
	Name: "orders-api", ARN: "arn:aws:lambda:us-east-1:1:function:orders-api",
	Runtime: "python3.12", Handler: "index.handler", PackageType: "Zip",
	Architectures: []string{"x86_64"}, MemoryMB: 256, TimeoutSec: 30, CodeSize: 2048,
	LastModified: time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC),
	LogGroup:     "/aws/lambda/orders-api", LogFormat: "Text",
}

var testImageFunction = aws.Function{
	Name: "legacy-image", ARN: "arn:aws:lambda:us-east-1:1:function:legacy-image",
	PackageType: "Image", MemoryMB: 128, TimeoutSec: 3,
	LogGroup: "/custom/legacy",
}

// fakeLambda is an aws.LambdaService for the function list.
type fakeLambda struct {
	aws.LambdaService

	detail  *aws.FunctionDetail
	getErr  error
	url     *aws.FunctionURL
	urlErr  error
	gets    []string // name:qualifier of each GetFunction
	listErr error
	pages   [][]aws.Function
}

func (f *fakeLambda) ListFunctionsPage(_ context.Context, tok *string) (*aws.FunctionPage, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	i := 0
	if tok != nil {
		i = int((*tok)[0] - '0')
	}
	p := &aws.FunctionPage{Functions: f.pages[i]}
	if i+1 < len(f.pages) {
		next := string(rune('0' + i + 1))
		p.Next = &next
	}
	return p, nil
}

func (f *fakeLambda) GetFunction(_ context.Context, name, qualifier string) (*aws.FunctionDetail, error) {
	f.gets = append(f.gets, name+":"+qualifier)
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.detail, nil
}

func (f *fakeLambda) GetFunctionURL(context.Context, string) (*aws.FunctionURL, error) {
	return f.url, f.urlErr
}

func testDetail() *aws.FunctionDetail {
	conc := int32(5)
	return &aws.FunctionDetail{
		Function: testFunction, State: "Active", StateReason: "ready", LastUpdateStatus: "Successful",
		Role: "arn:aws:iam::1:role/r", CodeSHA256: "abc", TracingMode: "PassThrough", EphemeralMB: 512,
		Env:   map[string]string{"DB_PASSWORD": "hunter2", "DB_HOST": "db.internal"},
		VPCID: "vpc-1", SubnetIDs: []string{"subnet-1", "subnet-2"}, SecurityGroupIDs: []string{"sg-1"},
		Layers:              []aws.Layer{{ARN: "arn:aws:lambda:us-east-1:1:layer:deps:3"}},
		ReservedConcurrency: &conc,
		Tags:                map[string]string{"team": "orders"},
	}
}

// newFunctions builds the list with its first load done.
func newFunctions(t *testing.T, f *fakeLambda, width int) *LambdaFunctions {
	t.Helper()
	v := NewLambdaFunctions(f)
	v.SetContext(t.Context())
	v.Update(tea.WindowSizeMsg{Width: width, Height: 30})
	pump(v, v.Init(), 10)
	return v
}

func TestLambdaFunctions_Rows(t *testing.T) {
	f := &fakeLambda{pages: [][]aws.Function{{testFunction, testImageFunction}}}
	v := newFunctions(t, f, 160)
	out := plain(v.View().Content)
	for _, s := range []string{"orders-api", "python3.12", "256 MB", "30s", "2.0 KB", "x86_64", "zip", "2026-11-20", "image"} {
		assert.Contains(t, out, s)
	}
	assert.Equal(t, "2/2 functions", v.Footer())

	row := lambdaRow(testFunction, ui.TierWide)
	assert.Equal(t, testFunction.ARN, row.Key, "the key is the ARN")
	assert.Equal(t, ui.SortKeyInt(256), row.Sort[2], "memory sorts as a number")
	assert.Equal(t, ui.SortKeyInt(30), row.Sort[3])
	assert.Equal(t, ui.SortKeyBytes(2048), row.Sort[4])
	assert.Equal(t, "image", lambdaRow(testImageFunction, ui.TierNarrow).Cells[1], "an image function has no runtime")
	assert.Len(t, lambdaRow(testFunction, ui.TierMedium).Cells, 6)
	assert.Len(t, lambdaRow(testFunction, ui.TierNarrow).Cells, 3)
	assert.Empty(t, lambdaRow(aws.Function{Name: "x"}, ui.TierNarrow).Cells[2], "no date for a zero time")
}

// The longest common runtime shows in full at every tier.
func TestLambdaFunctions_LongRuntimeFits(t *testing.T) {
	custom := testFunction
	custom.Name, custom.Runtime = "go-fn", "provided.al2023"
	for _, w := range []int{60, 100, 160} {
		v := newFunctions(t, &fakeLambda{pages: [][]aws.Function{{custom}}}, w)
		assert.Contains(t, plain(v.View().Content), "provided.al2023", "width %d", w)
	}
}

// Each row fits the window at every tier.
func TestLambdaFunctions_FitsTheWindow(t *testing.T) {
	long := testFunction
	long.Name = "a-function-name-that-is-very-long-and-goes-on-for-a-while-1234567"
	for _, w := range []int{40, 60, 80, 100, 120, 160, 200} {
		v := newFunctions(t, &fakeLambda{pages: [][]aws.Function{{long, testImageFunction}}}, w)
		for row := range strings.SplitSeq(v.View().Content, "\n") {
			assert.LessOrEqual(t, ansi.StringWidth(row), w, "width %d: %q", w, plain(row))
		}
	}
}

// Enter opens the log group in the event viewer: the custom group of the
// function, or /aws/lambda/<name> (decision D4).
func TestLambdaFunctions_EnterOpensTheLogs(t *testing.T) {
	f := &fakeLambda{pages: [][]aws.Function{{testImageFunction, {Name: "plain", ARN: "arn:plain"}}}}
	v := newFunctions(t, f, 160)
	_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Equal(t, []msg.NavigateMsg{{ViewID: "logs_events", Params: map[string]string{"log_group": "/custom/legacy", "missing_hint": lambdaMissingHint}}},
		navigations(collectMsgs(cmd)))
	v.Update(keyPress('j'))
	_, cmd = v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Equal(t, []msg.NavigateMsg{{ViewID: "logs_events", Params: map[string]string{"log_group": "/aws/lambda/plain", "missing_hint": lambdaMissingHint}}},
		navigations(collectMsgs(cmd)))
}

// d opens the detail: Info, Environment with hidden values, Network with
// links, and Tags. It calls GetFunction for the function, not a version.
func TestLambdaFunctions_Detail(t *testing.T) {
	f := &fakeLambda{pages: [][]aws.Function{{testFunction}}, detail: testDetail(),
		url: &aws.FunctionURL{URL: "https://x.lambda-url.on.aws/", AuthType: "NONE"}}
	v := newFunctions(t, f, 160)
	_, cmd := v.Update(keyPress('d'))
	panel := detailPanel(t, v, cmd)
	assert.Equal(t, []string{"orders-api:"}, f.gets)
	assert.Equal(t, "orders-api", panel.PanelTitle)
	require.Len(t, panel.Tabs, 4)
	assert.Equal(t, []string{"Info", "Environment", "Network", "Tags"}, tabTitles(panel))

	info := panel.Tabs[0].Content
	for _, s := range []string{"State            Active (ready)", "Last update      Successful", "python3.12", "index.handler",
		"256 MB", "30 s", "512 MB", "5 reserved", "arn:aws:iam::1:role/r", "/aws/lambda/orders-api (Text)",
		"https://x.lambda-url.on.aws/ (auth NONE)", "PassThrough", "deps:3", "abc"} {
		assert.Contains(t, info, s)
	}

	env := panel.Tabs[1].Content
	assert.Contains(t, env, "Values are hidden")
	assert.Contains(t, env, "DB_HOST")
	assert.Contains(t, env, "DB_PASSWORD")
	assert.NotContains(t, env, "hunter2", "the values are hidden (decision D5)")
	assert.NotContains(t, env, "db.internal")
	assert.Contains(t, env, lambdaHidden)

	net := panel.Tabs[2]
	assert.Contains(t, net.Content, "vpc-1")
	assert.Equal(t, []msg.TabLink{
		{Line: 0, ViewID: "vpc_list", Params: map[string]string{"focus": "vpc-1"}},
		{Line: 1, ViewID: "subnet_list", Params: map[string]string{"focus": "subnet-1"}},
		{Line: 2, ViewID: "subnet_list", Params: map[string]string{"focus": "subnet-2"}},
		{Line: 3, ViewID: "sg_list", Params: map[string]string{"focus": "sg-1"}},
	}, net.Links)
	assert.Contains(t, panel.Tabs[3].Content, "team")
}

func tabTitles(p msg.TabbedContentMsg) []string {
	var out []string
	for _, t := range p.Tabs {
		out = append(out, t.Title)
	}
	return out
}

// $ opens the detail with the values shown and the Environment tab first,
// so a narrow terminal, which shows only the first tab, shows them.
func TestLambdaFunctions_DollarShowsTheValues(t *testing.T) {
	f := &fakeLambda{pages: [][]aws.Function{{testFunction}}, detail: testDetail()}
	v := newFunctions(t, f, 160)
	_, cmd := v.Update(keyPress('$'))
	var panel msg.TabbedContentMsg
	for _, m := range collectMsgs(cmd) {
		if p, ok := m.(msg.TabbedContentMsg); ok {
			panel = p
		}
	}
	require.NotEmpty(t, panel.Tabs)
	assert.Equal(t, "Environment", panel.Tabs[0].Title)
	assert.Equal(t, "Info", panel.Tabs[1].Title)
	assert.Contains(t, panel.Tabs[0].Content, "hunter2")
	assert.NotContains(t, panel.Tabs[0].Content, "Values are hidden")

	f.getErr = errors.New("AccessDeniedException: no")
	_, cmd = v.Update(keyPress('$'))
	ts := toasts(collectMsgs(cmd))
	require.Len(t, ts, 1)
	assert.Contains(t, ts[0].Text, "Details failed")
}

// A failure of GetFunctionURL shows on its line, and the panel opens. No
// URL, no VPC, no tags and no environment show as such.
func TestLambdaFunctions_DetailEdges(t *testing.T) {
	d := testDetail()
	d.VPCID, d.SubnetIDs, d.SecurityGroupIDs = "", nil, nil
	d.Env, d.Tags, d.TagsError = nil, nil, "AccessDeniedException: no lambda:ListTags"
	d.EnvError = "KMSAccessDenied no key"
	f := &fakeLambda{pages: [][]aws.Function{{testFunction}}, detail: d, urlErr: errors.New("TooManyRequestsException: slow down")}
	v := newFunctions(t, f, 160)
	_, cmd := v.Update(keyPress('d'))
	panel := detailPanel(t, v, cmd)
	assert.Contains(t, panel.Tabs[0].Content, "Function URL     failed:")
	assert.Contains(t, panel.Tabs[1].Content, "No environment variables")
	assert.Contains(t, panel.Tabs[1].Content, "Environment error: KMSAccessDenied no key")
	assert.Contains(t, panel.Tabs[2].Content, "No VPC")
	assert.Empty(t, panel.Tabs[2].Links)
	require.Len(t, panel.Tabs, 4)
	assert.Contains(t, panel.Tabs[3].Content, "Tags failed: AccessDeniedException")

	f.urlErr, f.url = nil, nil
	d.TagsError = ""
	_, cmd = v.Update(keyPress('d'))
	panel = detailPanel(t, v, cmd)
	assert.Contains(t, panel.Tabs[0].Content, "Function URL     none")
	assert.Len(t, panel.Tabs, 3, "no Tags tab without tags")

	_, err := lambdaDetail(context.Background(), nil, testFunction, "", false)
	assert.ErrorIs(t, err, errNoClient)
}

// :lambda NAME with a name that the loaded pages do not have opens its
// detail with GetFunction, with no row (jump D5, H4). An ARN with a version
// opens the detail of that version.
func TestLambdaFunctions_FocusMissing(t *testing.T) {
	// A name past the page budget opens its detail with GetFunction, with no
	// row (jump D5, H4).
	f := &fakeLambda{pages: [][]aws.Function{{testFunction}}, detail: testDetail()}
	v := newFunctions(t, f, 160)
	_, cmd := v.Update(msg.FocusResourceMsg{ResourceID: "billing"})
	assert.NotEmpty(t, detailPanel(t, v, cmd).Tabs)
	assert.Equal(t, []string{"billing:"}, f.gets)
	assert.Len(t, v.items, 1, "no row is added")

	// An ARN asks for the name in it.
	f.gets = nil
	_, cmd = v.Update(msg.FocusResourceMsg{ResourceID: "arn:aws:lambda:us-east-1:123456789012:function:billing"})
	detailPanel(t, v, cmd)
	assert.Equal(t, []string{"billing:"}, f.gets)

	// A version ARN opens that version, also when the function is loaded.
	f.gets = nil
	_, cmd = v.Update(msg.FocusResourceMsg{ResourceID: "arn:aws:lambda:us-east-1:123456789012:function:orders-api:3"})
	assert.Equal(t, "orders-api:3", detailPanel(t, v, cmd).PanelTitle)
	assert.Equal(t, []string{"orders-api:3"}, f.gets)

	// A function that does not exist gives the detail error toast.
	f.getErr = errors.New("ResourceNotFoundException: Function not found")
	_, cmd = v.Update(msg.FocusResourceMsg{ResourceID: "nope"})
	ts := toasts(pump(v, cmd, 3))
	require.Len(t, ts, 1)
	assert.Equal(t, "Details failed: ResourceNotFoundException: Function not found", ts[0].Text)

	// A loaded name opens its detail.
	f.getErr = nil
	_, cmd = v.Update(msg.FocusResourceMsg{ResourceID: "orders-api"})
	assert.NotEmpty(t, detailPanel(t, v, cmd).Tabs)
}

// The list stops after 20 pages (1,000 functions), and l loads more
// (decision D3).
func TestLambdaFunctions_PageBudget(t *testing.T) {
	pages := make([][]aws.Function, lambdaPageBudget+1)
	for i := range pages {
		pages[i] = []aws.Function{{Name: "fn-" + strconv.Itoa(i), ARN: "arn:" + strconv.Itoa(i)}}
	}
	v := NewLambdaFunctions(&fakeLambda{pages: pages})
	v.SetContext(t.Context())
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	pump(v, v.Init(), 3*lambdaPageBudget)
	assert.Len(t, v.items, lambdaPageBudget)
	assert.Contains(t, v.Footer(), "l for more")
	_, cmd := v.Update(keyPress('l'))
	require.NotNil(t, cmd)
	pump(v, cmd, 10)
	assert.Len(t, v.items, lambdaPageBudget+1)
	assert.NotContains(t, v.Footer(), "l for more")
}

// y copies the ARN.
func TestLambdaFunctions_CopyARN(t *testing.T) {
	v := newFunctions(t, &fakeLambda{pages: [][]aws.Function{{testFunction}}}, 160)
	_, cmd := v.Update(keyPress('y'))
	ts := toasts(collectMsgs(cmd))
	require.NotEmpty(t, ts)
	assert.Contains(t, ts[0].Text, testFunction.ARN)
}

// The view keys do nothing on the error screen and while the filter is
// open.
func TestLambdaFunctions_KeysWait(t *testing.T) {
	// The error screen of a failed reload hides the loaded rows.
	v := newFunctions(t, &fakeLambda{pages: [][]aws.Function{{testFunction}}, detail: testDetail()}, 160)
	v.Update(msg.ErrorMsg{Err: errors.New("throttled")})
	require.Error(t, v.err)
	for _, k := range []tea.KeyPressMsg{{Code: tea.KeyEnter}, keyPress('$')} {
		_, cmd := v.Update(k)
		for _, m := range collectMsgs(cmd) {
			_, isPanel := m.(msg.TabbedContentMsg)
			_, isNav := m.(msg.NavigateMsg)
			assert.False(t, isPanel || isNav, "%s on the error screen", k)
		}
	}

	v = newFunctions(t, &fakeLambda{pages: [][]aws.Function{{testFunction}}}, 160)
	v.Update(keyPress('/'))
	_, cmd := v.Update(keyPress('$'))
	for _, m := range collectMsgs(cmd) {
		_, isPanel := m.(msg.TabbedContentMsg)
		assert.False(t, isPanel)
	}
}

func TestLambdaFunctions_KeyMap(t *testing.T) {
	var keys []string
	for _, h := range NewLambdaFunctions(nil).KeyMap() {
		keys = append(keys, h.Binding.Help().Key)
		assert.NotEqual(t, ui.ModeReadWrite, h.Mode, "every key reads")
	}
	assert.Subset(t, keys, []string{"enter", "d", "$", "y", "l", "/", "r"})
}
