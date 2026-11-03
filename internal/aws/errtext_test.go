package aws

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/ssocreds"
	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// M21: AWS errors showed as raw SDK strings.

// opErr builds the error shape of the SDK: an operation error around err.
func opErr(service, op string, err error) error {
	return &smithy.OperationError{ServiceID: service, OperationName: op, Err: err}
}

func apiErr(code, message string) error {
	return &smithy.GenericAPIError{Code: code, Message: message}
}

func TestErrorText(t *testing.T) {
	inProfile := func(err error) error { return &ProfileError{Profile: "prod", Err: err} }
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"cancel", fmt.Errorf("writing file: %w", context.Canceled), "cancelled"},
		{"timeout", opErr("EC2", "DescribeInstances", context.DeadlineExceeded), "no answer from AWS in time"},
		{"plain error", errors.New("creating file: permission denied"), "creating file: permission denied"},

		{"access denied", opErr("S3", "ListBuckets", inProfile(apiErr("AccessDenied", "Access Denied"))),
			"S3 ListBuckets: access denied (AccessDenied). The credentials of this profile do not allow this call"},
		{"denied in the credential call", opErr("S3", "ListBuckets", inProfile(fmt.Errorf("get identity: get credentials: %w",
			opErr("STS", "AssumeRole", apiErr("AccessDenied", "not authorized"))))),
			"STS AssumeRole: access denied (AccessDenied). The credentials of this profile do not allow this call"},
		{"expired", opErr("SQS", "ListQueues", inProfile(apiErr("ExpiredToken", "The security token included in the request is expired"))),
			"the AWS credentials have expired (ExpiredToken). Get new ones, for example with: aws sso login --profile prod"},
		{"bad credentials", opErr("EC2", "DescribeVpcs", apiErr("AuthFailure", "AWS was not able to validate the provided access credentials")),
			"AWS does not accept the credentials (AuthFailure). Check the profile"},
		{"throttled", opErr("SQS", "ListQueues", apiErr("ThrottlingException", "Rate exceeded")),
			"SQS ListQueues: AWS limits the request rate (ThrottlingException). Try again in a moment"},
		{"other code", opErr("SQS", "GetQueueAttributes", apiErr("QueueDoesNotExist", "The specified queue does not exist.")),
			"SQS GetQueueAttributes: The specified queue does not exist. (QueueDoesNotExist)"},
		{"code without message", opErr("S3", "HeadObject", apiErr("NotFound", "")), "S3 HeadObject: NotFound"},

		{"sso token expired", opErr("S3", "ListBuckets", inProfile(fmt.Errorf("get identity: get credentials: %w", &ssocreds.InvalidTokenError{}))),
			"the SSO session has expired or is not signed in. Sign in with: aws sso login --profile prod"},
		{"sso token refused", opErr("S3", "ListBuckets", fmt.Errorf("get identity: get credentials: %w",
			opErr("SSO", "GetRoleCredentials", apiErr("UnauthorizedException", "Session token not found or invalid")))),
			"the SSO session has expired or is not signed in. Sign in with: aws sso login"},
		{"sso session refresh", opErr("S3", "ListBuckets", inProfile(errors.New("get identity: get credentials: refresh cached SSO token failed, cached SSO token is expired, or not present, and cannot be refreshed"))),
			"the SSO session has expired or is not signed in. Sign in with: aws sso login --profile prod"},
		{"other sso error is not a login", opErr("S3", "ListBuckets", fmt.Errorf("get credentials: %w",
			opErr("SSO", "GetRoleCredentials", apiErr("ResourceNotFoundException", "No access")))),
			"SSO GetRoleCredentials: No access (ResourceNotFoundException)"},

		{"no profile", config.SharedConfigProfileNotExistError{Profile: "prd"}, `the profile "prd" is not in the AWS config files`},
		{"no credentials", opErr("S3", "ListBuckets", fmt.Errorf("get credentials: failed to refresh cached credentials, no EC2 IMDS role found, %w",
			&net.OpError{Op: "dial", Err: errors.New("connect: no route to host")})),
			"no AWS credentials found. Press P to pick a profile, or set up credentials in ~/.aws"},
		{"dns", opErr("S3", "ListBuckets", &net.DNSError{Name: "s3.xx-west-9.amazonaws.com", Err: "no such host"}),
			"cannot find the host s3.xx-west-9.amazonaws.com. Check the network and the region"},
		{"refused", opErr("S3", "ListBuckets", &net.OpError{Op: "dial", Addr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1},
			Err: errors.New("connect: connection refused")}),
			"cannot connect to 127.0.0.1:1 (connect: connection refused)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, ErrorText(c.err))
		})
	}
}

// Through the real SDK: the client adds the profile of its session to the
// error, and the text has no request ID or HTTP status.
func TestErrorTextFromARealCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<Error><Code>AccessDenied</Code><Message>Access Denied</Message><RequestId>R1</RequestId></Error>`))
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config")
	require.NoError(t, os.WriteFile(cfgFile, []byte("[profile prod]\nregion = eu-west-1\n"), 0o600))
	t.Setenv("AWS_CONFIG_FILE", cfgFile)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	t.Setenv("AWS_PROFILE", "")
	c, err := NewClient("prod", "", srv.URL)
	require.NoError(t, err)

	_, err = NewS3Service(c).ListBuckets(t.Context())
	require.Error(t, err)
	var pe *ProfileError
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, "prod", pe.Profile)
	assert.Contains(t, err.Error(), "RequestID", "the SDK text names transport details")
	assert.Equal(t, "S3 ListBuckets: access denied (AccessDenied). The credentials of this profile do not allow this call", ErrorText(err))
}

func TestErrorTextForAMissingProfile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	t.Setenv("AWS_PROFILE", "")
	_, err := NewClient("nope", "", "")
	require.Error(t, err)
	assert.Equal(t, `the profile "nope" is not in the AWS config files`, ErrorText(err))
}
