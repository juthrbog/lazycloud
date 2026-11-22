package app

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/aws/smithy-go"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/eventlog"
	"github.com/juthrbog/lazycloud/internal/jump"
	appmsg "github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// The jump by name (docs/design/jump.md, "Names (PR 5)"): :go NAME runs
// exact-name reads in the session Region, all at once. No match gives a
// toast, one match opens it, and more than one opens a picker.

// nameCandidate is one resource that a name matches.
type nameCandidate struct {
	label string // a line of the picker: "Lambda function", "EC2 instance i-0abc (running)"
	nav   appmsg.NavigateMsg
}

// nameRead is one read by name: the service it asks, for a failure
// message, and the read.
type nameRead struct {
	service string
	read    func(ctx context.Context) ([]nameCandidate, error)
}

// The name rules of each service. A read runs only for a name that the
// service allows, so a name such as "web server" asks only EC2.
var (
	lambdaNameRule = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	ecsNameRule    = regexp.MustCompile(`^[A-Za-z0-9_-]{1,255}$`)
	// A queue name has up to 80 characters, .fifo included.
	queueNameRule = regexp.MustCompile(`^(?:[A-Za-z0-9_-]{1,80}|[A-Za-z0-9_-]{1,75}\.fifo)$`)
	// A Lambda qualifier: a version, $LATEST or an alias.
	lambdaQualifierRule = regexp.MustCompile(`^[A-Za-z0-9_$-]{1,128}$`)
	bucketNameRule      = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
	groupNameRule       = regexp.MustCompile(`^[A-Za-z0-9_\-/.#]{1,512}$`)
)

// nameReads returns the reads for the name of t. NAME:SUFFIX asks Lambda
// for the function and qualifier and ECS for the family and revision. The
// EC2 reads take the whole text, because a Name tag or a group name can
// hold ":" and spaces.
func nameReads(svc aws.Services, t jump.Target, s jump.Session) []nameRead {
	name, q, raw := t.ID, t.Qualifier, t.Raw
	one := func(kind jump.Kind, id, qualifier string) func(context.Context) ([]nameCandidate, error) {
		return func(ctx context.Context) ([]nameCandidate, error) {
			target := jump.Target{Kind: kind, ID: id, Qualifier: qualifier, Raw: raw}
			nav, err := lookup(ctx, svc, target, s)
			if err != nil {
				return nil, err
			}
			label := kind.Noun()
			if qualifier != "" {
				label += " " + id + ":" + qualifier
			}
			return []nameCandidate{{label: label, nav: nav}}, nil
		}
	}
	var reads []nameRead
	if lambdaNameRule.MatchString(name) && (q == "" || lambdaQualifierRule.MatchString(q)) {
		reads = append(reads, nameRead{"Lambda", one(jump.Function, name, q)})
	}
	if q == "" && ecsNameRule.MatchString(name) {
		reads = append(reads, nameRead{"ECS", one(jump.Cluster, name, "")})
	}
	if ecsNameRule.MatchString(name) && (q == "" || numeric.MatchString(q)) {
		reads = append(reads, nameRead{"ECS", one(jump.TaskDefinition, name, q)})
	}
	if q == "" && queueNameRule.MatchString(name) {
		reads = append(reads, nameRead{"SQS", one(jump.Queue, name, "")})
	}
	if q == "" && bucketNameRule.MatchString(name) && !strings.Contains(name, "..") {
		reads = append(reads, nameRead{"S3", one(jump.Bucket, name, "")})
	}
	if q == "" && groupNameRule.MatchString(name) {
		reads = append(reads, nameRead{"CloudWatch Logs", one(jump.LogGroup, name, "")})
	}
	reads = append(reads,
		nameRead{"EC2", func(ctx context.Context) ([]nameCandidate, error) {
			groups, err := svc.EC2.FindSecurityGroupsByName(ctx, raw)
			out := make([]nameCandidate, 0, len(groups))
			for _, g := range groups {
				out = append(out, nameCandidate{
					label: fmt.Sprintf("Security group %s in %s", g.ID, g.VpcID),
					nav:   focusNav("sg_list", g.ID, nil),
				})
			}
			return out, err
		}},
		nameRead{"EC2", func(ctx context.Context) ([]nameCandidate, error) {
			instances, err := svc.EC2.FindInstancesByName(ctx, raw)
			out := make([]nameCandidate, 0, len(instances))
			for _, i := range instances {
				out = append(out, nameCandidate{
					label: fmt.Sprintf("EC2 instance %s (%s)", i.ID, i.State),
					nav:   focusNav("ec2_list", i.ID, nil),
				})
			}
			return out, err
		}},
	)
	return reads
}

