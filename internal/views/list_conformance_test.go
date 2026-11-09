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
	// built for its current load generation.
	coreMsgs func(v nav.View) []tea.Msg
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
		file:     "sg_list.go",
		typeName: "SGList",
		coreType: reflect.TypeFor[listCore[aws.SecurityGroup]](),
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
			msgs = append(msgs, tc.coreMsgs(v)...)
			msgs = append(msgs, tea.KeyPressMsg{Code: tea.KeyEscape}, struct{}{})
			for _, m := range msgs {
				got, _ := v.Update(m)
				require.Same(t, v, got, "%T", m)
			}
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
