package aws

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/ssocreds"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/middleware"
)

// ProfileError carries the profile of the session whose AWS call failed.
// Each client adds it to the errors of its calls (see profileMiddleware),
// so ErrorText can name the profile in a hint such as
// "aws sso login --profile prod". Its text is the text of Err.
type ProfileError struct {
	Profile string // "" when the session uses the default profile
	Err     error
}

func (e *ProfileError) Error() string { return e.Err.Error() }
func (e *ProfileError) Unwrap() error { return e.Err }

// profileMiddleware wraps the error of every call in a *ProfileError.
func profileMiddleware(profile string) func(*middleware.Stack) error {
	return func(s *middleware.Stack) error {
		return s.Initialize.Add(middleware.InitializeMiddlewareFunc("LazyCloudProfile",
			func(ctx context.Context, in middleware.InitializeInput, next middleware.InitializeHandler) (
				middleware.InitializeOutput, middleware.Metadata, error) {
				out, md, err := next.HandleInitialize(ctx, in)
				if err != nil {
					err = &ProfileError{Profile: profile, Err: err}
				}
				return out, md, err
			}), middleware.Before)
	}
}

// Error codes that ErrorText explains. AWS services use different codes
// for the same problem.
var (
	accessDeniedCodes = codeSet("AccessDenied", "AccessDeniedException", "UnauthorizedOperation",
		"UnauthorizedAccess", "Forbidden", "AuthorizationError", "AllAccessDisabled")
	expiredCodes       = codeSet("ExpiredToken", "ExpiredTokenException", "RequestExpired", "TokenRefreshRequired")
	badCredentialCodes = codeSet("InvalidClientTokenId", "UnrecognizedClientException", "SignatureDoesNotMatch",
		"InvalidAccessKeyId", "AuthFailure", "InvalidToken", "IncompleteSignature", "MissingAuthenticationToken")
	throttleCodes = codeSet("Throttling", "ThrottlingException", "ThrottledException", "RequestLimitExceeded",
		"RequestThrottled", "RequestThrottledException", "SlowDown", "TooManyRequestsException",
		"ProvisionedThroughputExceededException")
)

func codeSet(codes ...string) map[string]bool {
	m := make(map[string]bool, len(codes))
	for _, c := range codes {
		m[c] = true
	}
	return m
}

// ErrorText returns a short text for an error that the user sees, for
// example in a toast or an error line. The SDK text of an error is long
// and names transport details, such as the request ID and the HTTP status.
// ErrorText keeps the AWS error code and message, names the call, and adds
// a hint for common problems: an SSO session that has expired, missing or
// bad credentials, a denied call, throttling, and network errors.
//
// Log the full error as well, because ErrorText leaves details out.
func ErrorText(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case IsTimeout(err):
		return "no answer from AWS in time"
	case ssoLoginNeeded(err):
		return "the SSO session has expired or is not signed in. Sign in with: " + loginCommand(err)
	}

	if noProfile, ok := errors.AsType[config.SharedConfigProfileNotExistError](err); ok {
		return fmt.Sprintf("the profile %q is not in the AWS config files", noProfile.Profile)
	}
	// Without credentials, the SDK asks the EC2 instance metadata service
	// last. Its error is about the network, so this check comes first.
	if strings.Contains(err.Error(), "no EC2 IMDS role found") {
		return "no AWS credentials found. Press P to pick a profile, or set up credentials in ~/.aws"
	}

	if apiErr, ok := errors.AsType[smithy.APIError](err); ok {
		return apiErrorText(err, apiErr)
	}

	if dnsErr, ok := errors.AsType[*net.DNSError](err); ok {
		return fmt.Sprintf("cannot find the host %s. Check the network and the region", dnsErr.Name)
	}
	if opErr, ok := errors.AsType[*net.OpError](err); ok {
		if opErr.Addr != nil {
			return fmt.Sprintf("cannot connect to %s (%v)", opErr.Addr, opErr.Err)
		}
		return fmt.Sprintf("network error (%v)", opErr.Err)
	}
	return err.Error()
}

// apiErrorText is the ErrorText of an error that AWS returned.
func apiErrorText(err error, apiErr smithy.APIError) string {
	code := apiErr.ErrorCode()
	where := ""
	if call := failedCall(err); call != "" {
		where = call + ": "
	}
	switch {
	case accessDeniedCodes[code]:
		return fmt.Sprintf("%saccess denied (%s). The credentials of this profile do not allow this call", where, code)
	case expiredCodes[code]:
		return fmt.Sprintf("the AWS credentials have expired (%s). Get new ones, for example with: %s", code, loginCommand(err))
	case badCredentialCodes[code]:
		return fmt.Sprintf("AWS does not accept the credentials (%s). Check the profile", code)
	case throttleCodes[code]:
		return fmt.Sprintf("%sAWS limits the request rate (%s). Try again in a moment", where, code)
	}
	text := apiErr.ErrorMessage()
	if text == "" || text == code {
		return where + code
	}
	return fmt.Sprintf("%s%s (%s)", where, text, code)
}

// failedCall names the AWS call that returned the error, for example
// "S3 ListBuckets". A call can fail inside another one, such as the STS
// call that gets the credentials for an S3 call, so the innermost call
// is the one that failed.
func failedCall(err error) string {
	call := ""
	for e := err; e != nil; e = errors.Unwrap(e) {
		if oe, ok := e.(*smithy.OperationError); ok { //nolint:errorlint // the loop walks the chain layer by layer
			call = oe.ServiceID + " " + oe.OperationName
		}
	}
	return call
}

// ssoLoginNeeded reports whether err means that the user must sign in to
// SSO again: the cached SSO token is missing or has expired, the SSO
// service does not accept it, or it cannot be refreshed.
func ssoLoginNeeded(err error) bool {
	if _, ok := errors.AsType[*ssocreds.InvalidTokenError](err); ok {
		return true
	}
	for e := err; e != nil; e = errors.Unwrap(e) {
		oe, ok := e.(*smithy.OperationError) //nolint:errorlint // the loop walks the chain layer by layer
		if !ok {
			continue
		}
		if oe.ServiceID == "SSO OIDC" {
			return true // the refresh of the SSO token failed
		}
		if apiErr, ok := errors.AsType[smithy.APIError](oe.Err); ok && oe.ServiceID == "SSO" && apiErr.ErrorCode() == "UnauthorizedException" {
			return true
		}
	}
	// The token provider of an sso-session returns plain errors.
	return strings.Contains(err.Error(), "SSO token")
}

// loginCommand returns the AWS CLI command that signs in to the profile of
// the session whose call failed.
func loginCommand(err error) string {
	if pe, ok := errors.AsType[*ProfileError](err); ok && pe.Profile != "" {
		return "aws sso login --profile " + pe.Profile
	}
	return "aws sso login"
}
