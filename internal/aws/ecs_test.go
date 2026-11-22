package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeECS answers ECS calls in the awsjson 1.1 protocol. Each operation has
// a reply; the fake records the JSON input of each call.
type fakeECS struct {
	mu      sync.Mutex
	replies map[string]func(in map[string]any) (int, string)
	calls   []fakeLogsCall
}

func newFakeECS(t *testing.T) (*fakeECS, *ECSServiceImpl) {
	t.Helper()
	f := &fakeECS{replies: map[string]func(map[string]any) (int, string){}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_MAX_ATTEMPTS", "1")
	c, err := NewClient("", "us-east-1", srv.URL)
	require.NoError(t, err)
	return f, NewECSService(c)
}

func (f *fakeECS) reply(op, body string) {
	f.replies[op] = func(map[string]any) (int, string) { return http.StatusOK, body }
}

func (f *fakeECS) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), "AmazonEC2ContainerServiceV20141113.")
	raw, _ := io.ReadAll(r.Body)
	in := map[string]any{}
	_ = json.Unmarshal(raw, &in)
	f.calls = append(f.calls, fakeLogsCall{op: op, in: in})
	fn, ok := f.replies[op]
	if !ok {
		http.Error(w, "unexpected operation "+op, http.StatusNotImplemented)
		return
	}
	status, body := fn(in)
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	if status != http.StatusOK {
		var e struct {
			Type string `json:"__type"`
		}
		_ = json.Unmarshal([]byte(body), &e)
		w.Header().Set("X-Amzn-Errortype", e.Type)
	}
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

// inputs returns the inputs of every call of op, in order.
func (f *fakeECS) inputs(op string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []map[string]any
	for _, c := range f.calls {
		if c.op == op {
			out = append(out, c.in)
		}
	}
	return out
}

func (f *fakeECS) last(t *testing.T, op string) map[string]any {
	t.Helper()
	in := f.inputs(op)
	require.NotEmpty(t, in, "no %s call", op)
	return in[len(in)-1]
}

