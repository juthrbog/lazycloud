package app

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/aws/smithy-go"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/eventlog"
	"github.com/juthrbog/lazycloud/internal/jump"
	appmsg "github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
	"github.com/juthrbog/lazycloud/internal/views"
)

// The jump to a resource (docs/design/jump.md): :go TEXT, or a bare ARN, ID
// or URL after :. The parser in internal/jump reads the text, the checks
// refuse another partition or account, and one read confirms the resource
// and gets the parameters of its route. The read runs as an operation, so
// the operations line shows it and ctrl+x cancels it.

// jumpTimeout limits the read of a jump, as callTimeout does in the views.
const jumpTimeout = 30 * time.Second

// regionServices returns the services for a jump read in another Region.
// Tests replace it with mocks.
var regionServices = aws.Services.ForRegion

// jumpResultMsg is the answer of a jump read: the navigation to the
// resource, or the text of a toast. Both are empty when the user cancelled.
type jumpResultMsg struct {
	session string          // the session that the read ran in, for the event log
	gen     int             // the sessionGen of that session
	region  string          // the other Region that the resource is in, or ""
	name    string          // the name of a jump by name
	cands   []nameCandidate // the matches of a jump by name, when more than one
	nav     *appmsg.NavigateMsg
	text    string
}

// errNotFound marks a read that found no resource. lookup turns it into
// the message that names the resource and the Region.
var errNotFound = errors.New("not found")

// errNoCluster is the miss of a short ECS ARN in the default cluster.
var errNoCluster = errors.New("no cluster")

// jumpSession is what the checks of a jump know about the session. The
// profiles that name each account are read only for a refusal (see
// jumpCheck).
func (m Model) jumpSession() jump.Session {
	s := jump.Session{Region: m.activeRegion(), Endpoint: m.config.AWS.Endpoint != ""}
	if m.ident.state == ui.AccountKnown {
		s.Account = m.ident.id.Account
	}
	return s
}

// jumpCheck returns the refusal of jump.Check. A refusal for another
// account names the profiles of that account, which the config files give.
func jumpCheck(t jump.Target, s jump.Session) string {
	text := jump.Check(t, s)
	if text != "" && !s.Endpoint && s.Account != "" && t.Account != "" && t.Account != s.Account {
		s.Profiles = profilesByAccount(aws.DescribeProfiles(time.Now()))
		text = jump.Check(t, s)
	}
	return text
}

// profilesByAccount maps each account to the profiles that name it, in
// name order.
func profilesByAccount(infos []aws.ProfileInfo) map[string][]string {
	out := map[string][]string{}
	for _, p := range infos {
		if p.Account != "" {
			out[p.Account] = append(out[p.Account], p.Name)
		}
	}
	for _, names := range out {
		sort.Strings(names)
	}
	return out
}

