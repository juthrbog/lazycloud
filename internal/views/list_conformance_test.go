package views

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"testing"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/aws/awstest"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/nav"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// The conformance suite: every list view built on listCore must pass it.
// Each port PR adds the case of its view.

type conformanceCase struct {
	name string
	// build returns the view with a mock service whose list call accepts
	// only a context for which ok is true, and returns one item.
	build func(t *testing.T, ok func(context.Context) bool) nav.View
	// buildNil returns the view with a nil service.
	buildNil func() nav.View
	// coreMsgs returns the private core messages of the view's item type,
	// built for its current load generation. Call it only after every
	// earlier message is delivered, because a reload raises the generation.
	coreMsgs func(v nav.View) []tea.Msg
	// itemIDs returns the IDs of the items the view holds now.
	itemIDs func(v nav.View) []string
	// wantIDs are the items the view holds after the coreMsgs are delivered.
	// They prove that the current-generation pages ran, not only the drop path.
	wantIDs []string
	// rwKeys are the keys that must be marked ModeReadWrite.
	rwKeys []string
	// file and typeName name the source of the view, for the shadowing check.
	file, typeName string
	// coreType is the listCore instance the view embeds.
	coreType reflect.Type
	// allowMethods are core method names that the view may declare on
	// purpose (for example its own CapturingInput for a side input).
	allowMethods []string
}

