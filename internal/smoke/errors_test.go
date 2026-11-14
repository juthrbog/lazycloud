//go:build smoke

package smoke

import (
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestErrorScreenBlocksRowKeys loads the VPC list, makes the next refresh
// fail, and checks that the row keys do nothing on the error screen (L32
// in docs/reviews/2026-10-11). The old rows stay in the list behind the
// error, but the user cannot see them.
func TestErrorScreenBlocksRowKeys(t *testing.T) {
	proxy, fail := failingProxy(t)
	h := start(t, proxy)
	h.open("vpcs", "VPCs")
	h.filter("staging-vpc")
	h.waitFor("staging-vpc")

	fail.Store(true)
	h.keys("r")
	h.waitFor("Error:", "AuthFailure")

	h.enter()     // details
	h.keys("dyn") // details, copy ID, subnets
	time.Sleep(time.Second)
	text := h.screenText()
	for _, unwanted := range []string{"View subnets", "Copied", "Subnets"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("the error screen shows %q after a row key. Frame:\n%s", unwanted, text)
		}
	}
}

// failingProxy forwards to LocalStack until the test sets the flag. Then
// it answers every call with an EC2 AuthFailure, which the SDK does not
// retry.
func failingProxy(t *testing.T) (string, *atomic.Bool) {
	t.Helper()
	ep := endpoint()
	requireLocalStack(t, ep)
	target, err := url.Parse(ep)
	if err != nil {
		t.Fatal(err)
	}
	forward := httputil.NewSingleHostReverseProxy(target)
	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !fail.Load() {
			forward.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/xml")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`<Response><Errors><Error><Code>AuthFailure</Code>` +
			`<Message>smoke proxy refuses the call</Message></Error></Errors><RequestID>smoke</RequestID></Response>`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &fail
}
