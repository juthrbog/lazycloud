package aws

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeObject is one object in fakeS3.
type fakeObject struct {
	body         []byte
	storageClass string
	contentType  string
	color        string // the x-amz-meta-color header
}

// fakeS3 is an in-memory, path-style S3 endpoint for the copy, move,
// delete, download, and version tests. Objects are keyed by "bucket/key".
type fakeS3 struct {
	mu      sync.Mutex
	objects map[string]fakeObject
	heads   map[string]int
	// hideHead makes HEAD answer 404 for these objects, as when another
	// program creates the object between the check and the copy.
	hideHead map[string]bool

	copySources []string // the raw x-amz-copy-source of each CopyObject
	copyHeaders []http.Header

	// Multipart copy.
	failPart       int // UploadPartCopy fails for this part number
	ranges         []string
	parts          map[int][]byte
	createHeader   http.Header
	completeHeader http.Header
	completed      int
	aborted        int

	// DeleteObjects and DeleteObject.
	deleteFail     map[string]bool // reported as failed in the response, or a 403 for DeleteObject
	failDeleteCall int             // this call (1 is the first) fails as a whole
	deleteCalls    int
	batchKeys      []string // the keys in each DeleteObjects body
	singleDeletes  []string // the key of each DeleteObject

	// truncate makes GET send half of the body and then drop the connection.
	truncate bool

	// versions is the version listing of bucket "b", sorted by key.
	versions   []ObjectVersion
	lists      int      // ListObjectsV2 and ListObjectVersions calls
	keyMarkers []string // the key-marker of each ListObjectVersions call
	// ignoreMarkers makes ListObjectVersions start at the first entry, as
	// LocalStack does when URL encoding changes the key.
	ignoreMarkers bool
}

func newFakeS3(t *testing.T) (*fakeS3, *S3ServiceImpl) {
	t.Helper()
	f := &fakeS3{
		objects:    map[string]fakeObject{},
		heads:      map[string]int{},
		hideHead:   map[string]bool{},
		deleteFail: map[string]bool{},
	}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	c, err := NewClient("", "us-east-1", srv.URL)
	require.NoError(t, err)
	return f, NewS3Service(c)
}

func (f *fakeS3) object(id string) (fakeObject, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.objects[id]
	return o, ok
}

func writeS3Error(w http.ResponseWriter, status int, code string) {
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `<Error><Code>%s</Code><Message>%s</Message></Error>`, code, code)
}

