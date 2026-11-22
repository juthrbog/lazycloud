package aws

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

// ECSService defines the ECS calls that views depend on. Every call reads.
// See docs/design/ecs.md.
type ECSService interface {
	// ListClustersPage lists one page of clusters and describes them.
	ListClustersPage(ctx context.Context, token *string) (*ClusterPage, error)
	// DescribeCluster reads one cluster by its name or ARN, with its tags,
	// settings and configuration. It returns ErrClusterNotFound when no
	// cluster has the name.
	DescribeCluster(ctx context.Context, cluster string) (*Cluster, error)
	// ListServicesPage lists one page of the services of a cluster (up to
	// 100) and describes them in batches of 10.
	ListServicesPage(ctx context.Context, cluster string, token *string) (*ServicePage, error)
	// ListTasksPage lists one page of tasks (up to 100) and describes them.
	// service is a service name, or "" for every task of the cluster.
	// stopped lists the stopped tasks in place of the running ones.
	ListTasksPage(ctx context.Context, cluster, service string, stopped bool, token *string) (*TaskPage, error)
	// DescribeTask reads one task. It returns ErrTaskNotFound when AWS no
	// longer knows the task.
	DescribeTask(ctx context.Context, cluster, taskARN string) (*Task, error)
	// DescribeService reads one service of a cluster by its name or ARN,
	// with its tags. It returns ErrServiceNotFound when the cluster has no
	// such service.
	DescribeService(ctx context.Context, cluster, service string) (*ClusterService, error)
	// ListTaskFamiliesPage lists one page of the active task definition
	// families.
	ListTaskFamiliesPage(ctx context.Context, token *string) (*FamilyPage, error)
	// ListTaskDefinitionsPage lists one page of the active revisions of a
	// family, newest first.
	ListTaskDefinitionsPage(ctx context.Context, family string, token *string) (*TaskDefinitionPage, error)
	// DescribeTaskDefinition reads a task definition by its ARN, by
	// family:revision, or by family for the latest active revision.
	DescribeTaskDefinition(ctx context.Context, name string) (*TaskDefinition, error)
	// DescribeServiceDeployment reads one service deployment by its ARN.
	DescribeServiceDeployment(ctx context.Context, arn string) (*ServiceDeployment, error)

	// The writes. GuardECS refuses them in ReadOnly mode.

	// ForceDeployment starts a new deployment of a service with its task
	// definition, so every task is replaced.
	ForceDeployment(ctx context.Context, cluster, service string) error
	// SetDesiredCount sets the desired task count of a service. 0 is a
	// valid count. It starts no deployment.
	SetDesiredCount(ctx context.Context, cluster, service string, count int32) error
	// StopTask stops a task, by its full ARN, with a reason.
	StopTask(ctx context.Context, cluster, taskARN, reason string) error
}

// ECSServiceImpl is the real AWS-backed implementation of ECSService.
type ECSServiceImpl struct {
	client *Client
}

// NewECSService creates a real ECS service backed by client.
func NewECSService(client *Client) *ECSServiceImpl {
	return &ECSServiceImpl{client: client}
}

var _ ECSService = (*ECSServiceImpl)(nil)

// ErrTaskNotFound is returned by DescribeTask when AWS no longer knows the
// task. AWS keeps a stopped task for about an hour.
var ErrTaskNotFound = errors.New("task not found")

// ErrClusterNotFound is returned by DescribeCluster when no cluster has the
// name or ARN.
var ErrClusterNotFound = errors.New("cluster not found")

// ErrServiceNotFound is returned by DescribeService when the cluster has no
// service of that name or ARN.
var ErrServiceNotFound = errors.New("service not found")

// The batch sizes of the describe calls.
const (
	ecsClusterBatch = 100
	ecsServiceBatch = 10
	ecsTaskBatch    = 100
	ecsListPage     = 100
)

// ─── Domain types ───────────────────────────────────────────────────────────

// CapacityProviderItem is one entry of a capacity provider strategy.
type CapacityProviderItem struct {
	Name   string
	Base   int32
	Weight int32
}

// Cluster is an ECS cluster. When Err is set, AWS could not describe it,
// and only Name, ARN and Err are set.
type Cluster struct {
	Name              string
	ARN               string
	Status            string // ACTIVE, PROVISIONING, DEPROVISIONING, FAILED, INACTIVE
	ActiveServices    int32
	RunningTasks      int32
	PendingTasks      int32
	Instances         int32 // registered container instances
	CapacityProviders []string
	DefaultStrategy   []CapacityProviderItem
	ContainerInsights string // the containerInsights setting, or ""
	ExecLogging       string // NONE, DEFAULT or OVERRIDE, or ""
	ExecLogGroup      string
	ExecS3Bucket      string
	ExecKMSKey        string
	Tags              map[string]string
	Err               string
}

// ClusterPage is one page of clusters.
type ClusterPage struct {
	Clusters []Cluster
	Next     *string // nil: the last page
}