var conformanceCases = []conformanceCase{
	{
		name: "sg_list",
		build: func(t *testing.T, ok func(context.Context) bool) nav.View {
			t.Helper()
			svc := new(awstest.MockEC2Service)
			svc.Test(t)
			svc.On("ListSecurityGroups", mock.MatchedBy(ok)).Return([]aws.SecurityGroup{{ID: "sg-1", Name: "one"}}, nil)
			// A call with a wrong context fails as a load error, not a mock panic.
			svc.On("ListSecurityGroups", mock.Anything).Return([]aws.SecurityGroup(nil), errors.New("wrong context"))
			return NewSGList(svc)
		},
		buildNil: func() nav.View { return NewSGList(nil) },
		coreMsgs: func(v nav.View) []tea.Msg {
			c := &v.(*SGList).listCore
			return []tea.Msg{
				listPageMsg[aws.SecurityGroup]{gen: c.loadGen, items: []aws.SecurityGroup{{ID: "sg-1"}}},
				listPageMsg[aws.SecurityGroup]{gen: c.loadGen - 1},
				listFailedMsg{gen: c.loadGen - 1, err: errors.New("old")},
				msg.ErrorMsg{Err: &loadError{err: errors.New("echo")}},
			}
		},
		itemIDs: func(v nav.View) []string {
			var ids []string
			for _, it := range v.(*SGList).items {
				ids = append(ids, it.ID)
			}
			return ids
		},
		wantIDs:  []string{"sg-1"},
		file:     "sg_list.go",
		typeName: "SGList",
		coreType: reflect.TypeFor[listCore[aws.SecurityGroup]](),
	},
	{
		name: "vpc_list",
		build: func(t *testing.T, ok func(context.Context) bool) nav.View {
			t.Helper()
			svc := new(awstest.MockEC2Service)
			svc.Test(t)
			svc.On("ListVPCsPage", mock.MatchedBy(ok), mock.Anything).Return(&aws.VPCPage{VPCs: []aws.VPC{{ID: "vpc-1", Name: "one"}}}, nil)
			svc.On("ListVPCsPage", mock.Anything, mock.Anything).Return(nil, errors.New("wrong context"))
			return NewVPCList(svc)
		},
		buildNil: func() nav.View { return NewVPCList(nil) },
		coreMsgs: func(v nav.View) []tea.Msg {
			c := &v.(*VPCList).listCore
			tok := "t2"
			return []tea.Msg{
				listPageMsg[aws.VPC]{gen: c.loadGen, items: []aws.VPC{{ID: "vpc-1"}}, next: &tok, page: 1},
				listPageMsg[aws.VPC]{gen: c.loadGen, items: []aws.VPC{{ID: "vpc-2"}}, page: 2},
				listPageMsg[aws.VPC]{gen: c.loadGen - 1, page: 3},
				listFailedMsg{gen: c.loadGen - 1, err: errors.New("old")},
				msg.ErrorMsg{Err: &loadError{err: errors.New("echo")}},
				keyPress('n'),
			}
		},
		itemIDs: func(v nav.View) []string {
			var ids []string
			for _, it := range v.(*VPCList).items {
				ids = append(ids, it.ID)
			}
			return ids
		},
		wantIDs:  []string{"vpc-1", "vpc-2"},
		file:     "vpc_list.go",
		typeName: "VPCList",
		coreType: reflect.TypeFor[listCore[aws.VPC]](),
	},
	{
		name: "subnet_list",
		build: func(t *testing.T, ok func(context.Context) bool) nav.View {
			t.Helper()
			svc := new(awstest.MockEC2Service)
			svc.Test(t)
			svc.On("ListSubnetsPage", mock.MatchedBy(ok), mock.Anything, "vpc-1").Return(&aws.SubnetPage{Subnets: []aws.Subnet{{ID: "subnet-1", Name: "one"}}}, nil)
			svc.On("ListSubnetsPage", mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("wrong context or VPC"))
			return NewSubnetList(svc, "vpc-1")
		},
		buildNil: func() nav.View { return NewSubnetList(nil, "vpc-1") },
		coreMsgs: func(v nav.View) []tea.Msg {
			c := &v.(*SubnetList).listCore
			tok := "t2"
			return []tea.Msg{
				listPageMsg[aws.Subnet]{gen: c.loadGen, items: []aws.Subnet{{ID: "subnet-1"}}, next: &tok, page: 1},
				listPageMsg[aws.Subnet]{gen: c.loadGen, items: []aws.Subnet{{ID: "subnet-2"}}, page: 2},
				listPageMsg[aws.Subnet]{gen: c.loadGen - 1, page: 3},
				listFailedMsg{gen: c.loadGen - 1, err: errors.New("old")},
				msg.ErrorMsg{Err: &loadError{err: errors.New("echo")}},
			}
		},
		itemIDs: func(v nav.View) []string {
			var ids []string
			for _, it := range v.(*SubnetList).items {
				ids = append(ids, it.ID)
			}
			return ids
		},
		wantIDs:  []string{"subnet-1", "subnet-2"},
		file:     "subnet_list.go",
		typeName: "SubnetList",
		coreType: reflect.TypeFor[listCore[aws.Subnet]](),
	},
	{
		name: "ami_list",
		build: func(t *testing.T, ok func(context.Context) bool) nav.View {
			t.Helper()
			svc := new(awstest.MockEC2Service)
			svc.Test(t)
			svc.On("ListOwnedAMIs", mock.MatchedBy(ok)).Return([]aws.AMI{{ID: "ami-1", Name: "one"}}, nil)
			svc.On("ListOwnedAMIs", mock.Anything).Return([]aws.AMI(nil), errors.New("wrong context"))
			return NewAMIList(svc)
		},
		buildNil: func() nav.View { return NewAMIList(nil) },
		coreMsgs: func(v nav.View) []tea.Msg {
			c := &v.(*AMIList).listCore
			return []tea.Msg{
				listPageMsg[aws.AMI]{gen: c.loadGen, items: []aws.AMI{{ID: "ami-1"}}},
				listPageMsg[aws.AMI]{gen: c.loadGen - 1, items: []aws.AMI{{ID: "ami-old"}}},
				listRefreshedMsg[aws.AMI]{gen: c.loadGen - 1, items: []aws.AMI{{ID: "ami-1", Name: "stale"}}},
				listFailedMsg{gen: c.loadGen - 1, err: errors.New("old")},
				msg.ErrorMsg{Err: &loadError{err: errors.New("echo")}},
				amiFocusLoadedMsg{id: "ami-x", err: context.Canceled},
				keyPress('p'), keyPress('q'), tea.KeyPressMsg{Code: tea.KeyEscape},
			}
		},
		itemIDs: func(v nav.View) []string {
			var ids []string
			for _, it := range v.(*AMIList).items {
				ids = append(ids, it.ID)
			}
			return ids
		},
		wantIDs:      []string{"ami-1"},
		file:         "ami_list.go",
		typeName:     "AMIList",
		coreType:     reflect.TypeFor[listCore[aws.AMI]](),
		allowMethods: []string{"CapturingInput"}, // the filter or the search input
	},
	{
		name: "ec2_list",
		build: func(t *testing.T, ok func(context.Context) bool) nav.View {
			t.Helper()
			svc := new(awstest.MockEC2Service)
			svc.Test(t)
			svc.On("ListInstancesPage", mock.MatchedBy(ok), mock.Anything).Return(&aws.InstancePage{Instances: []aws.Instance{{ID: "i-1", Name: "one", State: "running"}}}, nil)
			svc.On("ListInstancesPage", mock.Anything, mock.Anything).Return(nil, errors.New("wrong context"))
			return NewEC2List(svc, nil)
		},
		buildNil: func() nav.View { return NewEC2List(nil, nil) },
		coreMsgs: func(v nav.View) []tea.Msg {
			c := &v.(*EC2List).listCore
			tok := "t2"
			return []tea.Msg{
				listPageMsg[aws.Instance]{gen: c.loadGen, items: []aws.Instance{{ID: "i-1"}}, next: &tok, page: 1},
				listPageMsg[aws.Instance]{gen: c.loadGen, items: []aws.Instance{{ID: "i-2"}}, page: 2},
				listPageMsg[aws.Instance]{gen: c.loadGen - 1, items: []aws.Instance{{ID: "i-old"}}, page: 3},
				listRefreshedMsg[aws.Instance]{gen: c.loadGen - 1, items: []aws.Instance{{ID: "i-1", Name: "stale"}}},
				listFailedMsg{gen: c.loadGen - 1, err: errors.New("old")},
				msg.ErrorMsg{Err: &loadError{err: errors.New("echo")}},
				ec2InstanceDetailMsg{err: context.Canceled},
				ec2SSMSessionFinishedMsg{instanceID: "i-1", err: errors.New("x")},
				ui.ConfirmResultMsg{Confirmed: false, Action: "ec2_stop"},
				ui.PickerResultMsg{ID: "action", Selected: -1},
				keyPress('o'),
				keyPress('m'),
			}
		},
		itemIDs: func(v nav.View) []string {
			var ids []string
			for _, it := range v.(*EC2List).items {
				ids = append(ids, it.ID)
			}
			return ids
		},
		wantIDs:  []string{"i-1", "i-2"},
		rwKeys:   []string{"o", "m"},
		file:     "ec2_list.go",
		typeName: "EC2List",
		coreType: reflect.TypeFor[listCore[aws.Instance]](),
	},
	{
		name: "sqs_queues",
		build: func(t *testing.T, ok func(context.Context) bool) nav.View {
			t.Helper()
			svc := new(awstest.MockSQSService)
			svc.Test(t)
			svc.On("ListQueuesPage", mock.MatchedBy(ok), mock.Anything).Return(&aws.QueuePage{QueueURLs: []string{"u1"}}, nil)
			svc.On("ListQueuesPage", mock.Anything, mock.Anything).Return(nil, errors.New("wrong context"))
			svc.On("FetchAllQueueAttributes", mock.MatchedBy(ok), mock.Anything).Return([]aws.Queue{{URL: "u1", Name: "one"}}, nil)
			svc.On("FetchAllQueueAttributes", mock.Anything, mock.Anything).Return(nil, errors.New("wrong context"))
			return NewSQSQueues(svc)
		},
		buildNil: func() nav.View { return NewSQSQueues(nil) },
		coreMsgs: func(v nav.View) []tea.Msg {
			c := &v.(*SQSQueues).listCore
			tok := "t2"
			return []tea.Msg{
				listPageMsg[aws.Queue]{gen: c.loadGen, items: []aws.Queue{{URL: "u1"}}, next: &tok, page: 1},
				listPageMsg[aws.Queue]{gen: c.loadGen, items: []aws.Queue{{URL: "u2"}}, page: 2},
				listCompleteMsg[aws.Queue]{gen: c.loadGen, items: []aws.Queue{{URL: "u1", Name: "one"}, {URL: "u2", Name: "two"}}},
				listCompleteMsg[aws.Queue]{gen: c.loadGen - 1, items: []aws.Queue{{URL: "u-old"}}},
				listRefreshedMsg[aws.Queue]{gen: c.loadGen - 1, items: []aws.Queue{{URL: "u1", Name: "stale"}}},
				listFailedMsg{gen: c.loadGen - 1, err: errors.New("old")},
				msg.ErrorMsg{Err: &loadError{err: errors.New("echo")}},
				sqsQueueDetailMsg{err: context.Canceled},
				ui.ConfirmResultMsg{Confirmed: false, Action: "sqs_purge"},
				ui.PickerResultMsg{ID: "action", Selected: -1},
				msg.FormResultMsg{ID: "sqs_send_message", Aborted: true},
				keyPress('p'),
				keyPress('m'),
			}
		},
		itemIDs: func(v nav.View) []string {
			var ids []string
			for _, it := range v.(*SQSQueues).items {
				ids = append(ids, it.URL)
			}
			return ids
		},
		wantIDs:  []string{"u1", "u2"},
		rwKeys:   []string{"m"},
		file:     "sqs_queues.go",
		typeName: "SQSQueues",
		coreType: reflect.TypeFor[listCore[aws.Queue]](),
	},
	{
		name: "s3_list",
		build: func(t *testing.T, ok func(context.Context) bool) nav.View {
			t.Helper()
			svc := new(awstest.MockS3Service)
			svc.Test(t)
			svc.On("ListBuckets", mock.MatchedBy(ok)).Return([]aws.Bucket{{Name: "b-1"}}, nil)
			svc.On("ListBuckets", mock.Anything).Return(nil, errors.New("wrong context"))
			// enter browses and d asks for properties; the conformance run
			// presses both.
			svc.On("GetBucketProperties", mock.Anything, mock.Anything).Return(&aws.BucketProperties{Name: "b-1"}, nil).Maybe()
			return NewS3List(svc, "us-east-1")
		},
		buildNil: func() nav.View { return NewS3List(nil, "us-east-1") },
		coreMsgs: func(v nav.View) []tea.Msg {
			c := &v.(*S3List).listCore
			return []tea.Msg{
				listPageMsg[aws.Bucket]{gen: c.loadGen, items: []aws.Bucket{{Name: "b-1"}, {Name: "b-2"}}},
				listPageMsg[aws.Bucket]{gen: c.loadGen - 1, items: []aws.Bucket{{Name: "b-old"}}},
				listFailedMsg{gen: c.loadGen - 1, err: errors.New("old")},
				msg.ErrorMsg{Err: &loadError{err: errors.New("echo")}},
				s3BucketPropsMsg{err: context.Canceled},
				s3BucketCreatedMsg{name: "b-3", err: errors.New("x")},
				ui.ConfirmResultMsg{Confirmed: false, Action: "delete_bucket"},
				ui.FilterChangedMsg{Text: "zz", Source: s3CreateSource},
				keyPress('n'), tea.KeyPressMsg{Code: tea.KeyEscape},
				keyPress('x'),
			}
		},
		itemIDs: func(v nav.View) []string {
			var ids []string
			for _, it := range v.(*S3List).items {
				ids = append(ids, it.Name)
			}
			return ids
		},
		wantIDs:      []string{"b-1", "b-2"},
		rwKeys:       []string{"n", "x"},
		file:         "s3_list.go",
		typeName:     "S3List",
		coreType:     reflect.TypeFor[listCore[aws.Bucket]](),
		allowMethods: []string{"CapturingInput"}, // the filter or the create input
	},
}

