package views

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/eventlog"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// The tests in this file give views AWS data with terminal control sequences
// and check that the rendered output is safe (M23). dcs is a DECRQSS query,
// which a terminal answers with input. csi8 is the C1 form of CSI.
const (
	dcs  = "\x1bP$qm\x1b\\"
	csi8 = "\u009b7m"
)

// assertSafeOutput fails when out holds a part that reaches the terminal as
// a control: a string sequence, a C1 control character, or a C0 control
// other than ESC (for SGR styling), line feed, and tab.
func assertSafeOutput(t *testing.T, out string) {
	t.Helper()
	assert.Equal(t, ui.StripUnsafe(out), out, "output holds a string sequence or a C1 control")
	for i := 0; i < len(out); i++ {
		if c := out[i]; c < 0x20 && c != 0x1b && c != '\n' && c != '\t' {
			assert.Failf(t, "C0 control in output", "byte %#x at %d", c, i)
			return
		}
	}
}

func TestS3Objects_ShowsKeysSafely(t *testing.T) {
	view, _ := newTestS3Objects()
	view.Update(tea.WindowSizeMsg{Width: 140, Height: 24})
	view.Update(s3PageLoadedMsg{
		bucket:   "test-bucket",
		prefixes: []string{"logs-" + csi8 + "/"},
		objects:  []aws.S3Object{{Key: "key-" + dcs + ".txt"}, {Key: "real\rfake.txt"}},
		pageNum:  1,
	})

	out := view.View().Content
	assertSafeOutput(t, out)
	plain := ansi.Strip(out)
	assert.Contains(t, plain, "logs-�7m/")
	assert.Contains(t, plain, "key-␛P$qm␛\\.txt")
	assert.Contains(t, plain, "real␍fake.txt")
}

func TestS3Objects_FooterShowsPrefixSafely(t *testing.T) {
	view, _ := newTestS3Objects()
	view.prefix = "logs-" + dcs + "/"
	footer := view.Footer()
	assertSafeOutput(t, footer)
	assert.Contains(t, footer, "s3://test-bucket/logs-␛P$qm␛\\/")
}

// A line break in a key shows as ␊, so the key cannot add a line to the
// delete prompt.
func TestS3Objects_DeletePromptKeepsEachKeyOnOneLine(t *testing.T) {
	ui.SetReadOnly(false)
	t.Cleanup(func() { ui.SetReadOnly(true) })
	view, _ := newTestS3Objects()
	view.Update(s3PageLoadedMsg{
		bucket:  "test-bucket",
		objects: []aws.S3Object{{Key: "a.txt\nNothing else is deleted."}},
		pageNum: 1,
	})

	_, cmd := view.Update(keyPress('x'))
	req, ok := firstOfType[msg.RequestConfirmMsg](collectMsgs(cmd))
	require.True(t, ok)
	assert.Equal(t, "Delete 1 object(s)?\n  a.txt␊Nothing else is deleted.", req.Message)
}

func TestS3Objects_OverwritePromptKeepsKeysOnOneLine(t *testing.T) {
	view, _ := newTestS3Objects()
	req, ok := view.askOverwrite("copy", "src\n.txt", "dst\nfake")().(msg.RequestConfirmMsg)
	require.True(t, ok)
	assert.Equal(t, "s3://test-bucket/dst␊fake exists.\nReplace it with src␊.txt?", req.Message)
}

func TestS3Objects_DeleteAllPromptShowsPrefixSafely(t *testing.T) {
	ui.SetReadOnly(false)
	t.Cleanup(func() { ui.SetReadOnly(true) })
	view, _ := newTestS3Objects()
	view.prefix = "p\n" + csi8 + "/"
	view.Update(s3PageLoadedMsg{
		bucket:  "test-bucket",
		prefix:  view.prefix,
		objects: []aws.S3Object{{Key: view.prefix + "a.txt"}},
		pageNum: 1,
	})

	_, cmd := view.Update(keyPress('X'))
	req, ok := firstOfType[msg.RequestConfirmMsg](collectMsgs(cmd))
	require.True(t, ok)
	assert.Equal(t, "Delete all 1 item(s) in s3://test-bucket/p␊�7m/?\n  a.txt", req.Message)
}