func (f *fakeS3) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	q := r.URL.Query()
	bucket, key, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	id := bucket + "/" + key

	switch {
	case r.Method == http.MethodPost && q.Has("delete"):
		f.deleteObjects(w, r, bucket)

	case r.Method == http.MethodGet && q.Has("versions"):
		f.listVersions(w, q)

	case r.Method == http.MethodGet && q.Get("list-type") == "2":
		f.listObjects(w, bucket, q)

	case r.Method == http.MethodHead:
		f.heads[id]++
		o, ok := f.objects[id]
		if !ok || f.hideHead[id] {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(o.body)))
		w.Header().Set("Content-Type", o.contentType)
		if o.storageClass != "" {
			w.Header().Set("X-Amz-Storage-Class", o.storageClass)
		}
		if o.color != "" {
			w.Header().Set("X-Amz-Meta-Color", o.color)
		}

	case r.Method == http.MethodGet:
		o, ok := f.objects[id]
		if !ok {
			writeS3Error(w, http.StatusNotFound, "NoSuchKey")
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(o.body)))
		if f.truncate {
			_, _ = w.Write(o.body[:len(o.body)/2])
			w.(http.Flusher).Flush()
			panic(http.ErrAbortHandler)
		}
		_, _ = w.Write(o.body)

	case r.Method == http.MethodPost && q.Has("uploads"):
		f.createHeader = r.Header.Clone()
		f.parts = map[int][]byte{}
		_, _ = w.Write([]byte(`<InitiateMultipartUploadResult><UploadId>u1</UploadId></InitiateMultipartUploadResult>`))

	case r.Method == http.MethodPut && q.Has("uploadId"):
		n, _ := strconv.Atoi(q.Get("partNumber"))
		if n == f.failPart {
			writeS3Error(w, http.StatusForbidden, "AccessDenied")
			return
		}
		src, ok := f.source(r)
		if !ok {
			writeS3Error(w, http.StatusNotFound, "NoSuchKey")
			return
		}
		rng := r.Header.Get("X-Amz-Copy-Source-Range")
		f.ranges = append(f.ranges, rng)
		var start, end int
		_, _ = fmt.Sscanf(rng, "bytes=%d-%d", &start, &end)
		f.parts[n] = src.body[start : end+1]
		_, _ = fmt.Fprintf(w, `<CopyPartResult><ETag>"p%d"</ETag></CopyPartResult>`, n)

	case r.Method == http.MethodPost && q.Has("uploadId"):
		f.completeHeader = r.Header.Clone()
		var req struct {
			Parts []struct {
				PartNumber int `xml:"PartNumber"`
			} `xml:"Part"`
		}
		if err := xml.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if _, exists := f.objects[id]; exists && r.Header.Get("If-None-Match") == "*" {
			writeS3Error(w, http.StatusPreconditionFailed, "PreconditionFailed")
			return
		}
		var body []byte
		for _, p := range req.Parts {
			body = append(body, f.parts[p.PartNumber]...)
		}
		f.objects[id] = fakeObject{
			body:         body,
			storageClass: f.createHeader.Get("X-Amz-Storage-Class"),
			contentType:  f.createHeader.Get("Content-Type"),
			color:        f.createHeader.Get("X-Amz-Meta-Color"),
		}
		f.completed++
		_, _ = w.Write([]byte(`<CompleteMultipartUploadResult><ETag>"m"</ETag></CompleteMultipartUploadResult>`))

	case r.Method == http.MethodDelete && q.Has("uploadId"):
		f.aborted++
		w.WriteHeader(http.StatusNoContent)

	case r.Method == http.MethodPut && r.Header.Get("X-Amz-Copy-Source") != "":
		f.copySources = append(f.copySources, r.Header.Get("X-Amz-Copy-Source"))
		f.copyHeaders = append(f.copyHeaders, r.Header.Clone())
		src, ok := f.source(r)
		if !ok {
			writeS3Error(w, http.StatusNotFound, "NoSuchKey")
			return
		}
		if _, exists := f.objects[id]; exists && r.Header.Get("If-None-Match") == "*" {
			writeS3Error(w, http.StatusPreconditionFailed, "PreconditionFailed")
			return
		}
		src.storageClass = r.Header.Get("X-Amz-Storage-Class")
		f.objects[id] = src
		_, _ = w.Write([]byte(`<CopyObjectResult><ETag>"c"</ETag></CopyObjectResult>`))

	case r.Method == http.MethodDelete:
		f.singleDeletes = append(f.singleDeletes, key)
		if f.deleteFail[key] {
			writeS3Error(w, http.StatusForbidden, "AccessDenied")
			return
		}
		delete(f.objects, id)
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "unexpected "+r.Method+" "+r.URL.String(), http.StatusNotImplemented)
	}
}

// source decodes x-amz-copy-source as S3 does: URL-decode, then split the
// bucket from the key at the first slash.
func (f *fakeS3) source(r *http.Request) (fakeObject, bool) {
	raw, err := url.PathUnescape(r.Header.Get("X-Amz-Copy-Source"))
	if err != nil {
		return fakeObject{}, false
	}
	o, ok := f.objects[strings.TrimPrefix(raw, "/")]
	return o, ok
}

func (f *fakeS3) deleteObjects(w http.ResponseWriter, r *http.Request, bucket string) {
	f.deleteCalls++
	if f.deleteCalls == f.failDeleteCall {
		writeS3Error(w, http.StatusForbidden, "AccessDenied")
		return
	}
	var req struct {
		Objects []struct {
			Key string `xml:"Key"`
		} `xml:"Object"`
	}
	if err := xml.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var b strings.Builder
	b.WriteString(`<DeleteResult>`)
	for _, o := range req.Objects {
		f.batchKeys = append(f.batchKeys, o.Key)
		if f.deleteFail[o.Key] {
			fmt.Fprintf(&b, `<Error><Key>%s</Key><Code>AccessDenied</Code><Message>Access Denied</Message></Error>`, o.Key)
			continue
		}
		delete(f.objects, bucket+"/"+o.Key)
	}
	b.WriteString(`</DeleteResult>`)
	_, _ = w.Write([]byte(b.String()))
}

