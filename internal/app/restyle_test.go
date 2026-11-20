package app

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/registry"
	"github.com/juthrbog/lazycloud/internal/ui"
)

var tableType = reflect.TypeFor[ui.Table]()

// hasTable reports whether the struct type t holds a ui.Table, also in an
// embedded struct.
func hasTable(t reflect.Type) bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return false
	}
	for f := range t.Fields() {
		if f.Type == tableType || (f.Anonymous && hasTable(f.Type)) {
			return true
		}
	}
	return false
}

// A8: a view that keeps colors draws them again on a theme switch. So each
// view with a table implements ui.Restyler, and so does the event log,
// which keeps colored lines.
func TestViewsThatKeepColorsRestyle(t *testing.T) {
	m := newTestModel(140, 40)
	withTable := map[string]bool{}
	for id := range routes {
		v := m.resolveView(msg.NavigateMsg{ViewID: id})
		require.NotNilf(t, v, "route %q", id)
		_, ok := v.(ui.Restyler)
		if hasTable(reflect.TypeOf(v)) {
			withTable[id] = true
			assert.Truef(t, ok, "view %q has a table, so it must implement ui.Restyler", id)
		}
		if id == "eventlog" {
			assert.True(t, ok, "the event log keeps colored lines")
		}
	}
	// Each service view is a list, so hasTable must find its table. This
	// keeps hasTable honest without a count to update for each service.
	for _, s := range registry.Services {
		for _, f := range s.Features {
			assert.Truef(t, withTable[f.ViewID], "hasTable finds no table in the list %q", f.ViewID)
		}
	}
	for _, id := range []string{"s3_objects", "s3_versions", "sqs_messages", "logs_streams"} {
		assert.Truef(t, withTable[id], "hasTable finds no table in %q", id)
	}
}