func TestS3Versions_FooterShowsKeySafely(t *testing.T) {
	view := newTestS3Versions()
	view.key = "k-" + dcs
	footer := view.Footer()
	assertSafeOutput(t, footer)
	assert.Contains(t, footer, "k-␛P$qm␛\\")
}

func TestEC2List_ShowsTagsSafelyAndColorsState(t *testing.T) {
	view, _ := newTestEC2List()
	loadInstances(view, []aws.Instance{{ID: "i-1", Name: "web-" + dcs + csi8, State: "running"}})

	out := view.View().Content
	assertSafeOutput(t, out)
	assert.Contains(t, ansi.Strip(out), "web-␛P$qm␛\\�7m")
	assert.Contains(t, out, ui.StateColor("running"), "the state column keeps its color")
	assert.Equal(t, "running", view.table.SelectedRow()[2], "the cell holds the plain state")
}

// The state cells hold plain text, and the State column colors them.
func TestViews_StateColumnKeepsColor(t *testing.T) {
	views := map[string]func() tea.Model{
		"ami_list": func() tea.Model {
			v := newTestAMIList()
			loadAMIs(v, []aws.AMI{{ID: "ami-1", Name: "n" + csi8, State: "available"}})
			return v
		},
		"subnet_list": func() tea.Model {
			v := newTestSubnetList("vpc-1")
			loadSubnets(v, []aws.Subnet{{ID: "subnet-1", Name: "n" + csi8, State: "available"}})
			return v
		},
		"vpc_list": func() tea.Model {
			v := newTestVPCList()
			loadVPCs(v, []aws.VPC{{ID: "vpc-1", Name: "n" + csi8, State: "available"}})
			return v
		},
	}
	for name, newView := range views {
		t.Run(name, func(t *testing.T) {
			out := newView().View().Content
			assertSafeOutput(t, out)
			assert.Contains(t, out, ui.StateColor("available"))
			assert.Contains(t, ansi.Strip(out), "n�7m")
		})
	}
}

func TestSQSQueues_CountColumnsKeepColor(t *testing.T) {
	v, _ := newQueueList()
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	v.Update(sqsAllAttrsLoadedMsg{queues: []aws.Queue{
		{Name: "orders", URL: "u1", Type: "Standard", ApproximateMessageCount: 8},
		{Name: "broken", URL: "u2", Err: errors.New("denied")},
	}})

	out := v.View().Content
	assertSafeOutput(t, out)
	assert.Contains(t, out, ui.CountColor("8"))
	assert.Equal(t, "8", v.table.SelectedRow()[2], "the cell holds the plain count")
	assert.Contains(t, ansi.Strip(out), "?")
}