// jumpCommand runs a jump to the resource that text names.
func (m Model) jumpCommand(text string) (Model, tea.Cmd) {
	t := jump.Parse(text)
	s := m.jumpSession()
	if refusal := jumpCheck(t, s); refusal != "" {
		eventlog.Infof(eventlog.CatNav, "Jump refused: %s", refusal)
		_, cmd := m.toasts.Add(refusal, ui.ToastError, 0)
		return m, cmd
	}
	if m.svcErr != nil {
		_, cmd := m.toasts.Add(noSessionText(m.svcErr), ui.ToastError, 0)
		return m, cmd
	}
	// A tag search opens its list, which reads the resources itself.
	if t.Kind == jump.Tags {
		return m.navigate(appmsg.NavigateMsg{ViewID: "jump_tags", Params: map[string]string{"filter": t.Raw}})
	}
	// A resource in another Region is read there first, so a miss gives
	// its message and no switch. The read uses a copy of the client for
	// that Region, and the messages name that Region.
	svc := m.svc
	region := jump.OtherRegion(t, s)
	if region != "" {
		svc = regionServices(svc, region)
		s.Region = region
	}
	label := "Find " + t.Kind.Noun() + " " + targetName(t)
	if t.Kind == jump.Name {
		label = "Find resources named " + t.Raw
	}
	session, gen := m.sessionLabel(), m.sessionGen
	op := m.ops.Begin(label, session)
	eventlog.Infof(eventlog.CatNav, "Jump: %s", label)
	run := func() tea.Msg {
		defer op.Done()
		ctx, cancel := context.WithTimeout(op.Ctx, jumpTimeout)
		defer cancel()
		if t.Kind == jump.Name {
			return nameResult(ctx, op.Ctx, svc, t, s, jumpResultMsg{session: session, gen: gen})
		}
		nav, err := lookup(ctx, svc, t, s)
		switch {
		case err == nil:
			return jumpResultMsg{session: session, gen: gen, region: region, nav: &nav}
		case op.Ctx.Err() != nil:
			return jumpResultMsg{session: session, gen: gen} // cancelled with ctrl+x
		default:
			return jumpResultMsg{session: session, gen: gen, text: lookupError(t, s, err)}
		}
	}
	m.syncOpsLine()
	cmd := tea.Cmd(run)
	if !m.opsTicking {
		m.opsTicking = true
		cmd = tea.Batch(cmd, opsTick())
	}
	return m, cmd
}

// onJumpResult opens the resource, or shows why not. A result of another
// session is dropped: its route would open with the services of the new
// session. The generation tells sessions apart also when the user switched
// away and back to the same profile and Region.
func (m Model) onJumpResult(r jumpResultMsg) (Model, tea.Cmd) {
	// The operation ended before its result came, so the line can go.
	m.syncOpsLine()
	if r.gen != m.sessionGen {
		eventlog.Infof(eventlog.CatNav, "Dropped a jump of session %s", r.session)
		return m, nil
	}
	switch {
	case len(r.cands) > 1:
		return m.pickName(r.name, r.cands)
	case r.nav != nil && r.region != "":
		return m.askRegionSwitch(r.region, *r.nav)
	case r.nav != nil:
		return m.navigate(*r.nav)
	case r.text != "":
		eventlog.Infof(eventlog.CatNav, "Jump: %s", r.text)
		_, cmd := m.toasts.Add(r.text, ui.ToastError, 0)
		return m, cmd
	}
	return m, nil
}

// askRegionSwitch asks to switch to region and open the resource there. On
// yes, the switch opens nav in place of the root view (H6). When an
// operation runs, the switch asks again, as R does, and nav waits for it.
func (m Model) askRegionSwitch(region string, nav appmsg.NavigateMsg) (Model, tea.Cmd) {
	c := sessionChange{profile: m.config.AWS.Profile, region: region, kind: "Region", value: region, then: &nav, gen: m.sessionGen}
	cmd := m.askQuick(fmt.Sprintf("The resource is in %s. Switch to %s and open it?", region, region),
		func(m Model) (Model, tea.Cmd) {
			if c.gen != m.sessionGen {
				return m.sessionChanged(c)
			}
			return m.requestSessionSwitch(c)
		},
		func(m Model) (Model, tea.Cmd) {
			eventlog.Infof(eventlog.CatNav, "Jump to %s cancelled", region)
			return m, nil
		})
	return m, cmd
}

// sessionChanged answers a yes that came after another switch: the change
// was asked for a session that is gone, so it does nothing and says so. A
// late yes must not undo a sign-in or a profile switch.
func (m Model) sessionChanged(c sessionChange) (Model, tea.Cmd) {
	eventlog.Infof(eventlog.CatConfig, "%s switch to %s dropped: the session changed", c.kind, c.value)
	_, cmd := m.toasts.Add("The session changed, so the switch did not run. Try again.", ui.ToastInfo, 0)
	return m, cmd
}