// conformanceLoaded builds the view at width 160 and runs its first load.
func conformanceLoaded(t *testing.T, tc conformanceCase) nav.View {
	t.Helper()
	v := tc.build(t, func(context.Context) bool { return true })
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 20})
	pump(v, v.Init(), 5)
	return v
}

func TestListConformance_Interfaces(t *testing.T) {
	for _, tc := range conformanceCases {
		t.Run(tc.name, func(t *testing.T) {
			var v any = tc.buildNil()
			_, isView := v.(nav.View)
			_, isCapturer := v.(nav.InputCapturer)
			_, isSetter := v.(nav.ContextSetter)
			assert.True(t, isView)
			assert.True(t, isCapturer)
			assert.True(t, isSetter)
		})
	}
}

func TestListConformance_UpdateReturnsSamePointer(t *testing.T) {
	for _, tc := range conformanceCases {
		t.Run(tc.name, func(t *testing.T) {
			v := conformanceLoaded(t, tc)
			_, reload := v.Update(keyPress('r'))
			load := netLoadMsg(reload)
			msgs := []tea.Msg{
				spinner.TickMsg{},
				tea.WindowSizeMsg{Width: 60, Height: 20},
				tea.WindowSizeMsg{Width: 160, Height: 20},
				keyPress('s'), keyPress('S'), keyPress('y'), keyPress('d'), keyPress(' '), keyPress('z'),
				tea.KeyPressMsg{Code: tea.KeyDown}, tea.KeyPressMsg{Code: tea.KeyUp},
				tea.KeyPressMsg{Code: tea.KeyEnter},
				ui.PickerResultMsg{ID: "sort", Selected: 0},
				ui.PickerResultMsg{ID: "sort", Value: "_clear"},
				ui.PickerResultMsg{ID: "other", Selected: 0},
				ui.ConfirmResultMsg{Confirmed: true, Action: "other"},
				ui.FilterChangedMsg{Text: "x"},
				ui.FilterChangedMsg{Text: "x", Source: "side"},
				msg.FocusResourceMsg{ResourceID: "missing"},
				keyPress('/'), keyPress('x'), tea.KeyPressMsg{Code: tea.KeyEnter},
				keyPress('/'), tea.KeyPressMsg{Code: tea.KeyEscape},
				load,
				msg.ErrorMsg{Err: errors.New("x")},
				keyPress('r'),
			}
			deliver := func(msgs []tea.Msg) {
				for _, m := range msgs {
					got, _ := v.Update(m)
					require.Same(t, v, got, "%T", m)
				}
			}
			deliver(msgs)
			// Build the core messages only now: the 'r' above raised the
			// load generation, and messages built earlier would be stale.
			deliver(tc.coreMsgs(v))
			assert.Equal(t, tc.wantIDs, tc.itemIDs(v), "the current-generation pages were applied")
			deliver([]tea.Msg{tea.KeyPressMsg{Code: tea.KeyEscape}, struct{}{}})
		})
	}
}

