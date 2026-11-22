package jump

import (
	"fmt"
	"regexp"
	"strings"
)

// Session is what Check needs to know about the session.
type Session struct {
	Region   string              // the Region of the session, never ""
	Account  string              // the account from STS, "" when it is not known
	Endpoint bool                // true with a custom endpoint (LocalStack)
	Profiles map[string][]string // the profiles that name each account in the config
}

// Forms names the forms that a jump takes, for the message of an empty or
// unreadable text.
const Forms = "Use an ID (i-..., sg-...), an ARN, an SQS queue URL, s3://BUCKET/KEY, a log group path, or :go NAME."

// partitions maps the Region names of each partition, as the SDK does in
// internal/endpoints/awsrulesfn/partitions.json. A Region that matches none
// is in "aws", as in the SDK.
var partitions = []struct {
	id     string
	region *regexp.Regexp
}{
	{"aws-cn", regexp.MustCompile(`^cn-\w+-\d+$`)},
	{"aws-eusc", regexp.MustCompile(`^eusc-(de)-\w+-\d+$`)},
	{"aws-iso", regexp.MustCompile(`^us-iso-\w+-\d+$`)},
	{"aws-iso-b", regexp.MustCompile(`^us-isob-\w+-\d+$`)},
	{"aws-iso-e", regexp.MustCompile(`^eu-isoe-\w+-\d+$`)},
	{"aws-iso-f", regexp.MustCompile(`^us-isof-\w+-\d+$`)},
	{"aws-us-gov", regexp.MustCompile(`^us-gov-\w+-\d+$`)},
}

// PartitionOf returns the partition of a Region, for example "aws-cn" for
// cn-north-1 and "aws" for us-east-1.
func PartitionOf(region string) string {
	for _, p := range partitions {
		if p.region.MatchString(region) {
			return p.id
		}
	}
	return "aws"
}

// Check returns the message that refuses the jump to t in the session s,
// or "" when the jump can go on. A field that t does not name skips its
// check. Another Region is not a refusal: see OtherRegion.
func Check(t Target, s Session) string {
	switch t.Kind {
	case Empty:
		return "Type what to jump to. " + Forms
	case Invalid:
		return fmt.Sprintf("LazyCloud cannot read %q. %s", short(t.Raw), Forms)
	case Unsupported:
		return fmt.Sprintf("LazyCloud has no view for %s resources: %s", t.Service, short(t.Raw))
	case Tags:
		return "LazyCloud cannot search by tag yet. " + Forms
	default:
		// A resource kind: the checks below.
	}
	if p, sp := targetPartition(t), PartitionOf(s.Region); p != "" && p != sp {
		return fmt.Sprintf("The resource is in the %s partition. The session uses %s.", p, sp)
	}
	// LocalStack ARNs all have the account 000000000000.
	if !s.Endpoint && s.Account != "" && t.Account != "" && t.Account != s.Account {
		names := s.Profiles[t.Account]
		switch len(names) {
		case 0:
			return fmt.Sprintf("The resource is in account %s. The session uses account %s.", t.Account, s.Account)
		case 1:
			return fmt.Sprintf("The resource is in account %s, the profile %s. Press P to switch.", t.Account, names[0])
		default:
			return fmt.Sprintf("The resource is in account %s, the profiles %s. Press P to switch.", t.Account, strings.Join(names, ", "))
		}
	}
	return ""
}

// maxShown is the number of runes of the text that a message shows.
const maxShown = 120

// short returns s, cut to maxShown runes with "…" at the end, so a long
// paste does not fill the screen.
func short(s string) string {
	r := []rune(s)
	if len(r) <= maxShown {
		return s
	}
	return string(r[:maxShown-1]) + "…"
}

// targetPartition returns the partition that t names: the partition of its
// ARN, else the partition of its Region, else "".
func targetPartition(t Target) string {
	if t.Partition != "" {
		return t.Partition
	}
	if t.Region != "" {
		return PartitionOf(t.Region)
	}
	return ""
}

// OtherRegion returns the Region of t when it is not the Region of the
// session, so the app must switch before it opens t. It returns "" for the
// session Region, for a target with no Region, and for S3, which needs no
// switch.
func OtherRegion(t Target, s Session) string {
	if t.Region == s.Region || t.Kind == Bucket || t.Kind == Object {
		return ""
	}
	return t.Region
}