// ClusterService is a service of an ECS cluster. When Err is set, AWS could
// not describe it, and only Name, ARN, ClusterARN and Err are set.
type ClusterService struct {
	Name               string
	ARN                string
	ClusterARN         string
	Status             string // ACTIVE, DRAINING, INACTIVE
	Desired            int32
	Running            int32
	Pending            int32
	LaunchType         string // empty when the service has a capacity provider strategy
	CapacityStrategy   []CapacityProviderItem
	PlatformVersion    string
	TaskDefinition     string // ARN
	SchedulingStrategy string // REPLICA or DAEMON
	Controller         string // ECS, CODE_DEPLOY or EXTERNAL
	Strategy           string // ROLLING, BLUE_GREEN, LINEAR or CANARY
	CircuitBreaker     bool
	CircuitRollback    bool
	MinHealthyPercent  int32
	MaxPercent         int32
	ExecEnabled        bool
	Managed            bool // Express Mode manages the service
	CreatedAt          time.Time
	CreatedBy          string
	CurrentDeployment  string // ARN of the current service deployment, or ""
	Deployments        []Deployment
	Events             []ServiceEvent // newest first, at most 100
	Subnets            []string
	SecurityGroups     []string
	PublicIP           string // ENABLED or DISABLED, or ""
	LoadBalancers      []ServiceLoadBalancer
	Registries         []string // service registry ARNs
	Tags               map[string]string
	Err                string
}

// Primary returns the PRIMARY deployment, or nil.
func (s ClusterService) Primary() *Deployment {
	for i := range s.Deployments {
		if s.Deployments[i].Status == "PRIMARY" {
			return &s.Deployments[i]
		}
	}
	return nil
}

