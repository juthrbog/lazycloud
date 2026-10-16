package aws

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeEC2Pages answers DescribeImages and DescribeInstances in the EC2
// query protocol. Each DescribeImages call returns the next entry of
// imagePages. endless makes every call return a next token.
type fakeEC2Pages struct {
	mu         sync.Mutex
	imagePages []int // the number of images on each page
	endless    bool
	calls      []url.Values
}

func newFakeEC2Pages(t *testing.T, pages ...int) (*fakeEC2Pages, *EC2ServiceImpl) {
	t.Helper()
	f := &fakeEC2Pages{imagePages: pages}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	c, err := NewClient("", "us-east-1", srv.URL)
	require.NoError(t, err)
	return f, NewEC2Service(c)
}

func (f *fakeEC2Pages) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	f.calls = append(f.calls, r.PostForm)
	switch r.PostForm.Get("Action") {
	case "DescribeImages":
		page := len(f.calls) - 1
		n := 0
		if page < len(f.imagePages) {
			n = f.imagePages[page]
		}
		var b strings.Builder
		b.WriteString(`<DescribeImagesResponse><imagesSet>`)
		for i := range n {
			fmt.Fprintf(&b, `<item><imageId>ami-%d-%d</imageId><name>img</name></item>`, page, i)
		}
		b.WriteString(`</imagesSet>`)
		if f.endless || page+1 < len(f.imagePages) {
			fmt.Fprintf(&b, `<nextToken>t%d</nextToken>`, page+1)
		}
		b.WriteString(`</DescribeImagesResponse>`)
		_, _ = w.Write([]byte(b.String()))
	case "DescribeInstances":
		_, _ = w.Write([]byte(`<DescribeInstancesResponse><reservationSet/></DescribeInstancesResponse>`))
	default:
		http.Error(w, "unexpected action "+r.PostForm.Get("Action"), http.StatusNotImplemented)
	}
}

func (f *fakeEC2Pages) param(call int, name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[call].Get(name)
}

// M26: ListOwnedAMIs read one page.
func TestListOwnedAMIsReadsEveryPage(t *testing.T) {
	f, svc := newFakeEC2Pages(t, 2, 2, 1)

	amis, err := svc.ListOwnedAMIs(context.Background())

	require.NoError(t, err)
	assert.Len(t, amis, 5)
	assert.Len(t, f.calls, 3)
	assert.Equal(t, "self", f.param(0, "Owner.1"))
	assert.Equal(t, "1000", f.param(0, "MaxResults"))
	assert.Equal(t, "t1", f.param(1, "NextToken"))
}

// M26: SearchAMIs read one page of 100, which can hold few matches while
// more pages follow.
func TestSearchAMIsReadsPagesUntilItHasEnough(t *testing.T) {
	f, svc := newFakeEC2Pages(t, 0, 60, 60, 60)

	amis, err := svc.SearchAMIs(context.Background(), "ubuntu")

	require.NoError(t, err)
	assert.Len(t, amis, maxSearchResults)
	assert.Len(t, f.calls, 3, "an empty first page, then two pages give 120")
	assert.Equal(t, "*ubuntu*", f.param(0, "Filter.1.Value.1"))
	assert.Empty(t, f.param(0, "Owner.1"), "a public search sets no owner")
}

func TestSearchAMIsStopsAfterTheLastPage(t *testing.T) {
	f, svc := newFakeEC2Pages(t, 3, 4)

	amis, err := svc.SearchAMIs(context.Background(), "x")

	require.NoError(t, err)
	assert.Len(t, amis, 7)
	assert.Len(t, f.calls, 2)
}

func TestSearchAMIsStopsAfterThePageLimit(t *testing.T) {
	f, svc := newFakeEC2Pages(t, 1)
	f.endless = true

	amis, err := svc.SearchAMIs(context.Background(), "rare")

	require.NoError(t, err)
	assert.Len(t, amis, 1)
	assert.Len(t, f.calls, maxSearchPages)
}

// M26: without MaxResults, EC2 returns every instance in one response.
func TestListInstancesPageSetsMaxResults(t *testing.T) {
	f, svc := newFakeEC2Pages(t)

	_, err := svc.ListInstancesPage(context.Background(), nil)

	require.NoError(t, err)
	require.Len(t, f.calls, 1)
	n, err := strconv.Atoi(f.param(0, "MaxResults"))
	require.NoError(t, err)
	assert.Equal(t, 1000, n)
}