// maxLists stops a listing loop in a test that went wrong.
const maxLists = 50

// encodeFor returns how a listing writes a key. With encoding-type=url, it
// URL-encodes the key, as S3 does. Otherwise it writes the key as XML text
// with its control characters, as LocalStack does, so the SDK fails to read
// a key with ESC.
func encodeFor(q url.Values) func(string) string {
	if q.Get("encoding-type") == "url" {
		return url.QueryEscape
	}
	return xmlText.Replace
}

var xmlText = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// listObjects answers ListObjectsV2 for one bucket as S3 does: prefix and
// delimiter, max-keys entries per page (1,000 by default), and a
// continuation token. A common prefix counts as one entry.
func (f *fakeS3) listObjects(w http.ResponseWriter, bucket string, q url.Values) {
	f.lists++
	if f.lists > maxLists {
		writeS3Error(w, http.StatusBadRequest, "TooManyLists")
		return
	}
	prefix, delim := q.Get("prefix"), q.Get("delimiter")
	maxKeys := 1000
	if n, err := strconv.Atoi(q.Get("max-keys")); err == nil {
		maxKeys = n
	}
	var keys []string
	for id := range f.objects {
		if k, ok := strings.CutPrefix(id, bucket+"/"); ok && strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	type entry struct {
		name     string
		isPrefix bool
	}
	var entries []entry
	for _, k := range keys {
		if i := strings.Index(k[len(prefix):], delim); delim != "" && i >= 0 {
			p := k[:len(prefix)+i+len(delim)]
			if len(entries) == 0 || entries[len(entries)-1].name != p {
				entries = append(entries, entry{p, true})
			}
			continue
		}
		entries = append(entries, entry{k, false})
	}

	start, _ := strconv.Atoi(q.Get("continuation-token"))
	end := min(start+maxKeys, len(entries))
	enc := encodeFor(q)
	var b strings.Builder
	fmt.Fprintf(&b, `<ListBucketResult><Name>%s</Name><IsTruncated>%t</IsTruncated>`, bucket, end < len(entries))
	if end < len(entries) {
		fmt.Fprintf(&b, `<NextContinuationToken>%d</NextContinuationToken>`, end)
	}
	for _, e := range entries[start:end] {
		if e.isPrefix {
			fmt.Fprintf(&b, `<CommonPrefixes><Prefix>%s</Prefix></CommonPrefixes>`, enc(e.name))
			continue
		}
		fmt.Fprintf(&b, `<Contents><Key>%s</Key><Size>%d</Size></Contents>`, enc(e.name), len(f.objects[bucket+"/"+e.name].body))
	}
	b.WriteString(`</ListBucketResult>`)
	_, _ = w.Write([]byte(b.String()))
}

// listVersions pages through f.versions as S3 does: prefix filter, key and
// version markers, and max-keys entries per page (1,000 by default).
func (f *fakeS3) listVersions(w http.ResponseWriter, q url.Values) {
	f.lists++
	if f.lists > maxLists {
		writeS3Error(w, http.StatusBadRequest, "TooManyLists")
		return
	}
	prefix, keyMarker, idMarker := q.Get("prefix"), q.Get("key-marker"), q.Get("version-id-marker")
	f.keyMarkers = append(f.keyMarkers, keyMarker)
	maxKeys := 1000
	if n, err := strconv.Atoi(q.Get("max-keys")); err == nil {
		maxKeys = n
	}
	var match []ObjectVersion
	for _, v := range f.versions {
		if strings.HasPrefix(v.Key, prefix) {
			match = append(match, v)
		}
	}
	start := 0
	if keyMarker != "" && !f.ignoreMarkers {
		start = sort.Search(len(match), func(i int) bool { return match[i].Key > keyMarker })
		for i, v := range match {
			if v.Key == keyMarker && v.VersionID == idMarker {
				start = i + 1
			}
		}
	}
	end := min(start+maxKeys, len(match))
	page := match[start:end]

	enc := encodeFor(q)
	var b strings.Builder
	fmt.Fprintf(&b, `<ListVersionsResult><Name>b</Name><IsTruncated>%t</IsTruncated>`, end < len(match))
	if end < len(match) {
		last := page[len(page)-1]
		fmt.Fprintf(&b, `<NextKeyMarker>%s</NextKeyMarker><NextVersionIdMarker>%s</NextVersionIdMarker>`, enc(last.Key), last.VersionID)
	}
	for _, v := range page {
		tag := "Version"
		if v.IsDeleteMarker {
			tag = "DeleteMarker"
		}
		fmt.Fprintf(&b, `<%s><Key>%s</Key><VersionId>%s</VersionId></%s>`, tag, enc(v.Key), v.VersionID, tag)
	}
	b.WriteString(`</ListVersionsResult>`)
	_, _ = w.Write([]byte(b.String()))
}

// --- M1: DeleteObjects ---

func TestDeleteObjectsReportsFailedKeys(t *testing.T) {
	f, svc := newFakeS3(t)
	for _, k := range []string{"a", "b", "c"} {
		f.objects["b/"+k] = fakeObject{body: []byte(k)}
	}
	f.deleteFail["b"] = true

	err := svc.DeleteObjects(context.Background(), "b", []string{"a", "b", "c"})

	var delErr *DeleteObjectsError
	require.ErrorAs(t, err, &delErr)
	assert.Equal(t, []KeyFailure{{Key: "b", Code: "AccessDenied", Message: "Access Denied"}}, delErr.Failed)
	assert.Equal(t, "DeleteObjects: 1 key(s) not deleted", err.Error())
	_, ok := f.object("b/a")
	assert.False(t, ok, "a is deleted")
	_, ok = f.object("b/b")
	assert.True(t, ok, "b stays")
}

func TestDeleteObjectsGoesOnAfterAFailedBatch(t *testing.T) {
	f, svc := newFakeS3(t)
	keys := make([]string, 1500)
	for i := range keys {
		keys[i] = fmt.Sprintf("k%04d", i)
		f.objects["b/"+keys[i]] = fakeObject{}
	}
	f.failDeleteCall = 1

	err := svc.DeleteObjects(context.Background(), "b", keys)

	var delErr *DeleteObjectsError
	require.ErrorAs(t, err, &delErr)
	assert.Len(t, delErr.Failed, 1000, "every key of the failed batch")
	assert.Equal(t, "k0000", delErr.Failed[0].Key)
	assert.Equal(t, "AccessDenied", delErr.Failed[0].Code)
	assert.Equal(t, 2, f.deleteCalls, "the second batch still runs")
	assert.Len(t, f.objects, 1000, "the second batch is deleted")
}

func TestDeleteObjectsAllDeleted(t *testing.T) {
	f, svc := newFakeS3(t)
	f.objects["b/a"] = fakeObject{}
	require.NoError(t, svc.DeleteObjects(context.Background(), "b", []string{"a"}))
	assert.Empty(t, f.objects)
}

// --- M2: CopyObject and MoveObject ---

func TestCopySourceEncodesTheKey(t *testing.T) {
	assert.Equal(t, "b/dir/a%20b%2Bc%25d%3Fe%23f%20%C3%A9.txt", copySource("b", "dir/a b+c%d?e#f é.txt"))
	assert.Equal(t, "my-bucket/plain/key_1.~txt", copySource("my-bucket", "plain/key_1.~txt"))
}

// M2(a): the copy source was not encoded, so S3 read a key with "%", "+",
// or "?" as a different key.
func TestCopyObjectWithSpecialCharacters(t *testing.T) {
	f, svc := newFakeS3(t)
	src := "dir/a b+c%d?e#f é.txt"
	f.objects["b/"+src] = fakeObject{body: []byte("data")}

	require.NoError(t, svc.CopyObject(context.Background(), "b", src, "b", "dst.txt", false))

	o, ok := f.object("b/dst.txt")
	require.True(t, ok)
	assert.Equal(t, "data", string(o.body))
	assert.Equal(t, []string{"b/dir/a%20b%2Bc%25d%3Fe%23f%20%C3%A9.txt"}, f.copySources)
}

// M2(b): a copy replaced an existing destination without a word.
func TestCopyObjectRefusesAnExistingDestination(t *testing.T) {
	f, svc := newFakeS3(t)
	f.objects["b/src"] = fakeObject{body: []byte("new")}
	f.objects["b/dst"] = fakeObject{body: []byte("old")}

	err := svc.CopyObject(context.Background(), "b", "src", "b", "dst", false)

	require.ErrorIs(t, err, ErrDestinationExists)
	assert.Empty(t, f.copySources, "no copy request")
	o, _ := f.object("b/dst")
	assert.Equal(t, "old", string(o.body))
}

// Another program can create the destination after the check. S3 then
// refuses the copy because of If-None-Match.
func TestCopyObjectMapsPreconditionFailed(t *testing.T) {
	f, svc := newFakeS3(t)
	f.objects["b/src"] = fakeObject{body: []byte("new")}
	f.objects["b/dst"] = fakeObject{body: []byte("old")}
	f.hideHead["b/dst"] = true

	err := svc.CopyObject(context.Background(), "b", "src", "b", "dst", false)

	require.ErrorIs(t, err, ErrDestinationExists)
	require.Len(t, f.copyHeaders, 1)
	assert.Equal(t, "*", f.copyHeaders[0].Get("If-None-Match"))
	o, _ := f.object("b/dst")
	assert.Equal(t, "old", string(o.body))
}

func TestCopyObjectOverwrite(t *testing.T) {
	f, svc := newFakeS3(t)
	f.objects["b/src"] = fakeObject{body: []byte("new")}
	f.objects["b/dst"] = fakeObject{body: []byte("old")}

	require.NoError(t, svc.CopyObject(context.Background(), "b", "src", "b", "dst", true))

	o, _ := f.object("b/dst")
	assert.Equal(t, "new", string(o.body))
	assert.Zero(t, f.heads["b/dst"], "no check when the user chose to overwrite")
	assert.Empty(t, f.copyHeaders[0].Get("If-None-Match"))
}

// M2(e): a copy without a storage class wrote STANDARD.
func TestCopyObjectKeepsTheStorageClass(t *testing.T) {
	f, svc := newFakeS3(t)
	f.objects["b/src"] = fakeObject{body: []byte("x"), storageClass: "STANDARD_IA"}

	require.NoError(t, svc.CopyObject(context.Background(), "b", "src", "b", "dst", false))

	o, _ := f.object("b/dst")
	assert.Equal(t, "STANDARD_IA", o.storageClass)
}

func TestCopyObjectMissingSource(t *testing.T) {
	f, svc := newFakeS3(t)
	err := svc.CopyObject(context.Background(), "b", "gone", "b", "dst", false)
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrDestinationExists)
	assert.Contains(t, err.Error(), "reading the source")
	assert.Empty(t, f.copySources)
}