// Deployment is one entry of Service.Deployments.
type Deployment struct {
	ID             string
	Status         string // PRIMARY, ACTIVE, INACTIVE
	TaskDefinition string // ARN
	Desired        int32
	Running        int32
	Pending        int32
	Failed         int32  // failed tasks in a row
	RolloutState   string // COMPLETED, FAILED, IN_PROGRESS, or ""
	RolloutReason  string
	LaunchType     string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// ServiceEvent is one event of a service.
type ServiceEvent struct {
	Time    time.Time
	Message string
}

// ServiceLoadBalancer is a load balancer of a service.
type ServiceLoadBalancer struct {
	TargetGroupARN string
	Name           string // a Classic Load Balancer
	Container      string
	Port           int32
}

// ServicePage is one page of services.
type ServicePage struct {
	Services []ClusterService
	Next     *string
}

// Task is an ECS task. When Err is set, AWS could not describe it, and only
// ARN, ID, ClusterARN and Err are set. An empty time means that AWS did not
// set it.
type Task struct {
	ARN                string
	ID                 string // the last segment of the ARN
	ClusterARN         string
	TaskDefinition     string // ARN
	LastStatus         string
	DesiredStatus      string
	Health             string // HEALTHY, UNHEALTHY, UNKNOWN
	StopCode           string
	StoppedReason      string
	LaunchType         string
	CapacityProvider   string
	PlatformVersion    string
	PlatformFamily     string
	CPU                string // CPU units, as AWS sends them
	Memory             string // MiB, as AWS sends them
	EphemeralGiB       int32
	AvailabilityZone   string
	Group              string
	StartedBy          string
	Connectivity       string
	ExecEnabled        bool
	CreatedAt          time.Time
	PullStartedAt      time.Time
	StartedAt          time.Time
	StoppingAt         time.Time
	ExecutionStoppedAt time.Time
	StoppedAt          time.Time
	ENI                string
	PrivateIP          string
	SubnetID           string
	Containers         []Container
	Tags               map[string]string
	Err                string
}

// Container is a container of a task.
type Container struct {
	Name        string
	ARN         string
	Image       string
	ImageDigest string
	RuntimeID   string
	LastStatus  string
	Health      string
	Reason      string
	ExitCode    *int32 // nil: the container did not exit
	CPU         string
	Memory      string
	Bindings    []PortBinding
	PrivateIPs  []string
	Agents      []ManagedAgent
}

// PortBinding is a network binding of a container.
type PortBinding struct {
	ContainerPort int32
	HostPort      int32
	Protocol      string
	BindIP        string
}

// ManagedAgent is an agent of a container, for example the
// ExecuteCommandAgent of ECS Exec.
type ManagedAgent struct {
	Name      string
	Status    string
	Reason    string
	StartedAt time.Time
}

// TaskPage is one page of tasks.
type TaskPage struct {
	Tasks []Task
	Next  *string
}

// FamilyPage is one page of task definition families.
type FamilyPage struct {
	Families []string
	Next     *string
}

// TaskDefinitionRef is a revision of a family, as ListTaskDefinitions
// returns it: only the ARN, with the family and revision parsed from it.
type TaskDefinitionRef struct {
	ARN      string
	Family   string
	Revision int
}

// TaskDefinitionPage is one page of revisions.
type TaskDefinitionPage struct {
	Revisions []TaskDefinitionRef
	Next      *string
}

// TaskDefinition is one revision of a task definition.
type TaskDefinition struct {
	ARN             string
	Family          string
	Revision        int32
	Status          string // ACTIVE, INACTIVE, DELETE_IN_PROGRESS
	RegisteredAt    time.Time
	RegisteredBy    string
	DeregisteredAt  time.Time
	CPU             string
	Memory          string
	NetworkMode     string
	Compatibilities []string
	RequiresCompat  []string
	TaskRole        string
	ExecutionRole   string
	CPUArchitecture string
	OSFamily        string
	EphemeralGiB    int32
	Volumes         []string
	Containers      []ContainerDefinition
	Tags            map[string]string
}

// Container returns the definition of the container with that name, or nil.
func (d *TaskDefinition) Container(name string) *ContainerDefinition {
	for i := range d.Containers {
		if d.Containers[i].Name == name {
			return &d.Containers[i]
		}
	}
	return nil
}

// ContainerDefinition is a container of a task definition. Env holds the
// values of the environment variables, which can hold secrets.
type ContainerDefinition struct {
	Name              string
	Image             string
	CPU               int32
	Memory            int32 // MiB, 0: not set
	MemoryReservation int32
	Essential         bool
	Command           []string
	EntryPoint        []string
	Ports             []PortMapping
	Env               map[string]string
	EnvFiles          []string
	Secrets           []SecretRef
	Log               *LogConfig // nil: no log configuration
}

// PortMapping is a port mapping of a container definition.
type PortMapping struct {
	Name          string
	ContainerPort int32
	// ContainerPortRange is a range such as 8000-8010, set in place of
	// ContainerPort.
	ContainerPortRange string
	HostPort           int32
	Protocol           string
}

// SecretRef is a secret of a container: a name and the ARN of its source.
// It never holds the value.
type SecretRef struct {
	Name string
	From string
}

// LogConfig is the log configuration of a container.
type LogConfig struct {
	Driver  string // awslogs, awsfirelens, splunk, ...
	Options map[string]string
}

// Group is the CloudWatch log group of an awslogs configuration, or "".
func (l *LogConfig) Group() string { return l.option("awslogs-group") }

// Region is the Region of an awslogs configuration, or "".
func (l *LogConfig) Region() string { return l.option("awslogs-region") }

// Prefix is the stream prefix of an awslogs configuration, or "".
func (l *LogConfig) Prefix() string { return l.option("awslogs-stream-prefix") }

func (l *LogConfig) option(key string) string {
	if l == nil || l.Driver != string(ecstypes.LogDriverAwslogs) {
		return ""
	}
	return l.Options[key]
}

// Stream returns the log stream of a container of a task:
// <prefix>/<container>/<task ID>. It returns "" when the configuration is
// not awslogs or has no prefix, because the stream then has the Docker
// container ID as its name.
func (l *LogConfig) Stream(container, taskID string) string {
	p := l.Prefix()
	if p == "" {
		return ""
	}
	return p + "/" + container + "/" + taskID
}

// ServiceDeployment is a service deployment of the newer deployment API.
type ServiceDeployment struct {
	ARN              string
	Status           string // PENDING, IN_PROGRESS, SUCCESSFUL, ROLLBACK_*, ...
	StatusReason     string
	LifecycleStage   string
	CreatedAt        time.Time
	StartedAt        time.Time
	FinishedAt       time.Time
	StoppedAt        time.Time
	UpdatedAt        time.Time
	TargetRevision   string // ARN of the target service revision
	TargetRequested  int32
	TargetRunning    int32
	TargetPending    int32
	RollbackReason   string
	RollbackStarted  time.Time
	CircuitStatus    string
	CircuitFailures  int32
	CircuitThreshold int32
	AlarmStatus      string
	TriggeredAlarms  []string
}

// ─── ARN helpers ────────────────────────────────────────────────────────────

// ARNName returns the last "/" segment of an ECS ARN: the name of a cluster
// or a service, or the ID of a task, in the long and the short format. ECS
// names hold no "/".
func ARNName(arn string) string {
	if i := strings.LastIndexByte(arn, '/'); i >= 0 {
		return arn[i+1:]
	}
	return arn
}

// ParseTaskDefinition returns the family and the revision of a task
// definition ARN (.../task-definition/<family>:<revision>) or of
// family:revision. The revision is 0 when the text has none.
func ParseTaskDefinition(s string) (family string, revision int) {
	if i := strings.Index(s, "task-definition/"); i >= 0 {
		s = s[i+len("task-definition/"):]
	}
	if i := strings.LastIndexByte(s, ':'); i >= 0 {
		if n, err := strconv.Atoi(s[i+1:]); err == nil {
			return s[:i], n
		}
	}
	return s, 0
}

// TaskDefinitionLabel returns family:revision for a task definition ARN.
func TaskDefinitionLabel(arn string) string {
	if arn == "" {
		return ""
	}
	f, r := ParseTaskDefinition(arn)
	if r == 0 {
		return f
	}
	return f + ":" + strconv.Itoa(r)
}

// ─── Calls ──────────────────────────────────────────────────────────────────

// ecsFailures returns the failures of a describe call by ARN, with their
// reasons. A MISSING failure means that the resource went away between the
// list and the describe call, so it is left out.
func ecsFailures(fs []ecstypes.Failure) map[string]string {
	out := map[string]string{}
	for _, f := range fs {
		arn, reason := aws.ToString(f.Arn), aws.ToString(f.Reason)
		if reason == "MISSING" {
			continue
		}
		if d := aws.ToString(f.Detail); d != "" {
			reason += ": " + d
		}
		out[arn] = reason
	}
	return out
}

// ordered returns the items in the order of arns. An ARN that AWS did not
// return becomes a failure row when it has a failure, and is left out when
// it has none. A failure for an ARN that AWS returned is ignored: LocalStack
// reports one for a task that it found by a bare ID.
func ordered[T any](arns []string, items map[string]T, failed map[string]string, fail func(arn, reason string) T) []T {
	out := make([]T, 0, len(arns))
	for _, arn := range arns {
		if it, ok := items[arn]; ok {
			out = append(out, it)
		} else if reason, ok := failed[arn]; ok {
			out = append(out, fail(arn, reason))
		}
	}
	return out
}

// ListClustersPage returns one page of clusters, up to 100, described with
// their tags, settings and configuration.
func (svc *ECSServiceImpl) ListClustersPage(ctx context.Context, token *string) (*ClusterPage, error) {
	client := svc.client.ECSClient()
	out, err := client.ListClusters(ctx, &ecs.ListClustersInput{NextToken: token, MaxResults: aws.Int32(ecsListPage)})
	if err != nil {
		return nil, fmt.Errorf("ListClusters: %w", err)
	}
	page := &ClusterPage{Next: nonEmpty(out.NextToken)}
	items, failed := map[string]Cluster{}, map[string]string{}
	// slices.Chunk yields no batch for an empty page. An empty describe call
	// would describe the cluster named default (H1).
	for batch := range slices.Chunk(out.ClusterArns, ecsClusterBatch) {
		d, err := client.DescribeClusters(ctx, &ecs.DescribeClustersInput{
			Clusters: batch,
			Include: []ecstypes.ClusterField{ecstypes.ClusterFieldTags, ecstypes.ClusterFieldSettings,
				ecstypes.ClusterFieldConfigurations},
		})
		if err != nil {
			return nil, fmt.Errorf("DescribeClusters: %w", err)
		}
		for i := range d.Clusters {
			c := clusterFrom(&d.Clusters[i])
			items[c.ARN] = c
		}
		maps.Copy(failed, ecsFailures(d.Failures))
	}
	page.Clusters = ordered(out.ClusterArns, items, failed, func(arn, reason string) Cluster {
		return Cluster{Name: ARNName(arn), ARN: arn, Err: reason}
	})
	return page, nil
}

// DescribeCluster reads one cluster. A missing cluster comes back in
// Failures with the reason MISSING, not as an error. A deleted cluster
// comes back with the status INACTIVE, and counts as missing.
func (svc *ECSServiceImpl) DescribeCluster(ctx context.Context, cluster string) (*Cluster, error) {
	d, err := svc.client.ECSClient().DescribeClusters(ctx, &ecs.DescribeClustersInput{
		Clusters: []string{cluster},
		Include: []ecstypes.ClusterField{ecstypes.ClusterFieldTags, ecstypes.ClusterFieldSettings,
			ecstypes.ClusterFieldConfigurations},
	})
	if err != nil {
		return nil, fmt.Errorf("DescribeClusters: %w", err)
	}
	if len(d.Clusters) > 0 && aws.ToString(d.Clusters[0].Status) != "INACTIVE" {
		c := clusterFrom(&d.Clusters[0])
		return &c, nil
	}
	// ecsFailures leaves out MISSING, so any reason left is another failure.
	for _, reason := range ecsFailures(d.Failures) {
		return nil, fmt.Errorf("DescribeClusters: %s: %s", ARNName(cluster), reason)
	}
	return nil, fmt.Errorf("%w: %s", ErrClusterNotFound, ARNName(cluster))
}

func clusterFrom(c *ecstypes.Cluster) Cluster {
	out := Cluster{
		Name:              aws.ToString(c.ClusterName),
		ARN:               aws.ToString(c.ClusterArn),
		Status:            aws.ToString(c.Status),
		ActiveServices:    c.ActiveServicesCount,
		RunningTasks:      c.RunningTasksCount,
		PendingTasks:      c.PendingTasksCount,
		Instances:         c.RegisteredContainerInstancesCount,
		CapacityProviders: c.CapacityProviders,
		DefaultStrategy:   strategyFrom(c.DefaultCapacityProviderStrategy),
		Tags:              ecsTags(c.Tags),
	}
	for _, s := range c.Settings {
		if s.Name == ecstypes.ClusterSettingNameContainerInsights {
			out.ContainerInsights = aws.ToString(s.Value)
		}
	}
	if cfg := c.Configuration; cfg != nil && cfg.ExecuteCommandConfiguration != nil {
		e := cfg.ExecuteCommandConfiguration
		out.ExecLogging = string(e.Logging)
		out.ExecKMSKey = aws.ToString(e.KmsKeyId)
		if l := e.LogConfiguration; l != nil {
			out.ExecLogGroup = aws.ToString(l.CloudWatchLogGroupName)
			out.ExecS3Bucket = aws.ToString(l.S3BucketName)
		}
	}
	return out
}

// ListServicesPage returns one page of the services of a cluster, up to
// 100, described in batches of 10 with their tags.
func (svc *ECSServiceImpl) ListServicesPage(ctx context.Context, cluster string, token *string) (*ServicePage, error) {
	client := svc.client.ECSClient()
	out, err := client.ListServices(ctx, &ecs.ListServicesInput{
		Cluster: aws.String(cluster), NextToken: token, MaxResults: aws.Int32(ecsListPage),
	})
	if err != nil {
		return nil, fmt.Errorf("ListServices: %w", err)
	}
	page := &ServicePage{Next: nonEmpty(out.NextToken)}
	items, failed := map[string]ClusterService{}, map[string]string{}
	// The batches run one after the other: one page costs 11 calls, and the
	// service read bucket refills at 20 calls each second.
	for batch := range slices.Chunk(out.ServiceArns, ecsServiceBatch) {
		d, err := client.DescribeServices(ctx, &ecs.DescribeServicesInput{
			Cluster: aws.String(cluster), Services: batch,
			Include: []ecstypes.ServiceField{ecstypes.ServiceFieldTags},
		})
		if err != nil {
			return nil, fmt.Errorf("DescribeServices: %w", err)
		}
		for i := range d.Services {
			s := serviceFrom(&d.Services[i])
			items[s.ARN] = s
		}
		maps.Copy(failed, ecsFailures(d.Failures))
	}
	page.Services = ordered(out.ServiceArns, items, failed, func(arn, reason string) ClusterService {
		return ClusterService{Name: ARNName(arn), ARN: arn, ClusterARN: cluster, Err: reason}
	})
	return page, nil
}

// DescribeService reads one service with its tags. A missing service comes
// back in Failures with the reason MISSING, not as an error.
func (svc *ECSServiceImpl) DescribeService(ctx context.Context, cluster, service string) (*ClusterService, error) {
	d, err := svc.client.ECSClient().DescribeServices(ctx, &ecs.DescribeServicesInput{
		Cluster: aws.String(cluster), Services: []string{service},
		Include: []ecstypes.ServiceField{ecstypes.ServiceFieldTags},
	})
	if err != nil {
		return nil, fmt.Errorf("DescribeServices: %w", err)
	}
	if len(d.Services) > 0 {
		s := serviceFrom(&d.Services[0])
		return &s, nil
	}
	// ecsFailures leaves out MISSING, so any reason left is another failure.
	for _, reason := range ecsFailures(d.Failures) {
		return nil, fmt.Errorf("DescribeServices: %s: %s", ARNName(service), reason)
	}
	return nil, fmt.Errorf("%w: %s", ErrServiceNotFound, ARNName(service))
}

func serviceFrom(s *ecstypes.Service) ClusterService {
	out := ClusterService{
		Name:               aws.ToString(s.ServiceName),
		ARN:                aws.ToString(s.ServiceArn),
		ClusterARN:         aws.ToString(s.ClusterArn),
		Status:             aws.ToString(s.Status),
		Desired:            s.DesiredCount,
		Running:            s.RunningCount,
		Pending:            s.PendingCount,
		LaunchType:         string(s.LaunchType),
		CapacityStrategy:   strategyFrom(s.CapacityProviderStrategy),
		PlatformVersion:    aws.ToString(s.PlatformVersion),
		TaskDefinition:     aws.ToString(s.TaskDefinition),
		SchedulingStrategy: string(s.SchedulingStrategy),
		ExecEnabled:        s.EnableExecuteCommand,
		Managed:            s.ResourceManagementType == ecstypes.ResourceManagementTypeEcs,
		CreatedAt:          aws.ToTime(s.CreatedAt),
		CreatedBy:          aws.ToString(s.CreatedBy),
		CurrentDeployment:  aws.ToString(s.CurrentServiceDeployment),
		Tags:               ecsTags(s.Tags),
	}
	if s.DeploymentController != nil {
		out.Controller = string(s.DeploymentController.Type)
	}
	if c := s.DeploymentConfiguration; c != nil {
		out.Strategy = string(c.Strategy)
		out.MinHealthyPercent = aws.ToInt32(c.MinimumHealthyPercent)
		out.MaxPercent = aws.ToInt32(c.MaximumPercent)
		if b := c.DeploymentCircuitBreaker; b != nil {
			out.CircuitBreaker, out.CircuitRollback = b.Enable, b.Rollback
		}
	}
	for i := range s.Deployments {
		d := &s.Deployments[i]
		out.Deployments = append(out.Deployments, Deployment{
			ID:             aws.ToString(d.Id),
			Status:         aws.ToString(d.Status),
			TaskDefinition: aws.ToString(d.TaskDefinition),
			Desired:        d.DesiredCount,
			Running:        d.RunningCount,
			Pending:        d.PendingCount,
			Failed:         d.FailedTasks,
			RolloutState:   string(d.RolloutState),
			RolloutReason:  aws.ToString(d.RolloutStateReason),
			LaunchType:     string(d.LaunchType),
			CreatedAt:      aws.ToTime(d.CreatedAt),
			UpdatedAt:      aws.ToTime(d.UpdatedAt),
		})
	}
	for _, e := range s.Events {
		out.Events = append(out.Events, ServiceEvent{Time: aws.ToTime(e.CreatedAt), Message: aws.ToString(e.Message)})
	}
	if n := s.NetworkConfiguration; n != nil && n.AwsvpcConfiguration != nil {
		out.Subnets = n.AwsvpcConfiguration.Subnets
		out.SecurityGroups = n.AwsvpcConfiguration.SecurityGroups
		out.PublicIP = string(n.AwsvpcConfiguration.AssignPublicIp)
	}
	for _, lb := range s.LoadBalancers {
		out.LoadBalancers = append(out.LoadBalancers, ServiceLoadBalancer{
			TargetGroupARN: aws.ToString(lb.TargetGroupArn),
			Name:           aws.ToString(lb.LoadBalancerName),
			Container:      aws.ToString(lb.ContainerName),
			Port:           aws.ToInt32(lb.ContainerPort),
		})
	}
	for _, r := range s.ServiceRegistries {
		out.Registries = append(out.Registries, aws.ToString(r.RegistryArn))
	}
	return out
}

// ListTasksPage returns one page of tasks, up to 100, described with their
// tags.
func (svc *ECSServiceImpl) ListTasksPage(ctx context.Context, cluster, service string, stopped bool, token *string) (*TaskPage, error) {
	client := svc.client.ECSClient()
	in := &ecs.ListTasksInput{Cluster: aws.String(cluster), NextToken: token, MaxResults: aws.Int32(ecsListPage),
		DesiredStatus: ecstypes.DesiredStatusRunning}
	if stopped {
		in.DesiredStatus = ecstypes.DesiredStatusStopped
	}
	if service != "" {
		in.ServiceName = aws.String(service)
	}
	out, err := client.ListTasks(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("ListTasks: %w", err)
	}
	page := &TaskPage{Next: nonEmpty(out.NextToken)}
	tasks, err := svc.describeTasks(ctx, client, cluster, out.TaskArns)
	if err != nil {
		return nil, err
	}
	page.Tasks = tasks
	return page, nil
}

func (svc *ECSServiceImpl) describeTasks(ctx context.Context, client *ecs.Client, cluster string, arns []string) ([]Task, error) {
	items, failed := map[string]Task{}, map[string]string{}
	for batch := range slices.Chunk(arns, ecsTaskBatch) {
		d, err := client.DescribeTasks(ctx, &ecs.DescribeTasksInput{
			Cluster: aws.String(cluster), Tasks: batch,
			Include: []ecstypes.TaskField{ecstypes.TaskFieldTags},
		})
		if err != nil {
			return nil, fmt.Errorf("DescribeTasks: %w", err)
		}
		for i := range d.Tasks {
			t := taskFrom(&d.Tasks[i])
			items[t.ARN] = t
		}
		maps.Copy(failed, ecsFailures(d.Failures))
	}
	return ordered(arns, items, failed, func(arn, reason string) Task {
		return Task{ARN: arn, ID: ARNName(arn), ClusterARN: cluster, Err: reason}
	}), nil
}

// DescribeTask reads one task.
func (svc *ECSServiceImpl) DescribeTask(ctx context.Context, cluster, taskARN string) (*Task, error) {
	tasks, err := svc.describeTasks(ctx, svc.client.ECSClient(), cluster, []string{taskARN})
	if err != nil {
		return nil, err
	}
	if len(tasks) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrTaskNotFound, ARNName(taskARN))
	}
	if tasks[0].Err != "" {
		return nil, fmt.Errorf("DescribeTasks: %s: %s", ARNName(taskARN), tasks[0].Err)
	}
	return &tasks[0], nil
}

