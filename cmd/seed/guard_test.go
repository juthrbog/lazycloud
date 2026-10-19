package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// M6: with an empty endpoint, the seed tool used the normal credential
// chain and created real resources.
func TestCheckEndpoint(t *testing.T) {
	tests := []struct {
		endpoint    string
		allowRemote bool
		ok          bool
	}{
		{"http://localhost:4566", false, true},
		{"http://127.0.0.1:4566", false, true},
		{"http://[::1]:4566", false, true},
		{"http://localstack:4566", false, true},
		{"https://localhost.localstack.cloud:4566", false, true},
		{"http://s3.localhost.localstack.cloud:4566", false, true},
		{"", false, false},
		{"   ", false, false},
		{"localhost:4566", false, false}, // no scheme
		{"ftp://localhost:4566", false, false},
		{"https://s3.us-east-1.amazonaws.com", false, false},
		{"http://10.0.0.5:4566", false, false},
		{"http://10.0.0.5:4566", true, true},
		{"", true, false}, // --allow-remote never accepts an empty endpoint
	}
	for _, tc := range tests {
		err := checkEndpoint(tc.endpoint, tc.allowRemote)
		if tc.ok {
			require.NoError(t, err, "%q allowRemote=%v", tc.endpoint, tc.allowRemote)
		} else {
			require.Error(t, err, "%q allowRemote=%v", tc.endpoint, tc.allowRemote)
		}
	}
	// An empty endpoint, for example from an unset variable, gets its own
	// message, because it is the case that used to reach real AWS.
	assert.ErrorContains(t, checkEndpoint("", true), "--endpoint is empty")
}

func TestCheckLocalStack(t *testing.T) {
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_localstack/health" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"services": {"s3": "running", "sqs": "available"}, "edition": "pro"}`))
	}))
	defer healthy.Close()
	assert.NoError(t, checkLocalStack(context.Background(), healthy.URL))
	assert.NoError(t, checkLocalStack(context.Background(), healthy.URL+"/"), "a trailing slash is fine")

	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status": "ok"}`))
	}))
	defer other.Close()
	err := checkLocalStack(context.Background(), other.URL)
	require.Error(t, err, "a server without a LocalStack health report")
	assert.Contains(t, err.Error(), "no LocalStack")

	missing := httptest.NewServer(http.NotFoundHandler())
	defer missing.Close()
	require.Error(t, checkLocalStack(context.Background(), missing.URL))

	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	assert.Error(t, checkLocalStack(context.Background(), closed.URL))
}

// The -wipe request had no timeout.
func TestWipeStateTimesOut(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
	}))
	defer slow.Close()
	old := httpClient.Timeout
	httpClient.Timeout = 50 * time.Millisecond
	defer func() { httpClient.Timeout = old }()

	start := time.Now()
	require.Error(t, wipeState(context.Background(), slow.URL))
	assert.Less(t, time.Since(start), 400*time.Millisecond)
}