func strs(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

func jsonList(items []string) string {
	b, _ := json.Marshal(items)
	return string(b)
}

const ecsCluster = "arn:aws:ecs:us-east-1:111122223333:cluster/prod"

func serviceARN(i int) string {
	return fmt.Sprintf("arn:aws:ecs:us-east-1:111122223333:service/prod/svc-%02d", i)
}

// A page of 23 services is described in batches of 10, 10 and 3, in
// order, and the rows keep the order of the list even when a describe
// answer comes back in another order.
func TestListServicesPageDescribesInBatchesOfTen(t *testing.T) {
	f, svc := newFakeECS(t)
	var arns []string
	for i := range 23 {
		arns = append(arns, serviceARN(i))
	}
	f.reply("ListServices", `{"serviceArns":`+jsonList(arns)+`,"nextToken":"t2"}`)
	f.replies["DescribeServices"] = func(in map[string]any) (int, string) {
		var out []string
		for _, a := range slices.Backward(strs(in["services"])) {
			out = append(out, fmt.Sprintf(`{"serviceArn":%q,"serviceName":%q,"clusterArn":%q,"status":"ACTIVE"}`, a, ARNName(a), ecsCluster))
		}
		return http.StatusOK, `{"services":[` + strings.Join(out, ",") + `],"failures":[]}`
	}

	page, err := svc.ListServicesPage(context.Background(), ecsCluster, new("t1"))
	require.NoError(t, err)
	in := f.last(t, "ListServices")
	assert.Equal(t, ecsCluster, in["cluster"])
	assert.Equal(t, "t1", in["nextToken"])
	assert.InDelta(t, 100, in["maxResults"], 0)

	calls := f.inputs("DescribeServices")
	require.Len(t, calls, 3)
	assert.Equal(t, arns[:10], strs(calls[0]["services"]))
	assert.Equal(t, arns[10:20], strs(calls[1]["services"]))
	assert.Equal(t, arns[20:], strs(calls[2]["services"]))
	for _, c := range calls {
		assert.Equal(t, ecsCluster, c["cluster"])
		assert.Equal(t, []string{"TAGS"}, strs(c["include"]))
	}
	require.Len(t, page.Services, 23)
	for i, s := range page.Services {
		assert.Equal(t, arns[i], s.ARN)
	}
	assert.Equal(t, "t2", *page.Next)
}

// An empty list page makes no describe call: an empty services or tasks
// batch fails on AWS, and an empty clusters batch describes the cluster
// named default. An empty token ends the walk.
func TestECSEmptyPageMakesNoDescribeCall(t *testing.T) {
	f, svc := newFakeECS(t)
	f.reply("ListClusters", `{"clusterArns":[],"nextToken":""}`)
	f.reply("ListServices", `{"serviceArns":[],"nextToken":"more"}`)
	f.reply("ListTasks", `{"taskArns":[],"nextToken":""}`)

	cp, err := svc.ListClustersPage(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, cp.Clusters)
	assert.Nil(t, cp.Next, "an empty token ends the walk")

	sp, err := svc.ListServicesPage(context.Background(), ecsCluster, nil)
	require.NoError(t, err)
	assert.Empty(t, sp.Services)
	assert.Equal(t, "more", *sp.Next, "an empty page can still have a next page")

	tp, err := svc.ListTasksPage(context.Background(), ecsCluster, "", false, nil)
	require.NoError(t, err)
	assert.Empty(t, tp.Tasks)
	assert.Nil(t, tp.Next)

	assert.Empty(t, f.inputs("DescribeClusters"))
	assert.Empty(t, f.inputs("DescribeServices"))
	assert.Empty(t, f.inputs("DescribeTasks"))
}

// A MISSING failure is dropped, because the resource went away between the
// two calls. Another failure becomes a row with the reason. A failure for a
// task that the call returned, or for an ARN that the list did not have
// (LocalStack, bare IDs), is ignored.
func TestECSDescribeFailures(t *testing.T) {
	f, svc := newFakeECS(t)
	a, b, c := serviceARN(1), serviceARN(2), serviceARN(3)
	f.reply("ListServices", `{"serviceArns":`+jsonList([]string{a, b, c})+`}`)
	f.reply("DescribeServices", `{"services":[{"serviceArn":"`+a+`","serviceName":"svc-01","status":"ACTIVE"}],
"failures":[{"arn":"`+b+`","reason":"MISSING"},{"arn":"`+c+`","reason":"ACCESS_DENIED","detail":"no ecs:DescribeServices"},
{"arn":"svc-01","reason":"MISSING"}]}`)

	page, err := svc.ListServicesPage(context.Background(), ecsCluster, nil)
	require.NoError(t, err)
	require.Len(t, page.Services, 2)
	assert.Equal(t, "svc-01", page.Services[0].Name)
	assert.Empty(t, page.Services[0].Err)
	assert.Equal(t, ClusterService{Name: "svc-03", ARN: c, ClusterARN: ecsCluster,
		Err: "ACCESS_DENIED: no ecs:DescribeServices"}, page.Services[1])

	task := "arn:aws:ecs:us-east-1:111122223333:task/prod/abc123"
	f.reply("ListTasks", `{"taskArns":["`+task+`"]}`)
	f.reply("DescribeTasks", `{"tasks":[{"taskArn":"`+task+`","lastStatus":"RUNNING"}],"failures":[{"arn":"abc123","reason":"OTHER"},
{"arn":"`+task+`","reason":"OTHER"}]}`)
	tp, err := svc.ListTasksPage(context.Background(), ecsCluster, "", false, nil)
	require.NoError(t, err)
	require.Len(t, tp.Tasks, 1)
	assert.Empty(t, tp.Tasks[0].Err, "a failure for a returned task is ignored")
}

func TestListClustersPageReadsEveryField(t *testing.T) {
	f, svc := newFakeECS(t)
	f.reply("ListClusters", `{"clusterArns":["`+ecsCluster+`"]}`)
	f.reply("DescribeClusters", `{"clusters":[{"clusterArn":"`+ecsCluster+`","clusterName":"prod","status":"ACTIVE",
"activeServicesCount":3,"runningTasksCount":5,"pendingTasksCount":1,"registeredContainerInstancesCount":2,
"capacityProviders":["FARGATE","FARGATE_SPOT"],
"defaultCapacityProviderStrategy":[{"capacityProvider":"FARGATE","base":1,"weight":2}],
"settings":[{"name":"containerInsights","value":"enhanced"}],
"configuration":{"executeCommandConfiguration":{"kmsKeyId":"k1","logging":"OVERRIDE",
  "logConfiguration":{"cloudWatchLogGroupName":"/ecs/exec","s3BucketName":"exec-logs"}}},
"tags":[{"key":"team","value":"web"}]}],"failures":[]}`)

	page, err := svc.ListClustersPage(context.Background(), nil)
	require.NoError(t, err)
	in := f.last(t, "DescribeClusters")
	assert.Equal(t, []string{ecsCluster}, strs(in["clusters"]))
	assert.Equal(t, []string{"TAGS", "SETTINGS", "CONFIGURATIONS"}, strs(in["include"]))
	assert.InDelta(t, 100, f.last(t, "ListClusters")["maxResults"], 0)
	require.Len(t, page.Clusters, 1)
	assert.Equal(t, Cluster{
		Name: "prod", ARN: ecsCluster, Status: "ACTIVE",
		ActiveServices: 3, RunningTasks: 5, PendingTasks: 1, Instances: 2,
		CapacityProviders: []string{"FARGATE", "FARGATE_SPOT"},
		DefaultStrategy:   []CapacityProviderItem{{Name: "FARGATE", Base: 1, Weight: 2}},
		ContainerInsights: "enhanced",
		ExecLogging:       "OVERRIDE", ExecLogGroup: "/ecs/exec", ExecS3Bucket: "exec-logs", ExecKMSKey: "k1",
		Tags: map[string]string{"team": "web"},
	}, page.Clusters[0])
}

// A cluster that DescribeClusters cannot read is a row with its reason.
func TestListClustersPageKeepsAFailedCluster(t *testing.T) {
	f, svc := newFakeECS(t)
	other := "arn:aws:ecs:us-east-1:111122223333:cluster/other"
	f.reply("ListClusters", `{"clusterArns":["`+other+`"]}`)
	f.reply("DescribeClusters", `{"clusters":[],"failures":[{"arn":"`+other+`","reason":"ACCESS_DENIED"}]}`)
	page, err := svc.ListClustersPage(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, []Cluster{{Name: "other", ARN: other, Err: "ACCESS_DENIED"}}, page.Clusters)
}

func TestServiceFromReadsEveryField(t *testing.T) {
	f, svc := newFakeECS(t)
	a := serviceARN(1)
	f.reply("ListServices", `{"serviceArns":["`+a+`"]}`)
	f.reply("DescribeServices", `{"services":[{"serviceArn":"`+a+`","serviceName":"web","clusterArn":"`+ecsCluster+`",
"status":"ACTIVE","desiredCount":3,"runningCount":2,"pendingCount":1,
"capacityProviderStrategy":[{"capacityProvider":"FARGATE_SPOT","weight":1}],"platformVersion":"LATEST",
"taskDefinition":"arn:aws:ecs:us-east-1:111122223333:task-definition/web:7","schedulingStrategy":"REPLICA",
"deploymentController":{"type":"ECS"},
"deploymentConfiguration":{"strategy":"ROLLING","minimumHealthyPercent":100,"maximumPercent":200,
  "deploymentCircuitBreaker":{"enable":true,"rollback":true}},
"enableExecuteCommand":true,"resourceManagementType":"ECS","createdAt":1763600000,"createdBy":"arn:aws:iam::1:role/ci",
"currentServiceDeployment":"arn:aws:ecs:us-east-1:111122223333:service-deployment/prod/web/d1",
"deployments":[{"id":"ecs-svc/2","status":"ACTIVE","taskDefinition":"td:6","desiredCount":1,"runningCount":1},
  {"id":"ecs-svc/3","status":"PRIMARY","taskDefinition":"td:7","desiredCount":3,"runningCount":1,"pendingCount":1,
   "failedTasks":2,"rolloutState":"IN_PROGRESS","rolloutStateReason":"ECS deployment ecs-svc/3 in progress.",
   "launchType":"FARGATE","createdAt":1763600100,"updatedAt":1763600200}],
"events":[{"id":"e2","createdAt":1763600300,"message":"(service web) has reached a steady state."},
  {"id":"e1","createdAt":1763600000,"message":"(service web) started 1 tasks."}],
"networkConfiguration":{"awsvpcConfiguration":{"subnets":["subnet-1"],"securityGroups":["sg-1"],"assignPublicIp":"DISABLED"}},
"loadBalancers":[{"targetGroupArn":"arn:tg","containerName":"app","containerPort":8080}],
"serviceRegistries":[{"registryArn":"arn:sd"}],
"tags":[{"key":"env","value":"prod"}]}]}`)

	page, err := svc.ListServicesPage(context.Background(), ecsCluster, nil)
	require.NoError(t, err)
	require.Len(t, page.Services, 1)
	s := page.Services[0]
	assert.Equal(t, "web", s.Name)
	assert.Equal(t, ecsCluster, s.ClusterARN)
	assert.Equal(t, [3]int32{3, 2, 1}, [3]int32{s.Desired, s.Running, s.Pending})
	assert.Empty(t, s.LaunchType)
	assert.Equal(t, []CapacityProviderItem{{Name: "FARGATE_SPOT", Weight: 1}}, s.CapacityStrategy)
	assert.Equal(t, "web:7", TaskDefinitionLabel(s.TaskDefinition))
	assert.Equal(t, "ECS", s.Controller)
	assert.Equal(t, "ROLLING", s.Strategy)
	assert.True(t, s.CircuitBreaker)
	assert.True(t, s.CircuitRollback)
	assert.Equal(t, [2]int32{100, 200}, [2]int32{s.MinHealthyPercent, s.MaxPercent})
	assert.True(t, s.ExecEnabled)
	assert.True(t, s.Managed, "resourceManagementType ECS is Express Mode")
	assert.Equal(t, time.Unix(1763600000, 0).UTC(), s.CreatedAt.UTC())
	assert.Equal(t, "arn:aws:iam::1:role/ci", s.CreatedBy)
	assert.Contains(t, s.CurrentDeployment, "service-deployment/")
	require.Len(t, s.Deployments, 2)
	p := s.Primary()
	require.NotNil(t, p)
	assert.Equal(t, Deployment{ID: "ecs-svc/3", Status: "PRIMARY", TaskDefinition: "td:7", Desired: 3, Running: 1,
		Pending: 1, Failed: 2, RolloutState: "IN_PROGRESS", RolloutReason: "ECS deployment ecs-svc/3 in progress.",
		LaunchType: "FARGATE", CreatedAt: time.Unix(1763600100, 0), UpdatedAt: time.Unix(1763600200, 0)}, withLocal(*p))
	require.Len(t, s.Events, 2)
	assert.Equal(t, "(service web) has reached a steady state.", s.Events[0].Message, "AWS sends the newest first")
	assert.Equal(t, []string{"subnet-1"}, s.Subnets)
	assert.Equal(t, []string{"sg-1"}, s.SecurityGroups)
	assert.Equal(t, "DISABLED", s.PublicIP)
	assert.Equal(t, []ServiceLoadBalancer{{TargetGroupARN: "arn:tg", Container: "app", Port: 8080}}, s.LoadBalancers)
	assert.Equal(t, []string{"arn:sd"}, s.Registries)
	assert.Equal(t, map[string]string{"env": "prod"}, s.Tags)

	assert.Nil(t, ClusterService{}.Primary())
}

// withLocal puts the times of d in the local zone, as time.Unix does, so
// that assert.Equal compares the instants.
func withLocal(d Deployment) Deployment {
	d.CreatedAt, d.UpdatedAt = d.CreatedAt.Local(), d.UpdatedAt.Local()
	return d
}

func TestListTasksPageSendsTheStatusAndTheServiceName(t *testing.T) {
	f, svc := newFakeECS(t)
	f.reply("ListTasks", `{"taskArns":[],"nextToken":"n2"}`)
	ctx := context.Background()

	page, err := svc.ListTasksPage(ctx, ecsCluster, "web", false, new("n1"))
	require.NoError(t, err)
	require.NotNil(t, page.Next)
	assert.Equal(t, "n2", *page.Next)
	in := f.last(t, "ListTasks")
	assert.Equal(t, ecsCluster, in["cluster"])
	assert.Equal(t, "web", in["serviceName"], "the bare service name, not the ARN")
	assert.Equal(t, "RUNNING", in["desiredStatus"])
	assert.Equal(t, "n1", in["nextToken"])
	assert.InDelta(t, 100, in["maxResults"], 0)

	_, err = svc.ListTasksPage(ctx, ecsCluster, "", true, nil)
	require.NoError(t, err)
	in = f.last(t, "ListTasks")
	assert.NotContains(t, in, "serviceName", "no service: every task of the cluster")
	assert.Equal(t, "STOPPED", in["desiredStatus"])
}

func TestTaskFromReadsEveryField(t *testing.T) {
	f, svc := newFakeECS(t)
	long := "arn:aws:ecs:us-east-1:111122223333:task/prod/0123456789abcdef"
	short := "arn:aws:ecs:us-east-1:111122223333:task/fedcba9876543210"
	f.reply("ListTasks", `{"taskArns":["`+long+`","`+short+`"]}`)
	f.reply("DescribeTasks", `{"tasks":[{"taskArn":"`+short+`","clusterArn":"`+ecsCluster+`","lastStatus":"STOPPED",
"stopCode":"EssentialContainerExited","stoppedReason":"Essential container in task exited",
"stoppingAt":1763600400,"executionStoppedAt":1763600410,"stoppedAt":1763600420},
{"taskArn":"`+long+`","clusterArn":"`+ecsCluster+`","taskDefinitionArn":"arn:aws:ecs:us-east-1:111122223333:task-definition/web:7",
"lastStatus":"RUNNING","desiredStatus":"RUNNING","healthStatus":"HEALTHY","launchType":"FARGATE",
"capacityProviderName":"FARGATE","platformVersion":"1.4.0","platformFamily":"Linux","cpu":"256","memory":"512",
"ephemeralStorage":{"sizeInGiB":20},"availabilityZone":"us-east-1a","group":"service:web","startedBy":"ecs-svc/3",
"connectivity":"CONNECTED","enableExecuteCommand":true,
"createdAt":1763600000,"pullStartedAt":1763600001,"startedAt":1763600010,
"attachments":[{"type":"ElasticNetworkInterface","status":"ATTACHED","details":[{"name":"subnetId","value":"subnet-1"},
  {"name":"networkInterfaceId","value":"eni-1"},{"name":"privateIPv4Address","value":"10.0.0.5"}]},
  {"type":"Service Connect","details":[{"name":"subnetId","value":"wrong"}]}],
"containers":[{"name":"app","containerArn":"arn:c1","image":"nginx:alpine","imageDigest":"sha256:ab","runtimeId":"0123-1",
  "lastStatus":"RUNNING","healthStatus":"HEALTHY","cpu":"0","memory":"256","exitCode":0,
  "networkBindings":[{"containerPort":80,"hostPort":80,"protocol":"tcp","bindIP":"0.0.0.0"}],
  "networkInterfaces":[{"privateIpv4Address":"10.0.0.5"}],
  "managedAgents":[{"name":"ExecuteCommandAgent","lastStatus":"RUNNING","lastStartedAt":1763600020}]},
  {"name":"sidecar","lastStatus":"PENDING"}],
"tags":[{"key":"k","value":"v"}]}]}`)

	page, err := svc.ListTasksPage(context.Background(), ecsCluster, "", false, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"TAGS"}, strs(f.last(t, "DescribeTasks")["include"]))
	require.Len(t, page.Tasks, 2)
	tk, st := page.Tasks[0], page.Tasks[1]
	assert.Equal(t, "0123456789abcdef", tk.ID)
	assert.Equal(t, "fedcba9876543210", st.ID, "the short format has no cluster in the ARN")
	assert.Equal(t, ecsCluster, st.ClusterARN, "the cluster comes from clusterArn")
	assert.Equal(t, "web:7", TaskDefinitionLabel(tk.TaskDefinition))
	assert.Equal(t, "RUNNING", tk.LastStatus)
	assert.Equal(t, "HEALTHY", tk.Health)
	assert.Equal(t, "FARGATE", tk.CapacityProvider)
	assert.Equal(t, [2]string{"256", "512"}, [2]string{tk.CPU, tk.Memory})
	assert.Equal(t, int32(20), tk.EphemeralGiB)
	assert.Equal(t, "service:web", tk.Group)
	assert.Equal(t, "ecs-svc/3", tk.StartedBy)
	assert.True(t, tk.ExecEnabled)
	assert.Equal(t, int64(1763600001), tk.PullStartedAt.Unix())
	assert.Equal(t, int64(1763600010), tk.StartedAt.Unix())
	assert.True(t, tk.StoppedAt.IsZero(), "an empty time stays zero")
	assert.Equal(t, [3]string{"eni-1", "10.0.0.5", "subnet-1"}, [3]string{tk.ENI, tk.PrivateIP, tk.SubnetID},
		"only the ENI attachment, with the key names in any case")
	require.Len(t, tk.Containers, 2)
	c := tk.Containers[0]
	assert.Equal(t, "app", c.Name)
	assert.Equal(t, "0123-1", c.RuntimeID)
	require.NotNil(t, c.ExitCode)
	assert.Equal(t, int32(0), *c.ExitCode)
	assert.Equal(t, []PortBinding{{ContainerPort: 80, HostPort: 80, Protocol: "tcp", BindIP: "0.0.0.0"}}, c.Bindings)
	assert.Equal(t, []string{"10.0.0.5"}, c.PrivateIPs)
	require.Len(t, c.Agents, 1)
	assert.Equal(t, "ExecuteCommandAgent", c.Agents[0].Name)
	assert.Equal(t, "RUNNING", c.Agents[0].Status)
	assert.Nil(t, tk.Containers[1].ExitCode, "a container that did not exit has no exit code")
	assert.Equal(t, map[string]string{"k": "v"}, tk.Tags)

	assert.Equal(t, "EssentialContainerExited", st.StopCode)
	assert.Equal(t, "Essential container in task exited", st.StoppedReason)
	assert.Equal(t, [3]int64{1763600400, 1763600410, 1763600420},
		[3]int64{st.StoppingAt.Unix(), st.ExecutionStoppedAt.Unix(), st.StoppedAt.Unix()})
}