// Normal AWS data shows no control pictures. A ␛ here means that a view put
// styled text in a cell, and the table showed the styling as text. Use a
// column Style instead.
func TestViews_CleanDataShowsNoControlPictures(t *testing.T) {
	now := time.Now()
	views := map[string]func(t *testing.T) tea.Model{
		"ami_list": func(*testing.T) tea.Model {
			v := newTestAMIList()
			loadAMIs(v, []aws.AMI{{ID: "ami-1", Name: "al2023", State: "available", CreationDate: "2026-01-01"}})
			return v
		},
		"ec2_list": func(*testing.T) tea.Model {
			v, _ := newTestEC2List()
			loadInstances(v, []aws.Instance{testRunningInstance, testStoppedInstance})
			return v
		},
		"s3_list": func(t *testing.T) tea.Model {
			v, _ := newBucketList(t, "logs", "data")
			return v
		},
		"s3_objects": func(t *testing.T) tea.Model {
			v, _ := newS3TransferView(t)
			return v
		},
		"s3_versions": func(*testing.T) tea.Model {
			v := newTestS3Versions()
			v.Update(s3VersionsLoadedMsg{versions: []aws.ObjectVersion{
				{VersionID: "v2", Size: 10, LastModified: now, IsLatest: true},
				{VersionID: "v1", IsDeleteMarker: true, LastModified: now},
			}})
			return v
		},
		"sg_list": func(*testing.T) tea.Model { return newSGList("sg-1", "sg-2") },
		"sqs_messages": func(*testing.T) tea.Model {
			v, _ := newTestSQSMessages()
			v.Update(sqsMessagesLoadedMsg{queueURL: testQueueURL, messages: []aws.SQSMessage{
				{MessageID: "m-1", Body: `{"order": 1}`, SentTimestamp: now, ApproximateReceiveCount: 2},
			}})
			return v
		},
		"sqs_queues": func(*testing.T) tea.Model {
			v, _ := newQueueList()
			v.Update(sqsAllAttrsLoadedMsg{queues: []aws.Queue{
				{Name: "orders", URL: "u1", Type: "Standard", ApproximateMessageCount: 8, ApproximateInFlightCount: 1},
			}})
			return v
		},
		"subnet_list": func(*testing.T) tea.Model {
			v := newTestSubnetList("vpc-aaa111")
			loadSubnets(v, []aws.Subnet{testSubnet1})
			return v
		},
		"vpc_list": func(*testing.T) tea.Model {
			v := newTestVPCList()
			loadVPCs(v, []aws.VPC{testVPC1})
			return v
		},
	}
	for name, newView := range views {
		t.Run(name, func(t *testing.T) {
			out := ansi.Strip(newView(t).View().Content)
			assert.NotContains(t, out, "␛")
			assert.NotContains(t, out, "�")
		})
	}
}

func TestViews_ShowListErrorSafely(t *testing.T) {
	views := map[string]func() tea.Model{
		"ami_list":     func() tea.Model { v := newTestAMIList(); return v },
		"ec2_list":     func() tea.Model { v, _ := newTestEC2List(); return v },
		"s3_list":      func() tea.Model { v, _ := newTestS3List(); return v },
		"s3_objects":   func() tea.Model { v, _ := newTestS3Objects(); return v },
		"s3_versions":  func() tea.Model { v := newTestS3Versions(); return v },
		"sg_list":      func() tea.Model { v := newTestSGList(); return v },
		"sqs_messages": func() tea.Model { v, _ := newTestSQSMessages(); return v },
		"sqs_queues":   func() tea.Model { v, _ := newQueueList(); return v },
		"subnet_list":  func() tea.Model { v := newTestSubnetList("vpc-1"); return v },
		"vpc_list":     func() tea.Model { v := newTestVPCList(); return v },
	}
	for name, newView := range views {
		t.Run(name, func(t *testing.T) {
			view := newView()
			view.Update(msg.ErrorMsg{Err: errors.New("denied for " + dcs), Context: "listing"})

			out := view.View().Content
			assertSafeOutput(t, out)
			assert.Contains(t, ansi.Strip(out), "Error: denied for ␛P$qm␛\\")
		})
	}
}

func TestSQSMessages_ShowsBodySafely(t *testing.T) {
	view, _ := newTestSQSMessages()
	view.Update(sqsMessagesLoadedMsg{queueURL: testQueueURL, messages: []aws.SQSMessage{
		{MessageID: "m-1", Body: "C1[" + csi8 + "] CR[visible\rHIDDEN]", SentTimestamp: time.Now()},
	}})

	out := view.View().Content
	assertSafeOutput(t, out)
	assert.Contains(t, ansi.Strip(out), "C1[�7m]")
}

func TestEventLog_ShowsMessagesSafely(t *testing.T) {
	eventlog.Info(eventlog.CatAWS, "Deleted k-"+dcs+"\nfake entry")
	view := NewEventLog()
	view.Update(tea.WindowSizeMsg{Width: 160, Height: 30})

	out := view.View().Content
	assertSafeOutput(t, out)
	plain := ansi.Strip(out)
	assert.Contains(t, plain, "Deleted k-␛P$qm␛\\␊fake entry", "one entry stays on one line")
	for line := range strings.SplitSeq(plain, "\n") {
		assert.False(t, strings.HasPrefix(strings.TrimSpace(line), "fake entry"))
	}
}