func taskFrom(t *ecstypes.Task) Task {
	arn := aws.ToString(t.TaskArn)
	out := Task{
		ARN:                arn,
		ID:                 ARNName(arn),
		ClusterARN:         aws.ToString(t.ClusterArn),
		TaskDefinition:     aws.ToString(t.TaskDefinitionArn),
		LastStatus:         aws.ToString(t.LastStatus),
		DesiredStatus:      aws.ToString(t.DesiredStatus),
		Health:             string(t.HealthStatus),
		StopCode:           string(t.StopCode),
		StoppedReason:      aws.ToString(t.StoppedReason),
		LaunchType:         string(t.LaunchType),
		CapacityProvider:   aws.ToString(t.CapacityProviderName),
		PlatformVersion:    aws.ToString(t.PlatformVersion),
		PlatformFamily:     aws.ToString(t.PlatformFamily),
		CPU:                aws.ToString(t.Cpu),
		Memory:             aws.ToString(t.Memory),
		AvailabilityZone:   aws.ToString(t.AvailabilityZone),
		Group:              aws.ToString(t.Group),
		StartedBy:          aws.ToString(t.StartedBy),
		Connectivity:       string(t.Connectivity),
		ExecEnabled:        t.EnableExecuteCommand,
		CreatedAt:          aws.ToTime(t.CreatedAt),
		PullStartedAt:      aws.ToTime(t.PullStartedAt),
		StartedAt:          aws.ToTime(t.StartedAt),
		StoppingAt:         aws.ToTime(t.StoppingAt),
		ExecutionStoppedAt: aws.ToTime(t.ExecutionStoppedAt),
		StoppedAt:          aws.ToTime(t.StoppedAt),
		Tags:               ecsTags(t.Tags),
	}
	if t.EphemeralStorage != nil {
		out.EphemeralGiB = t.EphemeralStorage.SizeInGiB
	}
	for _, a := range t.Attachments {
		if aws.ToString(a.Type) != "ElasticNetworkInterface" {
			continue
		}
		for _, kv := range a.Details {
			// The key names are not in the SDK. AWS and LocalStack send
			// privateIPv4Address; compare without case to be safe.
			switch strings.ToLower(aws.ToString(kv.Name)) {
			case "networkinterfaceid":
				out.ENI = aws.ToString(kv.Value)
			case "privateipv4address":
				out.PrivateIP = aws.ToString(kv.Value)
			case "subnetid":
				out.SubnetID = aws.ToString(kv.Value)
			}
		}
	}
	for i := range t.Containers {
		out.Containers = append(out.Containers, containerFrom(&t.Containers[i]))
	}
	return out
}