func TestListConformance_SetContextReachesFetch(t *testing.T) {
	type ctxKey struct{}
	for _, tc := range conformanceCases {
		t.Run(tc.name, func(t *testing.T) {
			v := tc.build(t, func(ctx context.Context) bool {
				return ctx.Value(ctxKey{}) == "nav"
			})
			v.(nav.ContextSetter).SetContext(context.WithValue(t.Context(), ctxKey{}, "nav"))
			v.Update(tea.WindowSizeMsg{Width: 160, Height: 20})
			seen := pump(v, v.Init(), 5)
			_, failed := firstOfType[msg.ErrorMsg](seen)
			require.False(t, failed, "the fetch got the navigator's context")
			assert.Regexp(t, `^1/1 `, plain(v.Footer()))
		})
	}
}

// The app binds these letters globally, so a view must not.
var globalLetters = []string{"q", "P", "R", "T", "W", "E", "L", "?", ":"}

func TestListConformance_NoGlobalLetters(t *testing.T) {
	for _, tc := range conformanceCases {
		t.Run(tc.name, func(t *testing.T) {
			v := conformanceLoaded(t, tc)
			for _, h := range v.KeyMap() {
				for _, k := range h.Keys() {
					assert.NotContains(t, globalLetters, k, "KeyMap binds a global letter")
				}
			}
			for _, k := range globalLetters {
				_, cmd := v.Update(keyPress([]rune(k)[0]))
				assert.Empty(t, collectMsgs(cmd), "key %q does something", k)
			}
		})
	}
}