func TestDescribeTask(t *testing.T) {
	f, svc := newFakeECS(t)
	task := "arn:aws:ecs:us-east-1:111122223333:task/prod/abc"
	f.reply("DescribeTasks", `{"tasks":[{"taskArn":"`+task+`","lastStatus":"RUNNING"}]}`)
	got, err := svc.DescribeTask(context.Background(), ecsCluster, task)
	require.NoError(t, err)
	assert.Equal(t, "abc", got.ID)
	in := f.last(t, "DescribeTasks")
	assert.Equal(t, ecsCluster, in["cluster"])
	assert.Equal(t, []string{task}, strs(in["tasks"]))

	f.reply("DescribeTasks", `{"tasks":[],"failures":[{"arn":"`+task+`","reason":"MISSING"}]}`)
	_, err = svc.DescribeTask(context.Background(), ecsCluster, task)
	require.ErrorIs(t, err, ErrTaskNotFound)

	f.reply("DescribeTasks", `{"tasks":[],"failures":[{"arn":"`+task+`","reason":"ACCESS_DENIED"}]}`)
	_, err = svc.DescribeTask(context.Background(), ecsCluster, task)
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrTaskNotFound)
	assert.Contains(t, err.Error(), "ACCESS_DENIED")
}

func TestDescribeService(t *testing.T) {
	f, svc := newFakeECS(t)
	arn := "arn:aws:ecs:us-east-1:111122223333:service/prod/web"
	f.reply("DescribeServices", `{"services":[{"serviceArn":"`+arn+`","serviceName":"web","status":"ACTIVE","desiredCount":2,
		"tags":[{"key":"team","value":"orders"}]}]}`)
	got, err := svc.DescribeService(context.Background(), ecsCluster, "web")
	require.NoError(t, err)
	assert.Equal(t, "web", got.Name)
	assert.Equal(t, arn, got.ARN)
	assert.Equal(t, map[string]string{"team": "orders"}, got.Tags)
	in := f.last(t, "DescribeServices")
	assert.Equal(t, ecsCluster, in["cluster"])
	assert.Equal(t, []string{"web"}, strs(in["services"]))
	assert.Equal(t, []string{"TAGS"}, strs(in["include"]))

	f.reply("DescribeServices", `{"services":[],"failures":[{"arn":"`+arn+`","reason":"MISSING"}]}`)
	_, err = svc.DescribeService(context.Background(), ecsCluster, arn)
	require.ErrorIs(t, err, ErrServiceNotFound)
	assert.Contains(t, err.Error(), "web")

	f.reply("DescribeServices", `{"services":[],"failures":[{"arn":"`+arn+`","reason":"ACCESS_DENIED"}]}`)
	_, err = svc.DescribeService(context.Background(), ecsCluster, arn)
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrServiceNotFound)
	assert.Contains(t, err.Error(), "ACCESS_DENIED")
}

