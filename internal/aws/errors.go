package aws

import (
	"context"
	"errors"

	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/smithy-go"
)

// ErrorCode returns the AWS error code of err, for example "AccessDenied".
// It returns the error text when err has no code. Use it where space is
// short, such as a toast that lists several failed items. Log the full
// error as well.
func ErrorCode(err error) string {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) && apiErr.ErrorCode() != "" {
		return apiErr.ErrorCode()
	}
	return err.Error()
}

// IsTimeout reports whether err comes from a timeout: a context deadline,
// or the read timeout of the HTTP client (no data from AWS for too long).
func IsTimeout(err error) bool {
	var rte *awshttp.ResponseTimeoutError
	return errors.Is(err, context.DeadlineExceeded) || errors.As(err, &rte)
}
