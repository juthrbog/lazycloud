package aws

import (
	"errors"

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