func TestTaskDefinitionLists(t *testing.T) {
	f, svc := newFakeECS(t)
	f.reply("ListTaskDefinitionFamilies", `{"families":["web","worker"],"nextToken":"f2"}`)
	fp, err := svc.ListTaskFamiliesPage(context.Background(), new("f1"))
	require.NoError(t, err)
	assert.Equal(t, []string{"web", "worker"}, fp.Families)
	assert.Equal(t, "f2", *fp.Next)
	in := f.last(t, "ListTaskDefinitionFamilies")
	assert.Equal(t, "ACTIVE", in["status"])
	assert.Equal(t, "f1", in["nextToken"])

	f.reply("ListTaskDefinitions", `{"taskDefinitionArns":["arn:aws:ecs:us-east-1:1:task-definition/web:12",
"arn:aws:ecs:us-east-1:1:task-definition/web:3"],"nextToken":"r2"}`)
	tp, err := svc.ListTaskDefinitionsPage(context.Background(), "web", new("r1"))
	require.NoError(t, err)
	assert.Equal(t, []TaskDefinitionRef{
		{ARN: "arn:aws:ecs:us-east-1:1:task-definition/web:12", Family: "web", Revision: 12},
		{ARN: "arn:aws:ecs:us-east-1:1:task-definition/web:3", Family: "web", Revision: 3},
	}, tp.Revisions)
	require.NotNil(t, tp.Next)
	assert.Equal(t, "r2", *tp.Next)
	in = f.last(t, "ListTaskDefinitions")
	assert.Equal(t, "r1", in["nextToken"])
	assert.Equal(t, "web", in["familyPrefix"])
	assert.Equal(t, "DESC", in["sort"], "newest first")
	assert.Equal(t, "ACTIVE", in["status"])
	assert.Empty(t, f.inputs("DescribeTaskDefinition"), "no describe call for each revision")

	f.reply("ListTaskDefinitions", `{"taskDefinitionArns":[],"nextToken":""}`)
	tp, err = svc.ListTaskDefinitionsPage(context.Background(), "web", nil)
	require.NoError(t, err)
	assert.Nil(t, tp.Next, "an empty token ends the walk")
}