// smallMultipart makes every object over 5 bytes a multipart copy with
// 4-byte parts.
func smallMultipart(t *testing.T) {
	t.Helper()
	threshold, part := multipartCopyThreshold, copyPartSize
	multipartCopyThreshold, copyPartSize = 5, 4
	t.Cleanup(func() { multipartCopyThreshold, copyPartSize = threshold, part })
}

// M2(d): one CopyObject call cannot copy an object over 5 GB.
func TestCopyObjectMultipart(t *testing.T) {
	smallMultipart(t)
	f, svc := newFakeS3(t)
	f.objects["b/big"] = fakeObject{
		body:         []byte("0123456789"),
		storageClass: "STANDARD_IA",
		contentType:  "text/csv",
		color:        "blue",
	}

	require.NoError(t, svc.CopyObject(context.Background(), "b", "big", "b", "dst", false))

	assert.Equal(t, []string{"bytes=0-3", "bytes=4-7", "bytes=8-9"}, f.ranges)
	assert.Empty(t, f.copySources, "no single CopyObject call")
	o, ok := f.object("b/dst")
	require.True(t, ok)
	assert.Equal(t, "0123456789", string(o.body))
	assert.Equal(t, "STANDARD_IA", o.storageClass)
	assert.Equal(t, "text/csv", o.contentType)
	assert.Equal(t, "blue", o.color, "the metadata is set on the new upload")
	assert.Equal(t, "*", f.completeHeader.Get("If-None-Match"))
	assert.Zero(t, f.aborted)
}