func TestListConformance_RWKeysMarked(t *testing.T) {
	for _, tc := range conformanceCases {
		t.Run(tc.name, func(t *testing.T) {
			for _, h := range tc.buildNil().KeyMap() {
				rw := slices.ContainsFunc(h.Keys(), func(k string) bool { return slices.Contains(tc.rwKeys, k) })
				if rw {
					assert.Equal(t, ui.ModeReadWrite, h.Mode, "%v", h.Keys())
				} else {
					assert.Equal(t, ui.ModeAny, h.Mode, "%v", h.Keys())
				}
			}
		})
	}
}

func TestListConformance_NilServiceGivesErrorMsg(t *testing.T) {
	for _, tc := range conformanceCases {
		t.Run(tc.name, func(t *testing.T) {
			v := tc.buildNil()
			v.Update(tea.WindowSizeMsg{Width: 160, Height: 20})
			var seen []tea.Msg
			require.NotPanics(t, func() { seen = pump(v, v.Init(), 5) })
			em, ok := firstOfType[msg.ErrorMsg](seen)
			require.True(t, ok)
			require.ErrorIs(t, em.Err, errNoClient)
			assert.NotEmpty(t, em.Context)
			assert.Equal(t, "\n  Error: AWS client not initialized", plain(v.View().Content))
			assert.Contains(t, v.View().Content, fgSGR(ui.ActiveTheme.Error))
		})
	}
}

