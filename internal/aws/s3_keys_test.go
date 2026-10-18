package aws

import (
	"context"
	"fmt"
	"sort"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- M28: keys that XML 1.0 cannot hold ---

// oddKeys are keys that a listing without EncodingType url breaks or
// changes: ESC fails the XML, a CR comes back as LF, and "+", "%", and a
// space must survive the URL decoding.
var oddKeys = []string{
	"plain.txt",
	"a b.txt",
	"a+b.txt",
	"50% off.txt",
	"esc-\x1b.txt",
	"cr-a\rb.txt",
	"lf-a\nb.txt",
	"tab-a\tb.txt",
	"c1-\u009b.txt",
	"amp-&<>.txt",
	"ü-é.txt",
}

func TestListObjectsPageDecodesKeysAndPrefixes(t *testing.T) {
	f, svc := newFakeS3(t)
	for _, k := range oddKeys {
		f.objects["b/"+k] = fakeObject{body: []byte("x")}
	}
	f.objects["b/dir x/inner+1.txt"] = fakeObject{}
	f.objects["b/dir-\x1b/inner.txt"] = fakeObject{}

	page, err := svc.ListObjectsPage(context.Background(), "b", "", nil)

	require.NoError(t, err)
	var got []string
	for _, o := range page.Objects {
		got = append(got, o.Key)
	}
	want := append([]string(nil), oddKeys...)
	sort.Strings(want)
	assert.Equal(t, want, got)
	assert.Equal(t, []string{"dir x/", "dir-\x1b/"}, page.Prefixes)
	assert.Equal(t, int64(1), page.Objects[0].Size)
}

func TestListObjectsPageInAFolderWithAnOddName(t *testing.T) {
	f, svc := newFakeS3(t)
	f.objects["b/dir-\x1b/"] = fakeObject{} // the folder marker
	f.objects["b/dir-\x1b/in 1+1.txt"] = fakeObject{}
	f.objects["b/dir-\x1b/sub/deep.txt"] = fakeObject{}

	page, err := svc.ListObjectsPage(context.Background(), "b", "dir-\x1b/", nil)

	require.NoError(t, err)
	require.Len(t, page.Objects, 1, "the folder marker is not an object in its own folder")
	assert.Equal(t, "dir-\x1b/in 1+1.txt", page.Objects[0].Key)
	assert.Equal(t, []string{"dir-\x1b/sub/"}, page.Prefixes)
}

func TestListAllKeysDecodesEveryPage(t *testing.T) {
	f, svc := newFakeS3(t)
	var want []string
	for i := range 2500 {
		k := fmt.Sprintf("f/%04d-%s", i, oddKeys[i%len(oddKeys)])
		f.objects["b/"+k] = fakeObject{}
		want = append(want, k)
	}
	sort.Strings(want)

	keys, err := svc.ListAllKeys(context.Background(), "b", "f/")

	require.NoError(t, err)
	assert.Equal(t, want, keys)
	assert.Equal(t, 3, f.lists)
}

// The fake encodes NextKeyMarker, as S3 does. LocalStack does not. The
// function sends the key itself as the marker, so both work.
func TestListObjectVersionsWithAnOddKey(t *testing.T) {
	const key = "50% off+\x1b\r.txt"
	f, svc := newFakeS3(t)
	f.addVersions(key, 2500)
	f.addVersions(key+".bak", 10)

	versions, err := svc.ListObjectVersions(context.Background(), "b", key)

	require.NoError(t, err)
	require.Len(t, versions, 2500)
	for _, v := range versions {
		assert.Equal(t, key, v.Key)
	}
	assert.Equal(t, []string{"", key, key}, f.keyMarkers)
}

// LocalStack ignores the marker when URL encoding changes the key, so the
// next page is the first page again. The function stops instead of looping.
func TestListObjectVersionsStopsWhenThePageRepeats(t *testing.T) {
	f, svc := newFakeS3(t)
	f.addVersions("a b.txt", 1500)
	f.ignoreMarkers = true

	_, err := svc.ListObjectVersions(context.Background(), "b", "a b.txt")

	require.EqualError(t, err, "the server sent the same page of versions again")
	assert.Equal(t, 2, f.lists)
}

func TestDecodeKey(t *testing.T) {
	k, err := decodeKey(aws.String("a+b%2B%20%1B%0D"))
	require.NoError(t, err)
	assert.Equal(t, "a b+ \x1b\r", k)

	_, err = decodeKey(aws.String("50% off"))
	assert.EqualError(t, err, `decoding the key "50% off": invalid URL escape "% o"`)
}

func TestXMLSafe(t *testing.T) {
	for _, k := range []string{"plain", "a b+c%", "cr\r", "lf\n", "tab\t", "c1-\u0085\u009b", "ü", "�", "&<>"} {
		assert.True(t, xmlSafe(k), "%q", k)
	}
	for _, k := range []string{"esc-\x1b", "nul-\x00", "bs-\b", "ff-\f", "us-\x1f", "￾", "￿", "bad-\xff"} {
		assert.False(t, xmlSafe(k), "%q", k)
	}
}

// The SDK writes U+FFFD for ESC in a DeleteObjects body, so S3 deletes a
// different key and reports success. Such a key is deleted alone.
func TestDeleteObjectsDeletesUnsafeKeysAlone(t *testing.T) {
	f, svc := newFakeS3(t)
	keys := []string{"plain", "esc-\x1b", "cr-a\rb", "ffff-￿"}
	for _, k := range keys {
		f.objects["b/"+k] = fakeObject{}
	}

	require.NoError(t, svc.DeleteObjects(context.Background(), "b", keys))

	assert.Empty(t, f.objects)
	assert.Equal(t, []string{"plain", "cr-a\rb"}, f.batchKeys)
	assert.Equal(t, []string{"esc-\x1b", "ffff-￿"}, f.singleDeletes)
}

func TestDeleteObjectsReportsAFailedSingleDelete(t *testing.T) {
	f, svc := newFakeS3(t)
	f.objects["b/plain"] = fakeObject{}
	f.objects["b/esc-\x1b"] = fakeObject{}
	f.deleteFail["esc-\x1b"] = true

	err := svc.DeleteObjects(context.Background(), "b", []string{"plain", "esc-\x1b"})

	var delErr *DeleteObjectsError
	require.ErrorAs(t, err, &delErr)
	require.Len(t, delErr.Failed, 1)
	assert.Equal(t, "esc-\x1b", delErr.Failed[0].Key)
	assert.Equal(t, "AccessDenied", delErr.Failed[0].Code)
	_, ok := f.object("b/plain")
	assert.False(t, ok, "the other keys are still deleted")
}

func TestEmptyAndDeleteBucketWithOddKeys(t *testing.T) {
	f, svc := newFakeVersionedBucket(t, 0)
	for i, k := range oddKeys {
		f.versions = append(f.versions,
			ObjectVersion{Key: k, VersionID: fmt.Sprintf("v%d", i)},
			ObjectVersion{Key: k, VersionID: fmt.Sprintf("d%d", i), IsDeleteMarker: true})
	}

	require.NoError(t, svc.EmptyAndDeleteBucket(context.Background(), "b"))

	assert.True(t, f.bucketDeleted)
	assert.Empty(t, f.versions)
	assert.ElementsMatch(t, []string{"esc-\x1b.txt@v4", "esc-\x1b.txt@d4"}, f.singleDeletes)
}

func TestEmptyAndDeleteBucketStopsOnAFailedSingleDelete(t *testing.T) {
	f, svc := newFakeVersionedBucket(t, 0)
	f.versions = []ObjectVersion{{Key: "esc-\x1b", VersionID: "v1"}, {Key: "plain", VersionID: "v2"}}
	f.failKey = "esc-\x1b"

	err := svc.EmptyAndDeleteBucket(context.Background(), "b")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "deleting version v1 of esc-\x1b")
	assert.Contains(t, err.Error(), "AccessDenied")
	assert.False(t, f.bucketDeleted)
}

// When S3 reports a delete as done but keeps the version, the next round
// lists it again. The function stops instead of looping forever.
func TestEmptyAndDeleteBucketStopsWhenAVersionStays(t *testing.T) {
	f, svc := newFakeVersionedBucket(t, 4)
	f.keep = true

	err := svc.EmptyAndDeleteBucket(context.Background(), "b")

	require.Error(t, err)
	assert.Equal(t, "deleting versions: version v0 of k00000 is still there", err.Error())
	assert.Equal(t, 2, f.lists)
	assert.False(t, f.bucketDeleted)
}
