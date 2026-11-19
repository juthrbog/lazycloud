package aws

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"
)

// Identity is the answer of STS GetCallerIdentity: the account and the
// principal that the credentials of a session belong to.
type Identity struct {
	Account   string
	ARN       string
	UserID    string
	Principal string // the role name for an assumed role, else the user name or the ARN resource
	Session   string // the session name for an assumed role
}

// CallerIdentity asks STS who the credentials of the session belong to.
// The call needs no permission. It also gets the credentials, so it fails
// like the first call of a view when the user must sign in.
func (c *Client) CallerIdentity(ctx context.Context) (Identity, error) {
	client := sts.NewFromConfig(c.Config, func(o *sts.Options) {
		if c.Endpoint != "" {
			o.BaseEndpoint = aws.String(c.Endpoint)
		}
	})
	out, err := client.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return Identity{}, err
	}
	id := Identity{
		Account: aws.ToString(out.Account),
		ARN:     aws.ToString(out.Arn),
		UserID:  aws.ToString(out.UserId),
	}
	id.Principal, id.Session = parsePrincipal(id.ARN)
	return id, nil
}

// parsePrincipal returns the principal and the session name of a caller
// ARN. An assumed role gives the role name and the session name. A user
// gives the user name without its path. The root user gives "root".
// Any other ARN gives its resource, and a string that is not an ARN
// gives itself.
func parsePrincipal(s string) (principal, session string) {
	a, err := arn.Parse(s)
	if err != nil {
		return s, ""
	}
	kind, rest, _ := strings.Cut(a.Resource, "/")
	switch kind {
	case "assumed-role":
		role, sess, _ := strings.Cut(rest, "/")
		return role, sess
	case "user", "federated-user":
		return rest[strings.LastIndex(rest, "/")+1:], ""
	}
	return a.Resource, ""
}

// ssoRoleName matches the name that IAM Identity Center gives the role of
// a permission set: AWSReservedSSO_<permission set>_<16 hex digits>.
var ssoRoleName = regexp.MustCompile(`^AWSReservedSSO_(.+)_[0-9a-f]{16}$`)

// DisplayPrincipal shortens a principal for the header. The role of an
// SSO permission set shows as the name of the permission set, so
// AWSReservedSSO_ReadOnlyAccess_0123456789abcdef shows as ReadOnlyAccess.
// Any other principal stays as it is.
func DisplayPrincipal(p string) string {
	if m := ssoRoleName.FindStringSubmatch(p); m != nil {
		return m[1]
	}
	return p
}

// LoginNeeded reports whether err means that the credentials of the
// session have expired or that the user must sign in.
func LoginNeeded(err error) bool {
	if err == nil {
		return false
	}
	if _, ok := loginNeeded(err); ok {
		return true
	}
	apiErr, ok := errors.AsType[smithy.APIError](err)
	return ok && expiredCodes[apiErr.ErrorCode()]
}