func TestDescribeTaskDefinition(t *testing.T) {
	f, svc := newFakeECS(t)
	f.reply("DescribeTaskDefinition", `{"taskDefinition":{"taskDefinitionArn":"arn:aws:ecs:us-east-1:1:task-definition/web:7",
"family":"web","revision":7,"status":"ACTIVE","registeredAt":1763600000,"registeredBy":"arn:user","cpu":"256","memory":"512",
"networkMode":"awsvpc","compatibilities":["EC2","FARGATE"],"requiresCompatibilities":["FARGATE"],
"taskRoleArn":"arn:task","executionRoleArn":"arn:exec","runtimePlatform":{"cpuArchitecture":"X86_64","operatingSystemFamily":"LINUX"},
"ephemeralStorage":{"sizeInGiB":21},"volumes":[{"name":"data"}],
"containerDefinitions":[{"name":"app","image":"nginx:alpine","cpu":128,"memory":256,"memoryReservation":128,
  "command":["nginx","-g","daemon off;"],"portMappings":[{"name":"http","containerPort":80,"hostPort":80,"protocol":"tcp"},{"containerPortRange":"8000-8010","protocol":"udp"}],
  "environment":[{"name":"LOG_LEVEL","value":"debug"}],"environmentFiles":[{"value":"arn:s3:env","type":"s3"}],
  "secrets":[{"name":"API_KEY","valueFrom":"arn:aws:secretsmanager:us-east-1:1:secret:key"}],
  "logConfiguration":{"logDriver":"awslogs","options":{"awslogs-group":"/ecs/web-app","awslogs-region":"us-east-1","awslogs-stream-prefix":"ecs"}}},
 {"name":"router","image":"fluent-bit","essential":false,"logConfiguration":{"logDriver":"awsfirelens","options":{"Name":"cloudwatch","awslogs-group":"/not-awslogs","awslogs-stream-prefix":"x"}}}]},
"tags":[{"key":"team","value":"web"}]}`)

	d, err := svc.DescribeTaskDefinition(context.Background(), "web")
	require.NoError(t, err)
	in := f.last(t, "DescribeTaskDefinition")
	assert.Equal(t, "web", in["taskDefinition"])
	assert.Equal(t, []string{"TAGS"}, strs(in["include"]))
	assert.Equal(t, map[string]string{"team": "web"}, d.Tags, "the tags of the output")
	assert.Equal(t, int32(7), d.Revision)
	assert.Equal(t, "awsvpc", d.NetworkMode)
	assert.Equal(t, [2]string{"X86_64", "LINUX"}, [2]string{d.CPUArchitecture, d.OSFamily})
	assert.Equal(t, []string{"data"}, d.Volumes)
	assert.Equal(t, [2]string{"arn:task", "arn:exec"}, [2]string{d.TaskRole, d.ExecutionRole})
	require.Len(t, d.Containers, 2)

	app := d.Container("app")
	require.NotNil(t, app)
	assert.True(t, app.Essential, "a container is essential unless it says otherwise")
	assert.Equal(t, [3]int32{128, 256, 128}, [3]int32{app.CPU, app.Memory, app.MemoryReservation})
	assert.Equal(t, []PortMapping{{Name: "http", ContainerPort: 80, HostPort: 80, Protocol: "tcp"},
		{ContainerPortRange: "8000-8010", Protocol: "udp"}}, app.Ports)
	assert.Equal(t, map[string]string{"LOG_LEVEL": "debug"}, app.Env)
	assert.Equal(t, []string{"arn:s3:env"}, app.EnvFiles)
	assert.Equal(t, []SecretRef{{Name: "API_KEY", From: "arn:aws:secretsmanager:us-east-1:1:secret:key"}}, app.Secrets)
	assert.Equal(t, "/ecs/web-app", app.Log.Group())
	assert.Equal(t, "us-east-1", app.Log.Region())
	assert.Equal(t, "ecs/app/abc", app.Log.Stream("app", "abc"))

	router := d.Container("router")
	require.NotNil(t, router)
	assert.False(t, router.Essential)
	assert.Equal(t, "awsfirelens", router.Log.Driver)
	assert.Empty(t, router.Log.Group(), "only awslogs has a group")
	assert.Empty(t, router.Log.Stream("router", "abc"))
	assert.Nil(t, d.Container("missing"))

	var none *LogConfig
	assert.Empty(t, none.Stream("app", "abc"), "no log configuration")
	noPrefix := &LogConfig{Driver: "awslogs", Options: map[string]string{"awslogs-group": "/g"}}
	assert.Equal(t, "/g", noPrefix.Group())
	assert.Empty(t, noPrefix.Stream("app", "abc"), "without a prefix the stream is the Docker container ID")
}