func containerFrom(c *ecstypes.Container) Container {
	out := Container{
		Name:        aws.ToString(c.Name),
		ARN:         aws.ToString(c.ContainerArn),
		Image:       aws.ToString(c.Image),
		ImageDigest: aws.ToString(c.ImageDigest),
		RuntimeID:   aws.ToString(c.RuntimeId),
		LastStatus:  aws.ToString(c.LastStatus),
		Health:      string(c.HealthStatus),
		Reason:      aws.ToString(c.Reason),
		ExitCode:    c.ExitCode,
		CPU:         aws.ToString(c.Cpu),
		Memory:      aws.ToString(c.Memory),
	}
	for _, b := range c.NetworkBindings {
		out.Bindings = append(out.Bindings, PortBinding{
			ContainerPort: aws.ToInt32(b.ContainerPort),
			HostPort:      aws.ToInt32(b.HostPort),
			Protocol:      string(b.Protocol),
			BindIP:        aws.ToString(b.BindIP),
		})
	}
	for _, n := range c.NetworkInterfaces {
		if ip := aws.ToString(n.PrivateIpv4Address); ip != "" {
			out.PrivateIPs = append(out.PrivateIPs, ip)
		}
	}
	for _, a := range c.ManagedAgents {
		out.Agents = append(out.Agents, ManagedAgent{
			Name:      string(a.Name),
			Status:    aws.ToString(a.LastStatus),
			Reason:    aws.ToString(a.Reason),
			StartedAt: aws.ToTime(a.LastStartedAt),
		})
	}
	return out
}