// targetName names the resource of t in a message: the ID or name, with the
// bucket of an object and the group of a stream.
func targetName(t jump.Target) string {
	switch t.Kind {
	case jump.Object:
		return "s3://" + t.Parent + "/" + t.ID
	case jump.LogStream:
		return t.ID + " in " + t.Parent
	case jump.Function, jump.TaskDefinition:
		if t.Qualifier != "" {
			return t.ID + ":" + t.Qualifier
		}
	default:
		// The ID alone names the resource.
	}
	return t.ID
}

// namedKinds are the kinds that a message names with "named": a name, not
// an ID.
var namedKinds = map[jump.Kind]bool{
	jump.Bucket: true, jump.Queue: true, jump.LogGroup: true, jump.Function: true,
	jump.Cluster: true, jump.Service: true, jump.TaskDefinition: true,
}

// lookupError is the toast of a failed jump read.
func lookupError(t jump.Target, s jump.Session, err error) string {
	switch {
	case errors.Is(err, aws.ErrBucketDenied):
		return fmt.Sprintf("Bucket %s is not in this account, or access is denied.", bucketOf(t))
	case errors.Is(err, aws.ErrBucketNotFound):
		return fmt.Sprintf("No bucket named %s.", bucketOf(t))
	case errors.Is(err, errNoCluster):
		return fmt.Sprintf("The ARN has no cluster, and the default cluster has no %s %s in %s. Use the long ARN, with the cluster.",
			t.Kind.Noun(), t.ID, s.Region)
	case errors.Is(err, errNotFound):
		named := " "
		if namedKinds[t.Kind] {
			named = " named "
		}
		return fmt.Sprintf("No %s%s%s in %s.", t.Kind.Noun(), named, targetName(t), s.Region)
	case aws.IsTimeout(err):
		return fmt.Sprintf("Finding %s %s took too long. Try again.", t.Kind.Noun(), targetName(t))
	}
	return aws.ErrorText(err)
}

func bucketOf(t jump.Target) string {
	if t.Kind == jump.Object {
		return t.Parent
	}
	return t.ID
}

// notFoundCodes are the AWS error codes of a read that found nothing. A
// malformed ID is also a miss: the parser already took it as an ID.
var notFoundCodes = map[string]bool{
	"InvalidInstanceID.NotFound": true, "InvalidInstanceID.Malformed": true,
	"InvalidAMIID.NotFound": true, "InvalidAMIID.Malformed": true, "InvalidAMIID.Unavailable": true,
	"InvalidGroup.NotFound": true, "InvalidGroupId.Malformed": true,
	"InvalidVpcID.NotFound": true, "InvalidVpcID.Malformed": true,
	"InvalidSubnetID.NotFound": true, "InvalidSubnetID.Malformed": true,
	"ResourceNotFoundException": true,
	// ECS answers a cluster that does not exist with this error, not with a
	// MISSING failure: a short ARN with no default cluster, or a deleted
	// cluster in a long ARN.
	"ClusterNotFoundException": true,
}

// taskDefMissing matches the ClientException of DescribeTaskDefinition for
// a family or revision that does not exist.
var taskDefMissing = regexp.MustCompile(`(?i)unable to describe task definition`)

// missing turns the "not found" answers of the reads into errNotFound.
func missing(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, aws.ErrLogGroupNotFound) || errors.Is(err, aws.ErrQueueNotFound) ||
		errors.Is(err, aws.ErrClusterNotFound) || errors.Is(err, aws.ErrServiceNotFound) ||
		errors.Is(err, aws.ErrTaskNotFound) {
		return fmt.Errorf("%w: %w", errNotFound, err)
	}
	if apiErr, ok := errors.AsType[smithy.APIError](err); ok {
		if notFoundCodes[apiErr.ErrorCode()] ||
			(apiErr.ErrorCode() == "ClientException" && taskDefMissing.MatchString(apiErr.ErrorMessage())) {
			return fmt.Errorf("%w: %w", errNotFound, err)
		}
	}
	return err
}

// focusNav is a navigation to a list with the cursor on id.
func focusNav(viewID, id string, params map[string]string) appmsg.NavigateMsg {
	p := map[string]string{"focus": id}
	maps.Copy(p, params)
	return appmsg.NavigateMsg{ViewID: viewID, Params: p}
}