func TestDescribeServiceDeployment(t *testing.T) {
	f, svc := newFakeECS(t)
	arn := "arn:aws:ecs:us-east-1:1:service-deployment/prod/web/d1"
	f.reply("DescribeServiceDeployments", `{"serviceDeployments":[{"serviceDeploymentArn":"`+arn+`","status":"ROLLBACK_SUCCESSFUL",
"statusReason":"circuit breaker","lifecycleStage":"CLEAN_UP","createdAt":1763600000,"startedAt":1763600001,
"finishedAt":1763600300,"updatedAt":1763600300,
"targetServiceRevision":{"arn":"arn:rev2","requestedTaskCount":3,"runningTaskCount":0,"pendingTaskCount":0},
"rollback":{"reason":"tasks failed","startedAt":1763600200,"serviceRevisionArn":"arn:rev1"},
"deploymentCircuitBreaker":{"status":"TRIGGERED","failureCount":10,"threshold":10},
"alarms":{"status":"MONITORING","triggeredAlarmNames":["5xx"]}}],"failures":[]}`)

	d, err := svc.DescribeServiceDeployment(context.Background(), arn)
	require.NoError(t, err)
	assert.Equal(t, []string{arn}, strs(f.last(t, "DescribeServiceDeployments")["serviceDeploymentArns"]))
	assert.Equal(t, "ROLLBACK_SUCCESSFUL", d.Status)
	assert.Equal(t, "circuit breaker", d.StatusReason)
	assert.Equal(t, "CLEAN_UP", d.LifecycleStage)
	assert.Equal(t, "arn:rev2", d.TargetRevision)
	assert.Equal(t, int32(3), d.TargetRequested)
	assert.Equal(t, "tasks failed", d.RollbackReason)
	assert.Equal(t, int64(1763600200), d.RollbackStarted.Unix())
	assert.Equal(t, [3]any{"TRIGGERED", int32(10), int32(10)}, [3]any{d.CircuitStatus, d.CircuitFailures, d.CircuitThreshold})
	assert.Equal(t, []string{"5xx"}, d.TriggeredAlarms)

	f.reply("DescribeServiceDeployments", `{"serviceDeployments":[],"failures":[{"arn":"`+arn+`","reason":"MISSING"}]}`)
	_, err = svc.DescribeServiceDeployment(context.Background(), arn)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "d1: MISSING")

	f.reply("DescribeServiceDeployments", `{"serviceDeployments":[],"failures":[{"arn":"`+arn+`","reason":"ACCESS_DENIED"}]}`)
	_, err = svc.DescribeServiceDeployment(context.Background(), arn)
	require.ErrorContains(t, err, "d1: ACCESS_DENIED")
	f.reply("DescribeServiceDeployments", `{"serviceDeployments":[],"failures":[]}`)
	_, err = svc.DescribeServiceDeployment(context.Background(), arn)
	require.ErrorContains(t, err, "d1: MISSING", "no answer at all")
}