// ListTaskFamiliesPage returns one page of the active families, up to 100.
func (svc *ECSServiceImpl) ListTaskFamiliesPage(ctx context.Context, token *string) (*FamilyPage, error) {
	out, err := svc.client.ECSClient().ListTaskDefinitionFamilies(ctx, &ecs.ListTaskDefinitionFamiliesInput{
		NextToken: token, MaxResults: aws.Int32(ecsListPage), Status: ecstypes.TaskDefinitionFamilyStatusActive,
	})
	if err != nil {
		return nil, fmt.Errorf("ListTaskDefinitionFamilies: %w", err)
	}
	return &FamilyPage{Families: out.Families, Next: nonEmpty(out.NextToken)}, nil
}

// ListTaskDefinitionsPage returns one page of the active revisions of a
// family, up to 100, newest first. FamilyPrefix matches the exact family.
func (svc *ECSServiceImpl) ListTaskDefinitionsPage(ctx context.Context, family string, token *string) (*TaskDefinitionPage, error) {
	out, err := svc.client.ECSClient().ListTaskDefinitions(ctx, &ecs.ListTaskDefinitionsInput{
		FamilyPrefix: aws.String(family), NextToken: token, MaxResults: aws.Int32(ecsListPage),
		Sort: ecstypes.SortOrderDesc, Status: ecstypes.TaskDefinitionStatusActive,
	})
	if err != nil {
		return nil, fmt.Errorf("ListTaskDefinitions: %w", err)
	}
	page := &TaskDefinitionPage{Next: nonEmpty(out.NextToken)}
	for _, arn := range out.TaskDefinitionArns {
		f, r := ParseTaskDefinition(arn)
		page.Revisions = append(page.Revisions, TaskDefinitionRef{ARN: arn, Family: f, Revision: r})
	}
	return page, nil
}