func TestCopyObjectMultipartAbortsOnError(t *testing.T) {
	smallMultipart(t)
	f, svc := newFakeS3(t)
	f.objects["b/big"] = fakeObject{body: []byte("0123456789")}
	f.failPart = 2

	err := svc.CopyObject(context.Background(), "b", "big", "b", "dst", false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "copying part 2")
	assert.Equal(t, 1, f.aborted, "no incomplete parts stay")
	assert.Zero(t, f.completed)
	_, ok := f.object("b/dst")
	assert.False(t, ok)
}

func TestCopyObjectMultipartMapsPreconditionFailed(t *testing.T) {
	smallMultipart(t)
	f, svc := newFakeS3(t)
	f.objects["b/big"] = fakeObject{body: []byte("0123456789")}
	f.objects["b/dst"] = fakeObject{body: []byte("old")}
	f.hideHead["b/dst"] = true

	err := svc.CopyObject(context.Background(), "b", "big", "b", "dst", false)

	require.ErrorIs(t, err, ErrDestinationExists)
	assert.Equal(t, 1, f.aborted)
	o, _ := f.object("b/dst")
	assert.Equal(t, "old", string(o.body))
}

// A move that stops at an existing destination keeps the source.
func TestMoveObjectKeepsTheSourceWhenTheDestinationExists(t *testing.T) {
	f, svc := newFakeS3(t)
	f.objects["b/src"] = fakeObject{body: []byte("new")}
	f.objects["b/dst"] = fakeObject{body: []byte("old")}

	err := svc.MoveObject(context.Background(), "b", "src", "b", "dst", false)

	require.ErrorIs(t, err, ErrDestinationExists)
	_, ok := f.object("b/src")
	assert.True(t, ok)
}