// numeric matches a Lambda version number.
var numeric = regexp.MustCompile(`^\d+$`)

// lookup reads the resource of t and returns the navigation that opens it
// (the table in docs/design/jump.md, "The lookup"). A missing resource
// gives errNotFound.
func lookup(ctx context.Context, svc aws.Services, t jump.Target, s jump.Session) (appmsg.NavigateMsg, error) {
	switch t.Kind {
	case jump.Instance:
		d, err := svc.EC2.GetInstanceDetail(ctx, t.ID)
		if err := missingOrNil(d == nil, err); err != nil {
			return appmsg.NavigateMsg{}, err
		}
		return focusNav("ec2_list", t.ID, nil), nil
	case jump.AMI:
		amis, err := svc.EC2.RefreshAMIs(ctx, []string{t.ID})
		if err := missingOrNil(len(amis) == 0, err); err != nil {
			return appmsg.NavigateMsg{}, err
		}
		return focusNav("ami_list", t.ID, nil), nil
	case jump.SecurityGroup:
		g, err := svc.EC2.GetSecurityGroup(ctx, t.ID)
		if err := missingOrNil(g == nil, err); err != nil {
			return appmsg.NavigateMsg{}, err
		}
		return focusNav("sg_list", t.ID, nil), nil
	case jump.VPC:
		v, err := svc.EC2.GetVPC(ctx, t.ID)
		if err := missingOrNil(v == nil, err); err != nil {
			return appmsg.NavigateMsg{}, err
		}
		return focusNav("vpc_list", t.ID, nil), nil
	case jump.Subnet:
		sn, err := svc.EC2.GetSubnet(ctx, t.ID)
		if err := missingOrNil(sn == nil, err); err != nil {
			return appmsg.NavigateMsg{}, err
		}
		return focusNav("subnet_list", t.ID, nil), nil
	case jump.Bucket, jump.Object:
		return lookupS3(ctx, svc, t, s)
	case jump.Queue:
		url, err := svc.SQS.GetQueueURL(ctx, t.ID, t.Account)
		if err != nil {
			return appmsg.NavigateMsg{}, missing(err)
		}
		return focusNav("sqs_queues", url, nil), nil
	case jump.LogGroup, jump.LogStream:
		group := t.ID
		if t.Kind == jump.LogStream {
			group = t.Parent
		}
		g, err := svc.Logs.GetLogGroup(ctx, group)
		if err != nil {
			return appmsg.NavigateMsg{}, missing(err)
		}
		p := views.LogGroupParams(*g)
		if t.Kind == jump.LogStream {
			p["log_stream"] = t.ID
		}
		return appmsg.NavigateMsg{ViewID: "logs_events", Params: p}, nil
	case jump.Function:
		return lookupFunction(ctx, svc, t)
	case jump.Cluster:
		c, err := svc.ECS.DescribeCluster(ctx, t.ID)
		if err != nil {
			return appmsg.NavigateMsg{}, missing(err)
		}
		return focusNav("ecs_clusters", c.ARN, nil), nil
	case jump.Service, jump.Task:
		return lookupECS(ctx, svc, t)
	case jump.TaskDefinition:
		name := t.ID
		if t.Qualifier != "" {
			name += ":" + t.Qualifier
		}
		d, err := svc.ECS.DescribeTaskDefinition(ctx, name)
		if err != nil {
			return appmsg.NavigateMsg{}, missing(err)
		}
		return appmsg.NavigateMsg{ViewID: "ecs_task_definitions", Params: map[string]string{"family": d.Family, "focus": d.ARN}}, nil
	default:
		return appmsg.NavigateMsg{}, fmt.Errorf("LazyCloud cannot jump to %s", t.Raw)
	}
}

// missingOrNil returns the error of a read that gives a nil result for a
// miss, as some EC2 reads do.
func missingOrNil(isNil bool, err error) error {
	if err != nil {
		return missing(err)
	}
	if isNil {
		return errNotFound
	}
	return nil
}

