package views

import (
	"context"
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

// Tests for the alias, version and trigger lists of a function
// (docs/design/lambda.md, PR 3).

// childFake answers the list calls of one function.
type childFake struct {
	aws.LambdaService

	aliases  []aws.Alias
	versions [][]aws.Function // pages
	triggers []aws.Trigger
	names    []string // the function name of each list call
	gets     []string // name:qualifier of each GetFunction
	urls     int
}

func (f *childFake) ListAliasesPage(_ context.Context, name string, _ *string) (*aws.AliasPage, error) {
	f.names = append(f.names, name)
	return &aws.AliasPage{Aliases: f.aliases}, nil
}

func (f *childFake) ListTriggersPage(_ context.Context, name string, _ *string) (*aws.TriggerPage, error) {
	f.names = append(f.names, name)
	return &aws.TriggerPage{Triggers: f.triggers}, nil
}

func (f *childFake) ListVersionsPage(_ context.Context, name string, tok *string) (*aws.FunctionPage, error) {
	f.names = append(f.names, name)
	i := 0
	if tok != nil {
		i, _ = strconv.Atoi(*tok)
	}
	p := &aws.FunctionPage{Functions: f.versions[i]}
	if i+1 < len(f.versions) {
		p.Next = new(strconv.Itoa(i + 1))
	}
	return p, nil
}

func (f *childFake) GetFunction(_ context.Context, name, qualifier string) (*aws.FunctionDetail, error) {
	f.gets = append(f.gets, name+":"+qualifier)
	return &aws.FunctionDetail{Function: aws.Function{Name: name, Version: qualifier, Runtime: "python3.12"}, State: "Active"}, nil
}

func (f *childFake) GetFunctionURL(context.Context, string) (*aws.FunctionURL, error) {
	f.urls++
	return nil, nil
}

// load sizes a child list and runs its load.
func load[V interface {
	tea.Model
	SetContext(context.Context)
}](t *testing.T, v V, rounds int) V {
	t.Helper()
	v.SetContext(t.Context())
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	pump(v, v.Init(), rounds)
	return v
}

// a, v and t on a function open its aliases, versions and triggers, with
// the function name and its log group.
func TestLambdaFunctions_ChildKeys(t *testing.T) {
	v := newFunctions(t, &fakeLambda{pages: [][]aws.Function{{testImageFunction}}}, 160)
	want := map[string]string{"function": "legacy-image", "log_group": "/custom/legacy"}
	for k, id := range map[rune]string{'a': "lambda_aliases", 'v': "lambda_versions", 't': "lambda_triggers"} {
		_, cmd := v.Update(keyPress(k))
		assert.Equal(t, []msg.NavigateMsg{{ViewID: id, Params: want}}, navigations(collectMsgs(cmd)), string(k))
	}
}

func TestLambdaAliases(t *testing.T) {
	f := &childFake{aliases: []aws.Alias{
		{Name: "live", ARN: "arn:live", Version: "1", Description: "production", Weights: map[string]float64{"2": 0.1}},
		{Name: "dev", Version: "$LATEST"},
	}}
	v := load(t, NewLambdaAliases(f, "orders-api", "/aws/lambda/orders-api"), 5)
	assert.Equal(t, []string{"orders-api"}, f.names)
	assert.Equal(t, "lambda_aliases:orders-api", v.ID())
	assert.Equal(t, "orders-api aliases", v.Title())
	out := plain(v.View().Content)
	for _, s := range []string{"live", "2: 10%", "production", "dev", "$LATEST"} {
		assert.Contains(t, out, s)
	}
	assert.Equal(t, "2/2 aliases", v.Footer())

	_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Equal(t, []msg.NavigateMsg{{ViewID: "logs_events", Params: map[string]string{"log_group": "/aws/lambda/orders-api"}}},
		navigations(collectMsgs(cmd)), "enter opens the logs of the function")

	_, cmd = v.Update(keyPress('d'))
	panel := detailPanel(t, v, cmd)
	assert.Equal(t, "orders-api:live", panel.PanelTitle)
	info := panel.Tabs[0].Content
	assert.Contains(t, info, "Routing          1: 90%, 2: 10%")
	assert.Contains(t, info, "arn:live")
}

func TestLambdaWeights(t *testing.T) {
	assert.Empty(t, lambdaWeights(nil))
	assert.Equal(t, "2: 10%, 10: 5.5%", lambdaWeights(map[string]float64{"10": 0.055, "2": 0.1}), "versions sort as numbers")
	assert.Equal(t, "$LATEST: 50%", lambdaWeights(map[string]float64{"$LATEST": 0.5}))
	assert.Equal(t, "2: 7%", lambdaWeights(map[string]float64{"2": 0.07}), "no float noise")
	assert.Equal(t, "2: 29%", lambdaWeights(map[string]float64{"2": 0.29}))
	assert.Equal(t, "2: 12.345%", lambdaWeights(map[string]float64{"2": 0.12345}), "AWS allows 5 decimal places")
	info, _ := buildInfo(lambdaAliasInfo("fn", aws.Alias{Name: "a", Version: "1", Weights: map[string]float64{"2": 0.57}}))
	assert.Contains(t, info, "Routing          1: 43%, 2: 57%")
}

// The versions list reads its pages, sorts the versions as numbers with
// $LATEST first, and d reads that version without the URL.
func TestLambdaVersions(t *testing.T) {
	at := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	f := &childFake{versions: [][]aws.Function{{
		{Name: "orders-api", Version: "$LATEST", Description: "Takes orders (version 2)", Runtime: "python3.12", CodeSize: 219, LastModified: at},
		{Name: "orders-api", Version: "10", Description: "ten"},
		{Name: "orders-api", Version: "2", Description: "two"},
	}}}
	v := load(t, NewLambdaVersions(f, "orders-api", "/aws/lambda/orders-api"), 5)
	assert.Equal(t, "orders-api versions", v.Title())
	out := plain(v.View().Content)
	for _, s := range []string{"$LATEST", "Takes orders (version 2)", "python3.12", "219 B", "2026-11-20", "ten"} {
		assert.Contains(t, out, s)
	}
	assert.Equal(t, ui.SortKeyInt(-1), lambdaVersionRow(aws.Function{Version: "$LATEST"}, ui.TierWide).Sort[0])
	assert.Less(t, lambdaVersionRow(aws.Function{Version: "2"}, ui.TierWide).Sort[0],
		lambdaVersionRow(aws.Function{Version: "10"}, ui.TierWide).Sort[0], "2 sorts before 10")

	_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Equal(t, []msg.NavigateMsg{{ViewID: "logs_events", Params: map[string]string{"log_group": "/aws/lambda/orders-api"}}},
		navigations(collectMsgs(cmd)))

	_, cmd = v.Update(keyPress('d'))
	panel := detailPanel(t, v, cmd)
	assert.Equal(t, []string{"orders-api:$LATEST"}, f.gets, "d reads the version under the cursor")
	assert.Equal(t, "orders-api:$LATEST", panel.PanelTitle)
	assert.Zero(t, f.urls, "a version has no URL of its own")
	assert.NotContains(t, panel.Tabs[0].Content, "Function URL")
	assert.Contains(t, panel.Tabs[0].Content, "Version          $LATEST")
}

// The versions list stops after 4 pages, and l loads more.
func TestLambdaVersions_Budget(t *testing.T) {
	pages := make([][]aws.Function, lambdaVersionBudget+1)
	for i := range pages {
		pages[i] = []aws.Function{{Name: "fn", Version: strconv.Itoa(i + 1)}}
	}
	v := load(t, NewLambdaVersions(&childFake{versions: pages}, "fn", "/aws/lambda/fn"), 3*lambdaVersionBudget)
	assert.Len(t, v.items, lambdaVersionBudget)
	assert.Contains(t, v.Footer(), "l for more")
	_, cmd := v.Update(keyPress('l'))
	require.NotNil(t, cmd)
	pump(v, cmd, 5)
	assert.Len(t, v.items, lambdaVersionBudget+1)
}

var testTrigger = aws.Trigger{
	UUID: "u1", SourceARN: "arn:aws:sqs:us-east-1:1:orders-api-jobs", FunctionARN: "arn:fn",
	State: "Enabled", StateReason: "USER_INITIATED", LastResult: "OK", BatchSize: 10, BatchWindowSec: 5,
	LastModified: time.Date(2026, 11, 21, 2, 8, 45, 0, time.UTC), Filters: []string{`{"body":{"type":["order"]}}`},
}

// The triggers list shows the type, the short source name and the state
// with its color. enter and d open the detail, which links an SQS source to
// its queue.
func TestLambdaTriggers(t *testing.T) {
	kinesis := aws.Trigger{UUID: "u2", SourceARN: "arn:aws:kinesis:us-east-1:1:stream/clicks", State: "Disabled", BatchSize: 100}
	f := &childFake{triggers: []aws.Trigger{testTrigger, kinesis}}
	v := load(t, NewLambdaTriggers(f, "orders-api"), 5)
	assert.Equal(t, "orders-api triggers", v.Title())
	out := v.View().Content
	assert.Contains(t, out, ui.StateColor("Enabled"), "the state has its color")
	for _, s := range []string{"SQS", "orders-api-jobs", "Kinesis", "clicks", "10", "100"} {
		assert.Contains(t, plain(out), s)
	}
	assert.NotContains(t, plain(out), "poll-based", "the note shows only for an empty list")

	for _, k := range []tea.KeyPressMsg{{Code: tea.KeyEnter}, keyPress('d')} {
		_, cmd := v.Update(k)
		var panel msg.TabbedContentMsg
		for _, m := range collectMsgs(cmd) {
			if p, ok := m.(msg.TabbedContentMsg); ok {
				panel = p
			}
		}
		require.Len(t, panel.Tabs, 2, k.String())
		assert.Equal(t, "SQS orders-api-jobs", panel.PanelTitle)
		info := panel.Tabs[0]
		for _, s := range []string{"u1", "USER_INITIATED", "OK", "5 s", "arn:fn"} {
			assert.Contains(t, info.Content, s)
		}
		assert.Equal(t, []msg.TabLink{{Line: 2, ViewID: "sqs_queues", Params: map[string]string{"focus": "orders-api-jobs"}}}, info.Links)
		assert.Contains(t, panel.Tabs[1].Content, `"type"`)
	}

	cmd := lambdaTriggerDetail(kinesis)
	p := cmd().(msg.TabbedContentMsg)
	assert.Empty(t, p.Tabs[0].Links, "only an SQS source has a link")
	assert.Contains(t, p.Tabs[1].Content, "No filters")
}

// An empty trigger list explains which triggers it can show. The note
// wraps to the width and fits the height of a small window.
func TestLambdaTriggers_EmptyNote(t *testing.T) {
	v := load(t, NewLambdaTriggers(&childFake{}, "fn"), 5)
	assert.Contains(t, plain(v.View().Content), "only poll-based triggers")
	assert.Equal(t, "0/0 triggers", v.Footer())
	for _, w := range []int{40, 80, 120} {
		v.Update(tea.WindowSizeMsg{Width: w, Height: 12})
		lines := strings.Split(v.View().Content, "\n")
		assert.LessOrEqual(t, len(lines), 12, "width %d", w)
		for _, l := range lines {
			assert.LessOrEqual(t, ansi.StringWidth(l), w, "width %d: %q", w, plain(l))
		}
		assert.Contains(t, strings.Join(strings.Fields(plain(v.View().Content)), " "), "set up on their side", "the whole note shows at width %d", w)
	}
}

func TestLambdaSourceName(t *testing.T) {
	for arn, want := range map[string]string{
		"arn:aws:sqs:us-east-1:1:orders":                                           "orders",
		"arn:aws:kinesis:us-east-1:1:stream/clicks":                                "clicks",
		"arn:aws:dynamodb:us-east-1:1:table/Orders/stream/2024-01-01T00:48:05.899": "Orders",
		"arn:aws:kafka:us-east-1:1:cluster/my-cluster/abcd-1234-1":                 "my-cluster",
		"arn:aws:mq:us-east-1:1:broker:my-broker:b-1234":                           "my-broker",
		"arn:aws:rds:us-east-1:1:cluster:docs":                                     "docs",
		"":                                                                         "self-managed",
		"no-colon":                                                                 "no-colon",
		"arn:aws:sqs:us-east-1:1:":                                                 "arn:aws:sqs:us-east-1:1:",
	} {
		assert.Equal(t, want, lambdaSourceName(arn), arn)
	}
}

// The child lists keep their keys on the error screen and with the filter
// open.
func TestLambdaChildren_KeysWait(t *testing.T) {
	v := load(t, NewLambdaAliases(&childFake{aliases: []aws.Alias{{Name: "live"}}}, "fn", "g"), 5)
	v.Update(msg.ErrorMsg{Err: errorString("throttled")})
	_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Empty(t, navigations(collectMsgs(cmd)))

	w := load(t, NewLambdaVersions(&childFake{versions: [][]aws.Function{{{Version: "1"}}}}, "fn", "g"), 5)
	w.Update(keyPress('/'))
	_, cmd = w.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Empty(t, navigations(collectMsgs(cmd)))
}

type errorString string

func (e errorString) Error() string { return string(e) }
