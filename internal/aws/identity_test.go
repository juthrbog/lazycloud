package aws

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/credentials/ssocreds"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSTS answers every call with status and body, after delay.
func fakeSTS(t *testing.T, status int, body string, delay time.Duration) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil || r.PostForm.Get("Action") != "GetCallerIdentity" {
			http.Error(w, "unexpected call", http.StatusNotImplemented)
			return
		}
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "text/xml")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	c, err := NewClient("", "us-east-1", srv.URL)
	require.NoError(t, err)
	return c
}

func stsError(code string) string {
	return `<ErrorResponse><Error><Type>Sender</Type><Code>` + code + `</Code><Message>m</Message></Error><RequestId>r</RequestId></ErrorResponse>`
}

func TestCallerIdentity(t *testing.T) {
	c := fakeSTS(t, http.StatusOK, `<GetCallerIdentityResponse><GetCallerIdentityResult>
<Arn>arn:aws:sts::123456789012:assumed-role/AWSReservedSSO_ReadOnlyAccess_0123456789abcdef/jane</Arn>
<UserId>AROAEXAMPLE:jane</UserId><Account>123456789012</Account>
</GetCallerIdentityResult></GetCallerIdentityResponse>`, 0)

	id, err := c.CallerIdentity(t.Context())

	require.NoError(t, err)
	assert.Equal(t, Identity{
		Account:   "123456789012",
		ARN:       "arn:aws:sts::123456789012:assumed-role/AWSReservedSSO_ReadOnlyAccess_0123456789abcdef/jane",
		UserID:    "AROAEXAMPLE:jane",
		Principal: "AWSReservedSSO_ReadOnlyAccess_0123456789abcdef",
		Session:   "jane",
	}, id)
}

func TestCallerIdentityDenied(t *testing.T) {
	c := fakeSTS(t, http.StatusForbidden, stsError("AccessDenied"), 0)

	_, err := c.CallerIdentity(t.Context())

	require.Error(t, err)
	assert.False(t, LoginNeeded(err))
	assert.Contains(t, ErrorText(err), "STS GetCallerIdentity: access denied")
}

func TestCallerIdentityExpired(t *testing.T) {
	c := fakeSTS(t, http.StatusForbidden, stsError("ExpiredToken"), 0)

	_, err := c.CallerIdentity(t.Context())

	require.Error(t, err)
	assert.True(t, LoginNeeded(err))
}

func TestCallerIdentityTimeout(t *testing.T) {
	c := fakeSTS(t, http.StatusOK, "", 5*time.Second)
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	_, err := c.CallerIdentity(ctx)

	require.Error(t, err)
	assert.True(t, IsTimeout(err))
	assert.False(t, LoginNeeded(err))
}

func TestParsePrincipal(t *testing.T) {
	for _, tc := range []struct{ arn, principal, session string }{
		{"arn:aws:sts::123456789012:assumed-role/Admin/jane", "Admin", "jane"},
		{"arn:aws:iam::123456789012:user/jane", "jane", ""},
		{"arn:aws:iam::123456789012:user/team/ops/jane", "jane", ""},
		{"arn:aws:sts::123456789012:federated-user/bob", "bob", ""},
		{"arn:aws:iam::123456789012:root", "root", ""},
		{"arn:aws-cn:sts::123456789012:assumed-role/R/s", "R", "s"},
		{"not an arn", "not an arn", ""},
	} {
		p, s := parsePrincipal(tc.arn)
		assert.Equal(t, tc.principal, p, tc.arn)
		assert.Equal(t, tc.session, s, tc.arn)
	}
}

func TestDisplayPrincipal(t *testing.T) {
	assert.Equal(t, "ReadOnlyAccess", DisplayPrincipal("AWSReservedSSO_ReadOnlyAccess_0123456789abcdef"))
	assert.Equal(t, "Power_User", DisplayPrincipal("AWSReservedSSO_Power_User_0123456789abcdef"))
	// Not the exact pattern: the name stays.
	assert.Equal(t, "AWSReservedSSO_X_0123", DisplayPrincipal("AWSReservedSSO_X_0123"))
	assert.Equal(t, "AWSReservedSSO_X_0123456789ABCDEF", DisplayPrincipal("AWSReservedSSO_X_0123456789ABCDEF"))
	assert.Equal(t, "Admin", DisplayPrincipal("Admin"))
}

func TestLoginNeededNil(t *testing.T) {
	assert.False(t, LoginNeeded(nil))
}

// An SSO or aws login error that is not an AWS error code also means a
// sign-in.
func TestLoginNeededForSignInErrors(t *testing.T) {
	assert.True(t, LoginNeeded(&ssocreds.InvalidTokenError{Err: errors.New("expired")}))
	assert.True(t, LoginNeeded(errors.New("load login token: no such file")))
	assert.False(t, LoginNeeded(errors.New("dial tcp: refused")))
}