var footerFormat = regexp.MustCompile(`^\d+/\d+ \S`)

func TestListConformance_FooterFormat(t *testing.T) {
	for _, tc := range conformanceCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Regexp(t, footerFormat, plain(tc.buildNil().Footer()))
			assert.Regexp(t, footerFormat, plain(conformanceLoaded(t, tc).Footer()))
		})
	}
}

// The view must not shadow the core: no field or method with the name of a
// core field or method (also the ones viewCtx promotes, such as ctx,
// SetContext and viewContext), and no second viewCtx.
func TestListConformance_NoShadowing(t *testing.T) {
	coreMethods := listCoreMethodNames(t)
	require.Contains(t, coreMethods, "update", "the parser found the core methods")
	require.Contains(t, coreMethods, "viewContext", "the parser found the viewCtx methods")
	require.Contains(t, coreMethods, "SetContext", "the parser found the viewCtx methods")

	for _, tc := range conformanceCases {
		t.Run(tc.name, func(t *testing.T) {
			// coreNames holds every field and method name of *listCore[T],
			// promoted ones included.
			coreNames := maps.Clone(coreMethods)
			for _, f := range reflect.VisibleFields(tc.coreType) {
				coreNames[f.Name] = true
			}
			for m := range reflect.PointerTo(tc.coreType).Methods() {
				coreNames[m.Name] = true
			}
			require.Contains(t, coreNames, "ctx", "the promoted viewCtx field is checked")
			require.Contains(t, coreNames, "items", "the core fields are checked")

			vt := reflect.TypeOf(tc.buildNil()).Elem()
			embeds := 0
			for f := range vt.Fields() {
				if f.Type == tc.coreType {
					embeds++
					continue
				}
				assert.False(t, coreNames[f.Name], "field %s shadows a core field or method", f.Name)
			}
			assert.Equal(t, 1, embeds, "the view embeds its listCore once")
			assert.Equal(t, 1, countViewCtx(vt), "viewCtx is embedded once, in listCore")

			for _, name := range methodNames(t, tc.file, tc.typeName) {
				if slices.Contains(tc.allowMethods, name) {
					continue
				}
				assert.False(t, coreNames[name], "method %s.%s shadows a core field or method", tc.typeName, name)
			}
		})
	}
}

// countViewCtx counts the viewCtx embeds reachable through embedded fields.
func countViewCtx(t reflect.Type) int {
	n := 0
	for f := range t.Fields() {
		if !f.Anonymous {
			continue
		}
		if f.Type == reflect.TypeFor[viewCtx]() {
			n++
			continue
		}
		if f.Type.Kind() == reflect.Struct {
			n += countViewCtx(f.Type)
		}
	}
	return n
}

// listCoreMethodNames parses the core files and returns the names of the
// methods of *listCore: the ones declared on listCore and the ones that the
// embedded viewCtx promotes.
func listCoreMethodNames(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, src := range []struct{ file, typeName string }{
		{"listcore.go", "listCore"},
		{"listcore_ui.go", "listCore"},
		{"context.go", "viewCtx"},
	} {
		for _, name := range methodNames(t, src.file, src.typeName) {
			out[name] = true
		}
	}
	return out
}

// methodNames parses file and returns the methods declared on typeName or
// *typeName (also with type parameters).
func methodNames(t *testing.T, file, typeName string) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.SkipObjectResolution)
	require.NoError(t, err)
	var out []string
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || len(fn.Recv.List) == 0 {
			continue
		}
		if receiverName(fn.Recv.List[0].Type) == typeName {
			out = append(out, fn.Name.Name)
		}
	}
	return out
}

func receiverName(e ast.Expr) string {
	switch e := e.(type) {
	case *ast.StarExpr:
		return receiverName(e.X)
	case *ast.IndexExpr:
		return receiverName(e.X)
	case *ast.IndexListExpr:
		return receiverName(e.X)
	case *ast.Ident:
		return e.Name
	}
	return ""
}