// DescribeTaskDefinition reads one task definition with its tags.
func (svc *ECSServiceImpl) DescribeTaskDefinition(ctx context.Context, name string) (*TaskDefinition, error) {
	out, err := svc.client.ECSClient().DescribeTaskDefinition(ctx, &ecs.DescribeTaskDefinitionInput{
		TaskDefinition: aws.String(name), Include: []ecstypes.TaskDefinitionField{ecstypes.TaskDefinitionFieldTags},
	})
	if err != nil {
		return nil, fmt.Errorf("DescribeTaskDefinition: %w", err)
	}
	if out.TaskDefinition == nil {
		return nil, fmt.Errorf("DescribeTaskDefinition: no task definition for %s", name)
	}
	d := taskDefinitionFrom(out.TaskDefinition)
	// The tags are on the output, not in the task definition.
	d.Tags = ecsTags(out.Tags)
	return &d, nil
}

func taskDefinitionFrom(t *ecstypes.TaskDefinition) TaskDefinition {
	out := TaskDefinition{
		ARN:            aws.ToString(t.TaskDefinitionArn),
		Family:         aws.ToString(t.Family),
		Revision:       t.Revision,
		Status:         string(t.Status),
		RegisteredAt:   aws.ToTime(t.RegisteredAt),
		RegisteredBy:   aws.ToString(t.RegisteredBy),
		DeregisteredAt: aws.ToTime(t.DeregisteredAt),
		CPU:            aws.ToString(t.Cpu),
		Memory:         aws.ToString(t.Memory),
		NetworkMode:    string(t.NetworkMode),
		TaskRole:       aws.ToString(t.TaskRoleArn),
		ExecutionRole:  aws.ToString(t.ExecutionRoleArn),
	}
	for _, c := range t.Compatibilities {
		out.Compatibilities = append(out.Compatibilities, string(c))
	}
	for _, c := range t.RequiresCompatibilities {
		out.RequiresCompat = append(out.RequiresCompat, string(c))
	}
	if p := t.RuntimePlatform; p != nil {
		out.CPUArchitecture, out.OSFamily = string(p.CpuArchitecture), string(p.OperatingSystemFamily)
	}
	if t.EphemeralStorage != nil {
		out.EphemeralGiB = t.EphemeralStorage.SizeInGiB
	}
	for _, v := range t.Volumes {
		out.Volumes = append(out.Volumes, aws.ToString(v.Name))
	}
	for i := range t.ContainerDefinitions {
		out.Containers = append(out.Containers, containerDefinitionFrom(&t.ContainerDefinitions[i]))
	}
	return out
}