func TestMoveObjectOverwrite(t *testing.T) {
	f, svc := newFakeS3(t)
	f.objects["b/src"] = fakeObject{body: []byte("new")}
	f.objects["b/dst"] = fakeObject{body: []byte("old")}

	require.NoError(t, svc.MoveObject(context.Background(), "b", "src", "b", "dst", true))

	_, ok := f.object("b/src")
	assert.False(t, ok)
	o, _ := f.object("b/dst")
	assert.Equal(t, "new", string(o.body))
}

// --- M25: DownloadObject ---

func TestDownloadObjectNeverOverwrites(t *testing.T) {
	f, svc := newFakeS3(t)
	f.objects["b/dir/report.pdf"] = fakeObject{body: []byte("pdf")}
	dest := filepath.Join(t.TempDir(), "sub", "report.pdf")
	require.NoError(t, os.MkdirAll(filepath.Dir(dest), 0o750))
	require.NoError(t, os.WriteFile(dest, []byte("mine"), 0o600))

	got1, err := svc.DownloadObject(context.Background(), "b", "dir/report.pdf", dest)
	require.NoError(t, err)
	got2, err := svc.DownloadObject(context.Background(), "b", "dir/report.pdf", dest)
	require.NoError(t, err)

	dir := filepath.Dir(dest)
	assert.Equal(t, filepath.Join(dir, "report (1).pdf"), got1)
	assert.Equal(t, filepath.Join(dir, "report (2).pdf"), got2)
	mine, err := os.ReadFile(dest) //nolint:gosec // a file in t.TempDir()
	require.NoError(t, err)
	assert.Equal(t, "mine", string(mine), "the existing file is not touched")
	data, err := os.ReadFile(got1) //nolint:gosec // a file in t.TempDir()
	require.NoError(t, err)
	assert.Equal(t, "pdf", string(data))
	info, err := os.Stat(got1)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestDownloadObjectFreeNames(t *testing.T) {
	f, svc := newFakeS3(t)
	f.objects["b/k"] = fakeObject{body: []byte("x")}
	dir := t.TempDir()
	for _, tc := range []struct{ name, want string }{
		{"README", "README (1)"},
		{".env", ".env (1)"},
		{"archive.tar.gz", "archive.tar (1).gz"},
	} {
		dest := filepath.Join(dir, tc.name)
		require.NoError(t, os.WriteFile(dest, nil, 0o600))
		got, err := svc.DownloadObject(context.Background(), "b", "k", dest)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(dir, tc.want), got)
	}
}