// lookupS3 confirms the bucket of t and opens its objects. An object opens
// its folder with the cursor on it (Q1). The bucket must belong to the
// account of the session, when STS knows it (H11).
func lookupS3(ctx context.Context, svc aws.Services, t jump.Target, s jump.Session) (appmsg.NavigateMsg, error) {
	bucket := bucketOf(t)
	if err := svc.S3.HeadBucket(ctx, bucket, s.Account); err != nil {
		return appmsg.NavigateMsg{}, err
	}
	if t.Kind == jump.Bucket {
		return appmsg.NavigateMsg{ViewID: "s3_objects", Params: map[string]string{"bucket": bucket, "prefix": ""}}, nil
	}
	return appmsg.NavigateMsg{ViewID: "s3_objects", Params: map[string]string{
		"bucket": bucket, "prefix": objectFolder(t.ID), "focus": t.ID,
	}}, nil
}

// objectFolder returns the folder that lists key: the prefix up to its last
// "/". A folder key such as "a/b/" is listed in "a/".
func objectFolder(key string) string {
	// S3 does not clean keys, so a cut at the last "/" keeps "a//" and
	// "a/./" as S3 lists them. path.Dir would clean them.
	k := strings.TrimSuffix(key, "/")
	if i := strings.LastIndex(k, "/"); i >= 0 {
		return k[:i+1]
	}
	return ""
}

// lookupFunction confirms a function. With no qualifier it opens the
// function list. A version ($LATEST or a number) opens the versions, and
// any other qualifier the aliases, each with the cursor on it.
func lookupFunction(ctx context.Context, svc aws.Services, t jump.Target) (appmsg.NavigateMsg, error) {
	d, err := svc.Lambda.GetFunction(ctx, t.ID, t.Qualifier)
	if err != nil {
		return appmsg.NavigateMsg{}, missing(err)
	}
	f := d.Function
	switch {
	case t.Qualifier == "":
		arn := f.ARN
		if arn == "" {
			arn = f.Name
		}
		return focusNav("lambda_functions", arn, nil), nil
	case t.Qualifier == "$LATEST" || numeric.MatchString(t.Qualifier):
		return focusNav("lambda_versions", t.Qualifier, views.LambdaChildParams(f)), nil
	default:
		return focusNav("lambda_aliases", t.Qualifier, views.LambdaChildParams(f)), nil
	}
}

// lookupECS confirms a service or task and opens the list of its cluster. A
// short ARN has no cluster, so the read tries the default cluster, as AWS
// does when a call names none.
func lookupECS(ctx context.Context, svc aws.Services, t jump.Target) (appmsg.NavigateMsg, error) {
	cluster := t.Parent
	if cluster == "" {
		cluster = "default"
	}
	shortMiss := func(err error) error {
		err = missing(err)
		if t.Parent == "" && errors.Is(err, errNotFound) {
			return fmt.Errorf("%w: %w", errNoCluster, err)
		}
		return err
	}
	if t.Kind == jump.Service {
		sv, err := svc.ECS.DescribeService(ctx, cluster, t.ID)
		if err != nil {
			return appmsg.NavigateMsg{}, shortMiss(err)
		}
		return focusNav("ecs_services", sv.ARN, map[string]string{"cluster": clusterOf(sv.ClusterARN, cluster)}), nil
	}
	tk, err := svc.ECS.DescribeTask(ctx, cluster, t.ID)
	if err != nil {
		return appmsg.NavigateMsg{}, shortMiss(err)
	}
	return focusNav("ecs_tasks", tk.ARN, map[string]string{"cluster": clusterOf(tk.ClusterARN, cluster)}), nil
}

// clusterOf is the cluster parameter of a route: the ARN that AWS gave, as
// the cluster list passes it, else the name.
func clusterOf(arn, name string) string {
	if arn != "" {
		return arn
	}
	return name
}