// An AWS error keeps the name of the call and the code.
func TestECSErrorsNameTheCall(t *testing.T) {
	f, svc := newFakeECS(t)
	f.replies["ListServices"] = func(map[string]any) (int, string) {
		return http.StatusBadRequest, `{"__type":"ClusterNotFoundException","message":"Cluster not found."}`
	}
	_, err := svc.ListServicesPage(context.Background(), ecsCluster, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ListServices")
	assert.Contains(t, err.Error(), "ClusterNotFoundException")

	a := serviceARN(1)
	f.reply("ListServices", `{"serviceArns":["`+a+`"]}`)
	f.replies["DescribeServices"] = func(map[string]any) (int, string) {
		return http.StatusBadRequest, `{"__type":"AccessDeniedException","message":"denied"}`
	}
	_, err = svc.ListServicesPage(context.Background(), ecsCluster, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DescribeServices")
}

func TestECSARNHelpers(t *testing.T) {
	for in, want := range map[string]string{
		"arn:aws:ecs:us-east-1:1:cluster/prod":          "prod",
		"arn:aws:ecs:us-east-1:1:service/prod/web":      "web",
		"arn:aws:ecs:us-east-1:1:service/web":           "web",
		"arn:aws:ecs:us-east-1:1:task/prod/0123abcd":    "0123abcd",
		"arn:aws:ecs:us-east-1:1:task/0123abcd":         "0123abcd",
		"web":                                           "web",
		"arn:aws:ecs:us-east-1:1:task-definition/web:3": "web:3",
	} {
		assert.Equal(t, want, ARNName(in), in)
	}
	for in, want := range map[string]struct {
		family string
		rev    int
	}{
		"arn:aws:ecs:us-east-1:1:task-definition/web-app_2:12": {"web-app_2", 12},
		"web:3":    {"web", 3},
		"web":      {"web", 0},
		"web:next": {"web:next", 0},
		"":         {"", 0},
	} {
		f, r := ParseTaskDefinition(in)
		assert.Equal(t, want.family, f, in)
		assert.Equal(t, want.rev, r, in)
	}
	assert.Equal(t, "web:12", TaskDefinitionLabel("arn:aws:ecs:us-east-1:1:task-definition/web:12"))
	assert.Equal(t, "web", TaskDefinitionLabel("web"))
	assert.Empty(t, TaskDefinitionLabel(""))
}

// A full page of 100 clusters or tasks is one describe call with every
// ARN, and the rows keep the order of the list. The tokens go in and come
// out.
func TestECSClustersAndTasksPageInOneBatchOfHundred(t *testing.T) {
	f, svc := newFakeECS(t)
	var clusters, tasks []string
	for i := range 100 {
		clusters = append(clusters, fmt.Sprintf("arn:aws:ecs:us-east-1:111122223333:cluster/c%03d", i))
		tasks = append(tasks, fmt.Sprintf("arn:aws:ecs:us-east-1:111122223333:task/prod/t%03d", i))
	}
	f.reply("ListClusters", `{"clusterArns":`+jsonList(clusters)+`,"nextToken":"c2"}`)
	f.replies["DescribeClusters"] = func(in map[string]any) (int, string) {
		var out []string
		for _, a := range slices.Backward(strs(in["clusters"])) {
			out = append(out, fmt.Sprintf(`{"clusterArn":%q,"clusterName":%q}`, a, ARNName(a)))
		}
		return http.StatusOK, `{"clusters":[` + strings.Join(out, ",") + `]}`
	}
	f.reply("ListTasks", `{"taskArns":`+jsonList(tasks)+`,"nextToken":"t2"}`)
	f.replies["DescribeTasks"] = func(in map[string]any) (int, string) {
		var out []string
		for _, a := range slices.Backward(strs(in["tasks"])) {
			out = append(out, fmt.Sprintf(`{"taskArn":%q}`, a))
		}
		return http.StatusOK, `{"tasks":[` + strings.Join(out, ",") + `]}`
	}

	cp, err := svc.ListClustersPage(context.Background(), new("c1"))
	require.NoError(t, err)
	assert.Equal(t, "c1", f.last(t, "ListClusters")["nextToken"])
	require.NotNil(t, cp.Next)
	assert.Equal(t, "c2", *cp.Next)
	require.Len(t, f.inputs("DescribeClusters"), 1)
	assert.Equal(t, clusters, strs(f.last(t, "DescribeClusters")["clusters"]))
	require.Len(t, cp.Clusters, 100)
	for i, c := range cp.Clusters {
		assert.Equal(t, clusters[i], c.ARN)
	}

	tp, err := svc.ListTasksPage(context.Background(), ecsCluster, "", false, nil)
	require.NoError(t, err)
	require.NotNil(t, tp.Next)
	assert.Equal(t, "t2", *tp.Next)
	require.Len(t, f.inputs("DescribeTasks"), 1)
	assert.Equal(t, tasks, strs(f.last(t, "DescribeTasks")["tasks"]))
	require.Len(t, tp.Tasks, 100)
	for i, tk := range tp.Tasks {
		assert.Equal(t, tasks[i], tk.ARN)
	}
}