func containerDefinitionFrom(c *ecstypes.ContainerDefinition) ContainerDefinition {
	out := ContainerDefinition{
		Name:              aws.ToString(c.Name),
		Image:             aws.ToString(c.Image),
		CPU:               c.Cpu,
		Memory:            aws.ToInt32(c.Memory),
		MemoryReservation: aws.ToInt32(c.MemoryReservation),
		// A container is essential unless the definition says otherwise.
		Essential:  c.Essential == nil || *c.Essential,
		Command:    c.Command,
		EntryPoint: c.EntryPoint,
	}
	for _, p := range c.PortMappings {
		out.Ports = append(out.Ports, PortMapping{
			Name:               aws.ToString(p.Name),
			ContainerPort:      aws.ToInt32(p.ContainerPort),
			ContainerPortRange: aws.ToString(p.ContainerPortRange),
			HostPort:           aws.ToInt32(p.HostPort),
			Protocol:           string(p.Protocol),
		})
	}
	if len(c.Environment) > 0 {
		out.Env = make(map[string]string, len(c.Environment))
		for _, kv := range c.Environment {
			out.Env[aws.ToString(kv.Name)] = aws.ToString(kv.Value)
		}
	}
	for _, f := range c.EnvironmentFiles {
		out.EnvFiles = append(out.EnvFiles, aws.ToString(f.Value))
	}
	for _, s := range c.Secrets {
		out.Secrets = append(out.Secrets, SecretRef{Name: aws.ToString(s.Name), From: aws.ToString(s.ValueFrom)})
	}
	if l := c.LogConfiguration; l != nil {
		out.Log = &LogConfig{Driver: string(l.LogDriver), Options: l.Options}
	}
	return out
}

// DescribeServiceDeployment reads one service deployment.
func (svc *ECSServiceImpl) DescribeServiceDeployment(ctx context.Context, arn string) (*ServiceDeployment, error) {
	out, err := svc.client.ECSClient().DescribeServiceDeployments(ctx, &ecs.DescribeServiceDeploymentsInput{
		ServiceDeploymentArns: []string{arn},
	})
	if err != nil {
		return nil, fmt.Errorf("DescribeServiceDeployments: %w", err)
	}
	if len(out.ServiceDeployments) == 0 {
		reason := "MISSING"
		if len(out.Failures) > 0 {
			reason = aws.ToString(out.Failures[0].Reason)
		}
		return nil, fmt.Errorf("DescribeServiceDeployments: %s: %s", ARNName(arn), reason)
	}
	d := &out.ServiceDeployments[0]
	sd := &ServiceDeployment{
		ARN:            aws.ToString(d.ServiceDeploymentArn),
		Status:         string(d.Status),
		StatusReason:   aws.ToString(d.StatusReason),
		LifecycleStage: string(d.LifecycleStage),
		CreatedAt:      aws.ToTime(d.CreatedAt),
		StartedAt:      aws.ToTime(d.StartedAt),
		FinishedAt:     aws.ToTime(d.FinishedAt),
		StoppedAt:      aws.ToTime(d.StoppedAt),
		UpdatedAt:      aws.ToTime(d.UpdatedAt),
	}
	if r := d.TargetServiceRevision; r != nil {
		sd.TargetRevision = aws.ToString(r.Arn)
		sd.TargetRequested, sd.TargetRunning, sd.TargetPending = r.RequestedTaskCount, r.RunningTaskCount, r.PendingTaskCount
	}
	if r := d.Rollback; r != nil {
		sd.RollbackReason, sd.RollbackStarted = aws.ToString(r.Reason), aws.ToTime(r.StartedAt)
	}
	if b := d.DeploymentCircuitBreaker; b != nil {
		sd.CircuitStatus, sd.CircuitFailures, sd.CircuitThreshold = string(b.Status), b.FailureCount, b.Threshold
	}
	if a := d.Alarms; a != nil {
		sd.AlarmStatus, sd.TriggeredAlarms = string(a.Status), a.TriggeredAlarmNames
	}
	return sd, nil
}

func strategyFrom(items []ecstypes.CapacityProviderStrategyItem) []CapacityProviderItem {
	var out []CapacityProviderItem
	for _, it := range items {
		out = append(out, CapacityProviderItem{Name: aws.ToString(it.CapacityProvider), Base: it.Base, Weight: it.Weight})
	}
	return out
}

func ecsTags(tags []ecstypes.Tag) map[string]string {
	if len(tags) == 0 {
		return nil
	}
	out := make(map[string]string, len(tags))
	for _, t := range tags {
		out[aws.ToString(t.Key)] = aws.ToString(t.Value)
	}
	return out
}

// ─── Writes ─────────────────────────────────────────────────────────────────

// ForceDeployment calls UpdateService with ForceNewDeployment only. A
// CODE_DEPLOY or EXTERNAL service rejects it (H9), so the view refuses it
// first.
func (svc *ECSServiceImpl) ForceDeployment(ctx context.Context, cluster, service string) error {
	_, err := svc.client.ECSClient().UpdateService(ctx, &ecs.UpdateServiceInput{
		Cluster: aws.String(cluster), Service: aws.String(service), ForceNewDeployment: true,
	})
	if err != nil {
		return fmt.Errorf("UpdateService: %w", err)
	}
	return nil
}

// SetDesiredCount calls UpdateService with DesiredCount only. The count
// is a pointer, so 0 is sent.
func (svc *ECSServiceImpl) SetDesiredCount(ctx context.Context, cluster, service string, count int32) error {
	_, err := svc.client.ECSClient().UpdateService(ctx, &ecs.UpdateServiceInput{
		Cluster: aws.String(cluster), Service: aws.String(service), DesiredCount: aws.Int32(count),
	})
	if err != nil {
		return fmt.Errorf("UpdateService: %w", err)
	}
	return nil
}

// StopTask stops a task. The task gets the stop code UserInitiated.
func (svc *ECSServiceImpl) StopTask(ctx context.Context, cluster, taskARN, reason string) error {
	in := &ecs.StopTaskInput{Cluster: aws.String(cluster), Task: aws.String(taskARN)}
	if reason != "" {
		in.Reason = aws.String(reason)
	}
	if _, err := svc.client.ECSClient().StopTask(ctx, in); err != nil {
		return fmt.Errorf("StopTask: %w", err)
	}
	return nil
}