func TestDownloadObjectRemovesAPartialFile(t *testing.T) {
	f, svc := newFakeS3(t)
	f.objects["b/k"] = fakeObject{body: []byte(strings.Repeat("x", 4096))}
	f.truncate = true
	dir := t.TempDir()

	got, err := svc.DownloadObject(context.Background(), "b", "k", filepath.Join(dir, "k.bin"))

	require.Error(t, err)
	assert.Empty(t, got)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries, "no partial file stays")
}

func TestDefaultDownloadPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	downloads := filepath.Join(home, "Downloads")
	assert.Equal(t, filepath.Join(downloads, "c.txt"), DefaultDownloadPath("a/b/c.txt"))
	assert.Equal(t, filepath.Join(downloads, "download"), DefaultDownloadPath("a/.."))
	assert.Equal(t, filepath.Join(downloads, "download"), DefaultDownloadPath(".."))
	assert.Equal(t, filepath.Join(downloads, "download"), DefaultDownloadPath("."))
}

// --- M26: ListObjectVersions ---

// addVersions adds n entries for key. Every third one is a delete marker.
func (f *fakeS3) addVersions(key string, n int) {
	for i := range n {
		f.versions = append(f.versions, ObjectVersion{
			Key:            key,
			VersionID:      fmt.Sprintf("v%05d", i),
			IsDeleteMarker: i%3 == 2,
		})
	}
	sort.SliceStable(f.versions, func(i, j int) bool { return f.versions[i].Key < f.versions[j].Key })
}

// The listing stopped at 1,000 entries, and a page with a sibling key such
// as "k.bak" is the last page that can hold versions of "k".
func TestListObjectVersionsReadsEveryPageOfTheKey(t *testing.T) {
	f, svc := newFakeS3(t)
	f.addVersions("j", 10)
	f.addVersions("k", 2500)
	f.addVersions("k.bak", 1500)

	versions, err := svc.ListObjectVersions(context.Background(), "b", "k")

	require.NoError(t, err)
	require.Len(t, versions, 2500)
	markers := 0
	for _, v := range versions {
		assert.Equal(t, "k", v.Key)
		if v.IsDeleteMarker {
			markers++
		}
	}
	assert.Equal(t, 833, markers)
	assert.Equal(t, 3, f.lists, "the third page holds k.bak, so no fourth page")
}

func TestListObjectVersionsWithoutSiblings(t *testing.T) {
	f, svc := newFakeS3(t)
	f.addVersions("k", 1200)

	versions, err := svc.ListObjectVersions(context.Background(), "b", "k")

	require.NoError(t, err)
	assert.Len(t, versions, 1200)
	assert.Equal(t, 2, f.lists)
}

func TestListObjectVersionsSkipsSiblings(t *testing.T) {
	f, svc := newFakeS3(t)
	f.addVersions("k", 3)
	f.addVersions("k/child", 2)
	f.addVersions("k.bak", 2)

	versions, err := svc.ListObjectVersions(context.Background(), "b", "k")

	require.NoError(t, err)
	assert.Len(t, versions, 3)
	assert.Equal(t, 1, f.lists)
}

func TestListObjectVersionsEmpty(t *testing.T) {
	f, svc := newFakeS3(t)
	f.addVersions("k.bak", 2)

	versions, err := svc.ListObjectVersions(context.Background(), "b", "k")

	require.NoError(t, err)
	assert.Empty(t, versions)
	assert.Equal(t, 1, f.lists)
}