// lookupNames runs the reads of t at once and returns the matches in the
// order of the reads. A miss of one read is no error. When nothing matches
// and a read failed, the error names the services that failed.
func lookupNames(ctx context.Context, svc aws.Services, t jump.Target, s jump.Session) ([]nameCandidate, error) {
	reads := nameReads(svc, t, s)
	found := make([][]nameCandidate, len(reads))
	errs := make([]error, len(reads))
	var wg sync.WaitGroup
	for i, r := range reads {
		wg.Go(func() {
			c, err := r.read(ctx)
			found[i] = c
			if err != nil && !isMiss(err) {
				errs[i] = err
			}
		})
	}
	wg.Wait()
	var out []nameCandidate
	var failed []string
	timeouts, failures := 0, 0
	for i := range reads {
		out = append(out, found[i]...)
		// A cancel (ctrl+x) is no failure of a read.
		if errs[i] == nil || errors.Is(errs[i], context.Canceled) {
			continue
		}
		eventlog.Warnf(eventlog.CatAWS, "Jump by name: %s read failed: %v", reads[i].service, errs[i])
		failures++
		if aws.IsTimeout(errs[i]) {
			timeouts++
		}
		// Two reads of one service often fail the same way: name it once.
		if entry := fmt.Sprintf("%s (%s)", reads[i].service, failLabel(errs[i])); !slices.Contains(failed, entry) {
			failed = append(failed, entry)
		}
	}
	if len(out) == 0 && len(failed) > 0 {
		return nil, &nameReadsError{failed: failed, timeout: timeouts == failures}
	}
	return out, nil
}

// failLabel is a short name for the failure of a read in a toast: the AWS
// error code, "timeout", or "error". The event log has the full error.
func failLabel(err error) string {
	if aws.IsTimeout(err) {
		return "timeout"
	}
	if apiErr, ok := errors.AsType[smithy.APIError](err); ok && apiErr.ErrorCode() != "" {
		return apiErr.ErrorCode()
	}
	return "error"
}

// isMiss reports whether err says that the resource does not exist. A
// bucket of another account counts as a miss: its name is taken, but it is
// not a resource of this account.
func isMiss(err error) bool {
	return errors.Is(missing(err), errNotFound) ||
		errors.Is(err, aws.ErrBucketNotFound) || errors.Is(err, aws.ErrBucketDenied)
}

// nameReadsError is a jump by name that found nothing while some reads
// failed.
type nameReadsError struct {
	failed  []string
	timeout bool // every failed read timed out
}

func (e *nameReadsError) Error() string { return "reads failed: " + strings.Join(e.failed, ", ") }

// isNameTimeout reports whether a jump by name found nothing because its
// reads timed out.
func isNameTimeout(err error) bool {
	e, ok := errors.AsType[*nameReadsError](err)
	return ok && e.timeout
}

// nameMissText is the toast of a jump by name that found nothing.
func nameMissText(t jump.Target, s jump.Session, err error) string {
	text := fmt.Sprintf("No resource named %s in %s.", t.Raw, s.Region)
	if e, ok := errors.AsType[*nameReadsError](err); ok {
		text += " Some reads failed: " + strings.Join(e.failed, ", ") + "."
	}
	return text
}

// nameResult runs the reads of a jump by name and fills r: the route of
// one match, the matches for a picker, or the toast of a miss. opCtx is the
// context of the operation, which ctrl+x cancels.
func nameResult(ctx, opCtx context.Context, svc aws.Services, t jump.Target, s jump.Session, r jumpResultMsg) jumpResultMsg {
	cands, err := lookupNames(ctx, svc, t, s)
	switch {
	case opCtx.Err() != nil:
		// cancelled with ctrl+x
	case len(cands) == 1:
		r.nav = &cands[0].nav
	case len(cands) > 1:
		r.name, r.cands = t.Raw, cands
	case isNameTimeout(err):
		r.text = fmt.Sprintf("Finding resources named %s took too long. Try again.", t.Raw)
	default:
		r.text = nameMissText(t, s, err)
	}
	return r
}

// pickName opens the picker for several matches of a name. A pick that
// comes after a session switch does nothing, because the routes belong to
// the old session.
func (m Model) pickName(name string, cands []nameCandidate) (Model, tea.Cmd) {
	options := make([]ui.PickerOption, len(cands))
	for i, c := range cands {
		options[i] = ui.PickerOption{Label: c.label, Value: strconv.Itoa(i)}
	}
	gen := m.sessionGen
	cmd := m.pick(fmt.Sprintf("Several resources are named %s", name), options, 0, onPicked(func(m Model, v string) (Model, tea.Cmd) {
		i, err := strconv.Atoi(v)
		if err != nil || i < 0 || i >= len(cands) {
			return m, nil
		}
		if gen != m.sessionGen {
			eventlog.Infof(eventlog.CatNav, "Dropped a pick of a jump by name: the session changed")
			_, cmd := m.toasts.Add("The session changed, so the jump did not run. Try again.", ui.ToastInfo, 0)
			return m, cmd
		}
		return m.navigate(cands[i].nav)
	}))
	return m, cmd
}
