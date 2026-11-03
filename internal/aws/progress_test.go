package aws

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/ops"
)

// M25: long S3 work reports its progress to the operation that runs it.

func TestDownloadReportsProgress(t *testing.T) {
	f, svc := newFakeS3(t)
	body := strings.Repeat("x", 100_000)
	f.objects["b/k"] = fakeObject{body: []byte(body)}
	reg := ops.NewRegistry()
	op := reg.Begin("Download k", "")
	defer op.Done()

	_, err := svc.DownloadObject(op.Ctx, "b", "k", filepath.Join(t.TempDir(), "k"))
	require.NoError(t, err)
	p := reg.Running()[0].Progress
	assert.True(t, p.Bytes)
	assert.Equal(t, int64(len(body)), p.Done)
	assert.Equal(t, int64(len(body)), p.Total, "the total comes from Content-Length")
}

// A cancel during the body read must come back as a cancel. The HTTP
// client returns the cause of the context there, not context.Canceled, so
// the view showed "writing file: cancelled by the user" as an error.
func TestDownloadCancelIsACancel(t *testing.T) {
	c := newHookClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		_, _ = w.Write([]byte(strings.Repeat("x", 10)))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	reg := ops.NewRegistry()
	op := reg.Begin("Download k", "")
	defer op.Done()
	dir := t.TempDir()

	errc := make(chan error, 1)
	go func() {
		_, err := NewS3Service(c).DownloadObject(op.Ctx, "b", "k", filepath.Join(dir, "k"))
		errc <- err
	}()
	require.Eventually(t, func() bool {
		return reg.Running()[0].Progress.Done == 10
	}, 5*time.Second, time.Millisecond)
	assert.Equal(t, int64(1000), reg.Running()[0].Progress.Total)
	reg.Cancel(op.ID)

	err := <-errc
	require.ErrorIs(t, err, context.Canceled)
	entries, rerr := os.ReadDir(dir)
	require.NoError(t, rerr)
	assert.Empty(t, entries, "the partial file is gone")
}

func TestDeleteObjectsReportsProgress(t *testing.T) {
	f, svc := newFakeS3(t)
	keys := make([]string, 1500) // two batches
	for i := range keys {
		keys[i] = fmt.Sprintf("k%04d", i)
		f.objects["b/"+keys[i]] = fakeObject{}
	}
	reg := ops.NewRegistry()
	op := reg.Begin("Delete", "")
	defer op.Done()

	require.NoError(t, svc.DeleteObjects(op.Ctx, "b", keys))
	assert.Equal(t, ops.Progress{Done: 1500, Total: 1500}, reg.Running()[0].Progress)
}
