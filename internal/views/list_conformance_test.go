package views

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/aws/awstest"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/nav"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// The conformance suite: every list view built on listCore must pass it.
// Each port PR adds the case of its view.

type conformanceCase struct {
	name string
	// build returns the view with a mock service whose list call accepts
	// only a context for which ok is true, and returns one item.
	build func(t *testing.T, ok func(context.Context) bool) nav.View
	// buildNil returns the view with a nil service.
	buildNil func() nav.View
	// coreMsgs returns the private core messages of the view's item type,
	// built for its current load generation. Call it only after every
	// earlier message is delivered, because a reload raises the generation.
	// A replyStep in the list asks for a prompt when its turn comes.
	coreMsgs func(v nav.View) []tea.Msg
	// itemIDs returns the IDs of the items the view holds now.
	itemIDs func(v nav.View) []string
	// wantIDs are the items the view holds after the coreMsgs are delivered.
	// They prove that the current-generation pages ran, not only the drop path.
	wantIDs []string
	// rwKeys are the keys that must be marked ModeReadWrite.
	rwKeys []string
	// rowKeys are the keys of the view that act on rows. Like the core row
	// keys, they must do nothing on the error screen.
	rowKeys []string
	// file and typeName name the source of the view, for the shadowing check.
	file, typeName string
	// coreType is the listCore instance the view embeds.
	coreType reflect.Type
	// allowMethods are core method names that the view may declare on
	// purpose (for example its own CapturingInput for a side input).
	allowMethods []string
}

var conformanceCases = []conformanceCase{
	{
		name: "sg_list",
		build: func(t *testing.T, ok func(context.Context) bool) nav.View {
			t.Helper()
			svc := new(awstest.MockEC2Service)
			svc.Test(t)
			svc.On("ListSecurityGroups", mock.MatchedBy(ok)).Return([]aws.SecurityGroup{{ID: "sg-1", Name: "one"}}, nil)
			// A call with a wrong context fails as a load error, not a mock panic.
			svc.On("ListSecurityGroups", mock.Anything).Return([]aws.SecurityGroup(nil), errors.New("wrong context"))
			return NewSGList(svc)
		},
		buildNil: func() nav.View { return NewSGList(nil) },
		coreMsgs: func(v nav.View) []tea.Msg {
			c := &v.(*SGList).listCore
			return []tea.Msg{
				listPageMsg[aws.SecurityGroup]{gen: c.loadGen, items: []aws.SecurityGroup{{ID: "sg-1"}}},
				listPageMsg[aws.SecurityGroup]{gen: c.loadGen - 1},
				listFailedMsg{gen: c.loadGen - 1, err: errors.New("old")},
				msg.ErrorMsg{Err: &loadError{err: errors.New("echo")}},
			}
		},
		itemIDs: func(v nav.View) []string {
			var ids []string
			for _, it := range v.(*SGList).items {
				ids = append(ids, it.ID)
			}
			return ids
		},
		wantIDs:  []string{"sg-1"},
		file:     "sg_list.go",
		typeName: "SGList",
		coreType: reflect.TypeFor[listCore[aws.SecurityGroup]](),
	},
	{
		name: "vpc_list",
		build: func(t *testing.T, ok func(context.Context) bool) nav.View {
			t.Helper()
			svc := new(awstest.MockEC2Service)
			svc.Test(t)
			svc.On("ListVPCsPage", mock.MatchedBy(ok), mock.Anything).Return(&aws.VPCPage{VPCs: []aws.VPC{{ID: "vpc-1", Name: "one"}}}, nil)
			svc.On("ListVPCsPage", mock.Anything, mock.Anything).Return(nil, errors.New("wrong context"))
			return NewVPCList(svc)
		},
		buildNil: func() nav.View { return NewVPCList(nil) },
		coreMsgs: func(v nav.View) []tea.Msg {
			c := &v.(*VPCList).listCore
			tok := "t2"
			return []tea.Msg{
				listPageMsg[aws.VPC]{gen: c.loadGen, items: []aws.VPC{{ID: "vpc-1"}}, next: &tok, page: 1},
				listPageMsg[aws.VPC]{gen: c.loadGen, items: []aws.VPC{{ID: "vpc-2"}}, page: 2},
				listPageMsg[aws.VPC]{gen: c.loadGen - 1, page: 3},
				listFailedMsg{gen: c.loadGen - 1, err: errors.New("old")},
				msg.ErrorMsg{Err: &loadError{err: errors.New("echo")}},
				keyPress('n'),
			}
		},
		itemIDs: func(v nav.View) []string {
			var ids []string
			for _, it := range v.(*VPCList).items {
				ids = append(ids, it.ID)
			}
			return ids
		},
		wantIDs:  []string{"vpc-1", "vpc-2"},
		rowKeys:  []string{"n"},
		file:     "vpc_list.go",
		typeName: "VPCList",
		coreType: reflect.TypeFor[listCore[aws.VPC]](),
	},
	{
		name: "subnet_list",
		build: func(t *testing.T, ok func(context.Context) bool) nav.View {
			t.Helper()
			svc := new(awstest.MockEC2Service)
			svc.Test(t)
			svc.On("ListSubnetsPage", mock.MatchedBy(ok), mock.Anything, "vpc-1").Return(&aws.SubnetPage{Subnets: []aws.Subnet{{ID: "subnet-1", Name: "one"}}}, nil)
			svc.On("ListSubnetsPage", mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("wrong context or VPC"))
			return NewSubnetList(svc, "vpc-1")
		},
		buildNil: func() nav.View { return NewSubnetList(nil, "vpc-1") },
		coreMsgs: func(v nav.View) []tea.Msg {
			c := &v.(*SubnetList).listCore
			tok := "t2"
			return []tea.Msg{
				listPageMsg[aws.Subnet]{gen: c.loadGen, items: []aws.Subnet{{ID: "subnet-1"}}, next: &tok, page: 1},
				listPageMsg[aws.Subnet]{gen: c.loadGen, items: []aws.Subnet{{ID: "subnet-2"}}, page: 2},
				listPageMsg[aws.Subnet]{gen: c.loadGen - 1, page: 3},
				listFailedMsg{gen: c.loadGen - 1, err: errors.New("old")},
				msg.ErrorMsg{Err: &loadError{err: errors.New("echo")}},
			}
		},
		itemIDs: func(v nav.View) []string {
			var ids []string
			for _, it := range v.(*SubnetList).items {
				ids = append(ids, it.ID)
			}
			return ids
		},
		wantIDs:  []string{"subnet-1", "subnet-2"},
		file:     "subnet_list.go",
		typeName: "SubnetList",
		coreType: reflect.TypeFor[listCore[aws.Subnet]](),
	},
	{
		name: "ami_list",
		build: func(t *testing.T, ok func(context.Context) bool) nav.View {
			t.Helper()
			svc := new(awstest.MockEC2Service)
			svc.Test(t)
			svc.On("ListOwnedAMIs", mock.MatchedBy(ok)).Return([]aws.AMI{{ID: "ami-1", Name: "one"}}, nil)
			svc.On("ListOwnedAMIs", mock.Anything).Return([]aws.AMI(nil), errors.New("wrong context"))
			return NewAMIList(svc)
		},
		buildNil: func() nav.View { return NewAMIList(nil) },
		coreMsgs: func(v nav.View) []tea.Msg {
			c := &v.(*AMIList).listCore
			return []tea.Msg{
				listPageMsg[aws.AMI]{gen: c.loadGen, items: []aws.AMI{{ID: "ami-1"}}},
				listPageMsg[aws.AMI]{gen: c.loadGen - 1, items: []aws.AMI{{ID: "ami-old"}}},
				listRefreshedMsg[aws.AMI]{gen: c.loadGen - 1, items: []aws.AMI{{ID: "ami-1", Name: "stale"}}},
				listFailedMsg{gen: c.loadGen - 1, err: errors.New("old")},
				msg.ErrorMsg{Err: &loadError{err: errors.New("echo")}},
				amiFocusLoadedMsg{id: "ami-x", err: context.Canceled},
				keyPress('p'), keyPress('q'), tea.KeyPressMsg{Code: tea.KeyEscape},
			}
		},
		itemIDs: func(v nav.View) []string {
			var ids []string
			for _, it := range v.(*AMIList).items {
				ids = append(ids, it.ID)
			}
			return ids
		},
		wantIDs:      []string{"ami-1"},
		file:         "ami_list.go",
		typeName:     "AMIList",
		coreType:     reflect.TypeFor[listCore[aws.AMI]](),
		allowMethods: []string{"CapturingInput"}, // the filter or the search input
	},
	{
		name: "ec2_list",
		build: func(t *testing.T, ok func(context.Context) bool) nav.View {
			t.Helper()
			svc := new(awstest.MockEC2Service)
			svc.Test(t)
			svc.On("ListInstancesPage", mock.MatchedBy(ok), mock.Anything).Return(&aws.InstancePage{Instances: []aws.Instance{{ID: "i-1", Name: "one", State: "running"}}}, nil)
			svc.On("ListInstancesPage", mock.Anything, mock.Anything).Return(nil, errors.New("wrong context"))
			return NewEC2List(svc, nil)
		},
		buildNil: func() nav.View { return NewEC2List(nil, nil) },
		coreMsgs: func(v nav.View) []tea.Msg {
			c := &v.(*EC2List).listCore
			tok := "t2"
			return []tea.Msg{
				listPageMsg[aws.Instance]{gen: c.loadGen, items: []aws.Instance{{ID: "i-1", State: "running"}}, next: &tok, page: 1},
				listPageMsg[aws.Instance]{gen: c.loadGen, items: []aws.Instance{{ID: "i-2", State: "running"}}, page: 2},
				listPageMsg[aws.Instance]{gen: c.loadGen - 1, items: []aws.Instance{{ID: "i-old"}}, page: 3},
				listRefreshedMsg[aws.Instance]{gen: c.loadGen - 1, items: []aws.Instance{{ID: "i-1", Name: "stale"}}},
				listFailedMsg{gen: c.loadGen - 1, err: errors.New("old")},
				msg.ErrorMsg{Err: &loadError{err: errors.New("echo")}},
				ec2InstanceDetailMsg{err: context.Canceled},
				ec2SSMSessionFinishedMsg{instanceID: "i-1", err: errors.New("x")},
				unfiltered(declineActionStep('m', "Stop")),
				unfiltered(cancelActionStep('m')),
				keyPress('o'),
				keyPress('m'),
			}
		},
		itemIDs: func(v nav.View) []string {
			var ids []string
			for _, it := range v.(*EC2List).items {
				ids = append(ids, it.ID)
			}
			return ids
		},
		wantIDs:  []string{"i-1", "i-2"},
		rwKeys:   []string{"o", "m"},
		rowKeys:  []string{"o", "m"},
		file:     "ec2_list.go",
		typeName: "EC2List",
		coreType: reflect.TypeFor[listCore[aws.Instance]](),
	},
	{
		name: "sqs_queues",
		build: func(t *testing.T, ok func(context.Context) bool) nav.View {
			t.Helper()
			svc := new(awstest.MockSQSService)
			svc.Test(t)
			svc.On("ListQueuesPage", mock.MatchedBy(ok), mock.Anything).Return(&aws.QueuePage{QueueURLs: []string{"u1"}}, nil)
			svc.On("ListQueuesPage", mock.Anything, mock.Anything).Return(nil, errors.New("wrong context"))
			svc.On("FetchAllQueueAttributes", mock.MatchedBy(ok), mock.Anything).Return([]aws.Queue{{URL: "u1", Name: "one"}}, nil)
			svc.On("FetchAllQueueAttributes", mock.Anything, mock.Anything).Return(nil, errors.New("wrong context"))
			return NewSQSQueues(svc)
		},
		buildNil: func() nav.View { return NewSQSQueues(nil) },
		coreMsgs: func(v nav.View) []tea.Msg {
			c := &v.(*SQSQueues).listCore
			tok := "t2"
			return []tea.Msg{
				listPageMsg[aws.Queue]{gen: c.loadGen, items: []aws.Queue{{URL: "u1"}}, next: &tok, page: 1},
				listPageMsg[aws.Queue]{gen: c.loadGen, items: []aws.Queue{{URL: "u2"}}, page: 2},
				listCompleteMsg[aws.Queue]{gen: c.loadGen, items: []aws.Queue{{URL: "u1", Name: "one"}, {URL: "u2", Name: "two"}}},
				listCompleteMsg[aws.Queue]{gen: c.loadGen - 1, items: []aws.Queue{{URL: "u-old"}}},
				listRefreshedMsg[aws.Queue]{gen: c.loadGen - 1, items: []aws.Queue{{URL: "u1", Name: "stale"}}},
				listFailedMsg{gen: c.loadGen - 1, err: errors.New("old")},
				msg.ErrorMsg{Err: &loadError{err: errors.New("echo")}},
				sqsQueueDetailMsg{err: context.Canceled},
				unfiltered(declineActionStep('m', "Purge Queue")),
				unfiltered(cancelActionStep('m')),
				msg.FormResultMsg{ID: "sqs_send_message", Aborted: true},
				keyPress('p'),
				keyPress('m'),
			}
		},
		itemIDs: func(v nav.View) []string {
			var ids []string
			for _, it := range v.(*SQSQueues).items {
				ids = append(ids, it.URL)
			}
			return ids
		},
		wantIDs:  []string{"u1", "u2"},
		rwKeys:   []string{"m"},
		rowKeys:  []string{"p", "m"},
		file:     "sqs_queues.go",
		typeName: "SQSQueues",
		coreType: reflect.TypeFor[listCore[aws.Queue]](),
	},
	{
		name: "s3_list",
		build: func(t *testing.T, ok func(context.Context) bool) nav.View {
			t.Helper()
			svc := new(awstest.MockS3Service)
			svc.Test(t)
			svc.On("ListBuckets", mock.MatchedBy(ok)).Return([]aws.Bucket{{Name: "b-1"}}, nil)
			svc.On("ListBuckets", mock.Anything).Return(nil, errors.New("wrong context"))
			// enter browses and d asks for properties; the conformance run
			// presses both.
			svc.On("GetBucketProperties", mock.Anything, mock.Anything).Return(&aws.BucketProperties{Name: "b-1"}, nil).Maybe()
			return NewS3List(svc, "us-east-1")
		},
		buildNil: func() nav.View { return NewS3List(nil, "us-east-1") },
		coreMsgs: func(v nav.View) []tea.Msg {
			c := &v.(*S3List).listCore
			return []tea.Msg{
				listPageMsg[aws.Bucket]{gen: c.loadGen, items: []aws.Bucket{{Name: "b-1"}, {Name: "b-2"}}},
				listPageMsg[aws.Bucket]{gen: c.loadGen - 1, items: []aws.Bucket{{Name: "b-old"}}},
				listFailedMsg{gen: c.loadGen - 1, err: errors.New("old")},
				msg.ErrorMsg{Err: &loadError{err: errors.New("echo")}},
				s3BucketPropsMsg{err: context.Canceled},
				s3BucketCreatedMsg{name: "b-3", err: errors.New("x")},
				unfiltered(declineKeyStep('x')),
				ui.FilterChangedMsg{Text: "zz", Source: s3CreateSource},
				keyPress('n'), tea.KeyPressMsg{Code: tea.KeyEscape},
				keyPress('x'),
			}
		},
		itemIDs: func(v nav.View) []string {
			var ids []string
			for _, it := range v.(*S3List).items {
				ids = append(ids, it.Name)
			}
			return ids
		},
		wantIDs:      []string{"b-1", "b-2"},
		rwKeys:       []string{"n", "x"},
		rowKeys:      []string{"x"}, // n opens the create input, which needs no row
		file:         "s3_list.go",
		typeName:     "S3List",
		coreType:     reflect.TypeFor[listCore[aws.Bucket]](),
		allowMethods: []string{"CapturingInput"}, // the filter or the create input
	},
	{
		name: "logs_groups",
		build: func(t *testing.T, ok func(context.Context) bool) nav.View {
			t.Helper()
			svc := new(awstest.MockLogsService)
			svc.Test(t)
			svc.On("ListLogGroupsPage", mock.MatchedBy(ok), mock.Anything).Return(&aws.LogGroupPage{Groups: []aws.LogGroup{{Name: "/g/1"}}}, nil)
			svc.On("ListLogGroupsPage", mock.Anything, mock.Anything).Return(nil, errors.New("wrong context"))
			// d fetches the detail with the tags.
			svc.On("ListLogGroupTags", mock.Anything, mock.Anything).Return(map[string]string{}, nil).Maybe()
			return NewLogGroups(svc)
		},
		buildNil: func() nav.View { return NewLogGroups(nil) },
		coreMsgs: func(v nav.View) []tea.Msg {
			c := &v.(*LogGroups).listCore
			tok := "t2"
			return []tea.Msg{
				listPageMsg[aws.LogGroup]{gen: c.loadGen, items: []aws.LogGroup{{Name: "/g/1"}}, next: &tok, page: 1},
				listPageMsg[aws.LogGroup]{gen: c.loadGen, items: []aws.LogGroup{{Name: "/g/2"}}, page: 2},
				listPageMsg[aws.LogGroup]{gen: c.loadGen - 1, items: []aws.LogGroup{{Name: "/g/old"}}, page: 3},
				listFailedMsg{gen: c.loadGen - 1, err: errors.New("old")},
				msg.ErrorMsg{Err: &loadError{err: errors.New("echo")}},
				listDetailMsg{err: context.Canceled},
				keyPress('v'),
				keyPress('l'),
			}
		},
		itemIDs: func(v nav.View) []string {
			var ids []string
			for _, it := range v.(*LogGroups).items {
				ids = append(ids, it.Name)
			}
			return ids
		},
		wantIDs:  []string{"/g/1", "/g/2"},
		rowKeys:  []string{"v"},
		file:     "logs_groups.go",
		typeName: "LogGroups",
		coreType: reflect.TypeFor[listCore[aws.LogGroup]](),
	},
	{
		name: "lambda_functions",
		build: func(t *testing.T, ok func(context.Context) bool) nav.View {
			t.Helper()
			svc := new(awstest.MockLambdaService)
			svc.Test(t)
			fn := aws.Function{Name: "fn-1", ARN: "arn:fn-1"}
			svc.On("ListFunctionsPage", mock.MatchedBy(ok), mock.Anything).Return(&aws.FunctionPage{Functions: []aws.Function{fn}}, nil)
			svc.On("ListFunctionsPage", mock.Anything, mock.Anything).Return(nil, errors.New("wrong context"))
			// d and $ fetch the detail.
			svc.On("GetFunction", mock.Anything, mock.Anything, mock.Anything).Return(&aws.FunctionDetail{Function: fn}, nil).Maybe()
			svc.On("GetFunctionURL", mock.Anything, mock.Anything).Return(nil, nil).Maybe()
			return NewLambdaFunctions(svc)
		},
		buildNil: func() nav.View { return NewLambdaFunctions(nil) },
		coreMsgs: func(v nav.View) []tea.Msg {
			c := &v.(*LambdaFunctions).listCore
			tok := "t2"
			return []tea.Msg{
				listPageMsg[aws.Function]{gen: c.loadGen, items: []aws.Function{{Name: "fn-1", ARN: "arn:fn-1"}}, next: &tok, page: 1},
				listPageMsg[aws.Function]{gen: c.loadGen, items: []aws.Function{{Name: "fn-2", ARN: "arn:fn-2"}}, page: 2},
				listPageMsg[aws.Function]{gen: c.loadGen - 1, items: []aws.Function{{Name: "old", ARN: "arn:old"}}, page: 3},
				listFailedMsg{gen: c.loadGen - 1, err: errors.New("old")},
				msg.ErrorMsg{Err: &loadError{err: errors.New("echo")}},
				listDetailMsg{err: context.Canceled},
				keyPress('$'),
				keyPress('l'),
			}
		},
		itemIDs: func(v nav.View) []string {
			var ids []string
			for _, it := range v.(*LambdaFunctions).items {
				ids = append(ids, it.ARN)
			}
			return ids
		},
		wantIDs:  []string{"arn:fn-1", "arn:fn-2"},
		rowKeys:  []string{"$"},
		file:     "lambda_functions.go",
		typeName: "LambdaFunctions",
		coreType: reflect.TypeFor[listCore[aws.Function]](),
	},
	{
		name: "lambda_aliases",
		build: func(t *testing.T, ok func(context.Context) bool) nav.View {
			t.Helper()
			svc := new(awstest.MockLambdaService)
			svc.Test(t)
			svc.On("ListAliasesPage", mock.MatchedBy(ok), "fn", mock.Anything).Return(&aws.AliasPage{Aliases: []aws.Alias{{Name: "a1"}}}, nil)
			svc.On("ListAliasesPage", mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("wrong context"))
			return NewLambdaAliases(svc, "fn", "/aws/lambda/fn")
		},
		buildNil: func() nav.View { return NewLambdaAliases(nil, "fn", "/aws/lambda/fn") },
		coreMsgs: func(v nav.View) []tea.Msg {
			c := &v.(*LambdaAliases).listCore
			tok := "t2"
			return []tea.Msg{
				listPageMsg[aws.Alias]{gen: c.loadGen, items: []aws.Alias{{Name: "a1"}}, next: &tok, page: 1},
				listPageMsg[aws.Alias]{gen: c.loadGen, items: []aws.Alias{{Name: "a2"}}, page: 2},
				listPageMsg[aws.Alias]{gen: c.loadGen - 1, items: []aws.Alias{{Name: "old"}}, page: 3},
				listFailedMsg{gen: c.loadGen - 1, err: errors.New("old")},
				msg.ErrorMsg{Err: &loadError{err: errors.New("echo")}},
				listDetailMsg{err: context.Canceled},
				keyPress('l'),
			}
		},
		itemIDs: func(v nav.View) []string {
			var ids []string
			for _, it := range v.(*LambdaAliases).items {
				ids = append(ids, it.Name)
			}
			return ids
		},
		wantIDs:  []string{"a1", "a2"},
		file:     "lambda_children.go",
		typeName: "LambdaAliases",
		coreType: reflect.TypeFor[listCore[aws.Alias]](),
	},
	{
		name: "lambda_versions",
		build: func(t *testing.T, ok func(context.Context) bool) nav.View {
			t.Helper()
			svc := new(awstest.MockLambdaService)
			svc.Test(t)
			svc.On("ListVersionsPage", mock.MatchedBy(ok), "fn", mock.Anything).Return(&aws.FunctionPage{Functions: []aws.Function{{Name: "fn", Version: "1"}}}, nil)
			svc.On("ListVersionsPage", mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("wrong context"))
			svc.On("GetFunction", mock.Anything, mock.Anything, mock.Anything).Return(&aws.FunctionDetail{}, nil).Maybe()
			return NewLambdaVersions(svc, "fn", "/aws/lambda/fn")
		},
		buildNil: func() nav.View { return NewLambdaVersions(nil, "fn", "/aws/lambda/fn") },
		coreMsgs: func(v nav.View) []tea.Msg {
			c := &v.(*LambdaVersions).listCore
			tok := "t2"
			return []tea.Msg{
				listPageMsg[aws.Function]{gen: c.loadGen, items: []aws.Function{{Name: "fn", Version: "1"}}, next: &tok, page: 1},
				listPageMsg[aws.Function]{gen: c.loadGen, items: []aws.Function{{Name: "fn", Version: "2"}}, page: 2},
				listPageMsg[aws.Function]{gen: c.loadGen - 1, items: []aws.Function{{Name: "fn", Version: "9"}}, page: 3},
				listFailedMsg{gen: c.loadGen - 1, err: errors.New("old")},
				msg.ErrorMsg{Err: &loadError{err: errors.New("echo")}},
				listDetailMsg{err: context.Canceled},
				keyPress('l'),
			}
		},
		itemIDs: func(v nav.View) []string {
			var ids []string
			for _, it := range v.(*LambdaVersions).items {
				ids = append(ids, it.Version)
			}
			return ids
		},
		wantIDs:  []string{"1", "2"},
		file:     "lambda_children.go",
		typeName: "LambdaVersions",
		coreType: reflect.TypeFor[listCore[aws.Function]](),
	},
	{
		name: "lambda_triggers",
		build: func(t *testing.T, ok func(context.Context) bool) nav.View {
			t.Helper()
			svc := new(awstest.MockLambdaService)
			svc.Test(t)
			svc.On("ListTriggersPage", mock.MatchedBy(ok), "fn", mock.Anything).Return(&aws.TriggerPage{Triggers: []aws.Trigger{{UUID: "u1"}}}, nil)
			svc.On("ListTriggersPage", mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("wrong context"))
			return NewLambdaTriggers(svc, "fn")
		},
		buildNil: func() nav.View { return NewLambdaTriggers(nil, "fn") },
		coreMsgs: func(v nav.View) []tea.Msg {
			c := &v.(*LambdaTriggers).listCore
			tok := "t2"
			return []tea.Msg{
				listPageMsg[aws.Trigger]{gen: c.loadGen, items: []aws.Trigger{{UUID: "u1"}}, next: &tok, page: 1},
				listPageMsg[aws.Trigger]{gen: c.loadGen, items: []aws.Trigger{{UUID: "u2"}}, page: 2},
				listPageMsg[aws.Trigger]{gen: c.loadGen - 1, items: []aws.Trigger{{UUID: "old"}}, page: 3},
				listFailedMsg{gen: c.loadGen - 1, err: errors.New("old")},
				msg.ErrorMsg{Err: &loadError{err: errors.New("echo")}},
				listDetailMsg{err: context.Canceled},
				keyPress('l'),
			}
		},
		itemIDs: func(v nav.View) []string {
			var ids []string
			for _, it := range v.(*LambdaTriggers).items {
				ids = append(ids, it.UUID)
			}
			return ids
		},
		wantIDs:  []string{"u1", "u2"},
		file:     "lambda_children.go",
		typeName: "LambdaTriggers",
		coreType: reflect.TypeFor[listCore[aws.Trigger]](),
	},
	{
		name: "lambda_invocations",
		build: func(t *testing.T, ok func(context.Context) bool) nav.View {
			t.Helper()
			svc := new(awstest.MockLogsService)
			svc.Test(t)
			// The floor of the walk is now, so the load ends after one window.
			svc.On("GetLogGroup", mock.Anything, "/aws/lambda/fn").Return(&aws.LogGroup{Name: "/aws/lambda/fn",
				Created: time.Now().Add(logsBackfill + time.Minute)}, nil)
			ev := aws.LogEvent{ID: "e1", Message: "REPORT RequestId: r1\tDuration: 1 ms\t"}
			svc.On("FilterEvents", mock.MatchedBy(ok), mock.Anything).Return(&aws.EventPage{Events: []aws.LogEvent{ev}, End: true}, nil)
			svc.On("FilterEvents", mock.Anything, mock.Anything).Return(nil, errors.New("wrong context"))
			return NewLambdaInvocations(svc, map[string]string{"function": "fn", "log_group": "/aws/lambda/fn"})
		},
		buildNil: func() nav.View {
			return NewLambdaInvocations(nil, map[string]string{"function": "fn", "log_group": "/aws/lambda/fn"})
		},
		coreMsgs: func(v nav.View) []tea.Msg {
			c := &v.(*LambdaInvocations).listCore
			tok := "t2"
			return []tea.Msg{
				listPageMsg[lambdaInvocation]{gen: c.loadGen, items: []lambdaInvocation{{RequestID: "r1", Parsed: true}}, next: &tok, page: 1},
				listPageMsg[lambdaInvocation]{gen: c.loadGen, items: []lambdaInvocation{{RequestID: "r2", Parsed: true}}, page: 2},
				listPageMsg[lambdaInvocation]{gen: c.loadGen - 1, items: []lambdaInvocation{{RequestID: "old", Parsed: true}}, page: 3},
				listFailedMsg{gen: c.loadGen - 1, err: errors.New("old")},
				msg.ErrorMsg{Err: &loadError{err: errors.New("echo")}},
				listDetailMsg{err: context.Canceled},
				keyPress('l'),
			}
		},
		itemIDs: func(v nav.View) []string {
			var ids []string
			for _, it := range v.(*LambdaInvocations).items {
				ids = append(ids, it.RequestID)
			}
			return ids
		},
		wantIDs:  []string{"r1", "r2"},
		file:     "lambda_invocations.go",
		typeName: "LambdaInvocations",
		coreType: reflect.TypeFor[listCore[lambdaInvocation]](),
	},
	{
		name: "ecs_clusters",
		build: func(t *testing.T, ok func(context.Context) bool) nav.View {
			t.Helper()
			svc := new(awstest.MockECSService)
			svc.Test(t)
			svc.On("ListClustersPage", mock.MatchedBy(ok), mock.Anything).Return(&aws.ClusterPage{Clusters: []aws.Cluster{{Name: "c1", ARN: "arn:c1"}}}, nil)
			svc.On("ListClustersPage", mock.Anything, mock.Anything).Return(nil, errors.New("wrong context"))
			return NewECSClusters(svc)
		},
		buildNil: func() nav.View { return NewECSClusters(nil) },
		coreMsgs: func(v nav.View) []tea.Msg {
			c := &v.(*ECSClusters).listCore
			tok := "t2"
			return []tea.Msg{
				listPageMsg[aws.Cluster]{gen: c.loadGen, items: []aws.Cluster{{Name: "c1", ARN: "arn:c1"}}, next: &tok, page: 1},
				listPageMsg[aws.Cluster]{gen: c.loadGen, items: []aws.Cluster{{Name: "c2", ARN: "arn:c2"}}, page: 2},
				listPageMsg[aws.Cluster]{gen: c.loadGen - 1, items: []aws.Cluster{{Name: "old", ARN: "arn:old"}}, page: 3},
				listFailedMsg{gen: c.loadGen - 1, err: errors.New("old")},
				msg.ErrorMsg{Err: &loadError{err: errors.New("echo")}},
				listDetailMsg{err: context.Canceled},
			}
		},
		itemIDs: func(v nav.View) []string {
			var ids []string
			for _, it := range v.(*ECSClusters).items {
				ids = append(ids, it.ARN)
			}
			return ids
		},
		wantIDs:  []string{"arn:c1", "arn:c2"},
		rowKeys:  []string{"enter"},
		file:     "ecs_clusters.go",
		typeName: "ECSClusters",
		coreType: reflect.TypeFor[listCore[aws.Cluster]](),
	},
	{
		name: "ecs_services",
		build: func(t *testing.T, ok func(context.Context) bool) nav.View {
			t.Helper()
			svc := new(awstest.MockECSService)
			svc.Test(t)
			svc.On("ListServicesPage", mock.MatchedBy(ok), "arn:c", mock.Anything).Return(&aws.ServicePage{Services: []aws.ClusterService{{Name: "s1", ARN: "arn:s1"}}}, nil)
			svc.On("ListServicesPage", mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("wrong context"))
			return NewECSServices(svc, "arn:c")
		},
		buildNil: func() nav.View { return NewECSServices(nil, "arn:c") },
		coreMsgs: func(v nav.View) []tea.Msg {
			c := &v.(*ECSServices).listCore
			tok := "t2"
			return []tea.Msg{
				listPageMsg[aws.ClusterService]{gen: c.loadGen, items: []aws.ClusterService{{Name: "s1", ARN: "arn:s1"}}, next: &tok, page: 1},
				listPageMsg[aws.ClusterService]{gen: c.loadGen, items: []aws.ClusterService{{Name: "s2", ARN: "arn:s2"}}, page: 2},
				listPageMsg[aws.ClusterService]{gen: c.loadGen - 1, items: []aws.ClusterService{{Name: "old", ARN: "arn:old"}}, page: 3},
				listFailedMsg{gen: c.loadGen - 1, err: errors.New("old")},
				msg.ErrorMsg{Err: &loadError{err: errors.New("echo")}},
				listDetailMsg{err: context.Canceled},
				keyPress('l'),
			}
		},
		itemIDs: func(v nav.View) []string {
			var ids []string
			for _, it := range v.(*ECSServices).items {
				ids = append(ids, it.ARN)
			}
			return ids
		},
		wantIDs:  []string{"arn:s1", "arn:s2"},
		rowKeys:  []string{"enter"},
		file:     "ecs_services.go",
		typeName: "ECSServices",
		coreType: reflect.TypeFor[listCore[aws.ClusterService]](),
	},
	{
		name: "ecs_tasks",
		build: func(t *testing.T, ok func(context.Context) bool) nav.View {
			t.Helper()
			svc := new(awstest.MockECSService)
			svc.Test(t)
			svc.On("ListTasksPage", mock.MatchedBy(ok), "arn:c", "web", false, mock.Anything).Return(&aws.TaskPage{Tasks: []aws.Task{{ID: "t1", ARN: "arn:t1"}}}, nil)
			// x loads the stopped tasks.
			svc.On("ListTasksPage", mock.MatchedBy(ok), "arn:c", "web", true, mock.Anything).Return(&aws.TaskPage{}, nil).Maybe()
			svc.On("ListTasksPage", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("wrong context"))
			return NewECSTasks(svc, "arn:c", "web")
		},
		buildNil: func() nav.View { return NewECSTasks(nil, "arn:c", "web") },
		coreMsgs: func(v nav.View) []tea.Msg {
			c := &v.(*ECSTasks).listCore
			tok := "t2"
			return []tea.Msg{
				listPageMsg[aws.Task]{gen: c.loadGen, items: []aws.Task{{ID: "t1", ARN: "arn:t1"}}, next: &tok, page: 1},
				listPageMsg[aws.Task]{gen: c.loadGen, items: []aws.Task{{ID: "t2", ARN: "arn:t2"}}, page: 2},
				listPageMsg[aws.Task]{gen: c.loadGen - 1, items: []aws.Task{{ID: "old", ARN: "arn:old"}}, page: 3},
				listFailedMsg{gen: c.loadGen - 1, err: errors.New("old")},
				msg.ErrorMsg{Err: &loadError{err: errors.New("echo")}},
				listDetailMsg{err: context.Canceled},
				keyPress('l'),
			}
		},
		itemIDs: func(v nav.View) []string {
			var ids []string
			for _, it := range v.(*ECSTasks).items {
				ids = append(ids, it.ARN)
			}
			return ids
		},
		wantIDs:  []string{"arn:t1", "arn:t2"},
		rowKeys:  []string{"enter"},
		file:     "ecs_tasks.go",
		typeName: "ECSTasks",
		coreType: reflect.TypeFor[listCore[aws.Task]](),
	},
	{
		name: "ecs_containers",
		build: func(t *testing.T, ok func(context.Context) bool) nav.View {
			t.Helper()
			svc := new(awstest.MockECSService)
			svc.Test(t)
			task := &aws.Task{ARN: "arn:t1", ID: "t1", TaskDefinition: "arn:td", Containers: []aws.Container{{Name: "c1", ARN: "arn:c1"}}}
			svc.On("DescribeTask", mock.MatchedBy(ok), "arn:c", "arn:t1").Return(task, nil)
			svc.On("DescribeTask", mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("wrong context"))
			svc.On("DescribeTaskDefinition", mock.MatchedBy(ok), "arn:td").Return(&aws.TaskDefinition{}, nil)
			svc.On("DescribeTaskDefinition", mock.Anything, mock.Anything).Return(nil, errors.New("wrong context"))
			return NewECSContainers(svc, "arn:c", "arn:t1", "us-east-1")
		},
		buildNil: func() nav.View { return NewECSContainers(nil, "arn:c", "arn:t1", "us-east-1") },
		coreMsgs: func(v nav.View) []tea.Msg {
			c := &v.(*ECSContainers).listCore
			tok := "t2"
			task := &aws.Task{ID: "t1"}
			return []tea.Msg{
				listPageMsg[ecsContainer]{gen: c.loadGen, items: []ecsContainer{{Container: aws.Container{ARN: "arn:c1"}, Task: task}}, next: &tok, page: 1},
				listPageMsg[ecsContainer]{gen: c.loadGen, items: []ecsContainer{{Container: aws.Container{ARN: "arn:c2"}, Task: task}}, page: 2},
				listPageMsg[ecsContainer]{gen: c.loadGen - 1, items: []ecsContainer{{Container: aws.Container{ARN: "arn:old"}, Task: task}}, page: 3},
				listFailedMsg{gen: c.loadGen - 1, err: errors.New("old")},
				msg.ErrorMsg{Err: &loadError{err: errors.New("echo")}},
				listDetailMsg{err: context.Canceled},
				keyPress('l'),
			}
		},
		itemIDs: func(v nav.View) []string {
			var ids []string
			for _, it := range v.(*ECSContainers).items {
				ids = append(ids, it.ARN)
			}
			return ids
		},
		wantIDs:  []string{"arn:c1", "arn:c2"},
		rowKeys:  []string{"enter"},
		file:     "ecs_containers.go",
		typeName: "ECSContainers",
		coreType: reflect.TypeFor[listCore[ecsContainer]](),
	},
	{
		name: "ecs_task_families",
		build: func(t *testing.T, ok func(context.Context) bool) nav.View {
			t.Helper()
			svc := new(awstest.MockECSService)
			svc.Test(t)
			svc.On("ListTaskFamiliesPage", mock.MatchedBy(ok), mock.Anything).Return(&aws.FamilyPage{Families: []string{"f1"}}, nil)
			svc.On("ListTaskFamiliesPage", mock.Anything, mock.Anything).Return(nil, errors.New("wrong context"))
			return NewECSTaskFamilies(svc)
		},
		buildNil: func() nav.View { return NewECSTaskFamilies(nil) },
		coreMsgs: func(v nav.View) []tea.Msg {
			c := &v.(*ECSTaskFamilies).listCore
			tok := "t2"
			return []tea.Msg{
				listPageMsg[string]{gen: c.loadGen, items: []string{"f1"}, next: &tok, page: 1},
				listPageMsg[string]{gen: c.loadGen, items: []string{"f2"}, page: 2},
				listPageMsg[string]{gen: c.loadGen - 1, items: []string{"old"}, page: 3},
				listFailedMsg{gen: c.loadGen - 1, err: errors.New("old")},
				msg.ErrorMsg{Err: &loadError{err: errors.New("echo")}},
				listDetailMsg{err: context.Canceled},
				keyPress('l'),
			}
		},
		itemIDs: func(v nav.View) []string {
			return append([]string(nil), v.(*ECSTaskFamilies).items...)
		},
		wantIDs:  []string{"f1", "f2"},
		rowKeys:  []string{"enter", "$"},
		file:     "ecs_taskdefs.go",
		typeName: "ECSTaskFamilies",
		coreType: reflect.TypeFor[listCore[string]](),
	},
	{
		name: "ecs_task_definitions",
		build: func(t *testing.T, ok func(context.Context) bool) nav.View {
			t.Helper()
			svc := new(awstest.MockECSService)
			svc.Test(t)
			svc.On("ListTaskDefinitionsPage", mock.MatchedBy(ok), "web", mock.Anything).Return(&aws.TaskDefinitionPage{Revisions: []aws.TaskDefinitionRef{{ARN: "arn:r1", Revision: 1}}}, nil)
			svc.On("ListTaskDefinitionsPage", mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("wrong context"))
			return NewECSTaskDefinitions(svc, "web")
		},
		buildNil: func() nav.View { return NewECSTaskDefinitions(nil, "web") },
		coreMsgs: func(v nav.View) []tea.Msg {
			c := &v.(*ECSTaskDefinitions).listCore
			tok := "t2"
			return []tea.Msg{
				listPageMsg[aws.TaskDefinitionRef]{gen: c.loadGen, items: []aws.TaskDefinitionRef{{ARN: "arn:r1"}}, next: &tok, page: 1},
				listPageMsg[aws.TaskDefinitionRef]{gen: c.loadGen, items: []aws.TaskDefinitionRef{{ARN: "arn:r2"}}, page: 2},
				listPageMsg[aws.TaskDefinitionRef]{gen: c.loadGen - 1, items: []aws.TaskDefinitionRef{{ARN: "arn:old"}}, page: 3},
				listFailedMsg{gen: c.loadGen - 1, err: errors.New("old")},
				msg.ErrorMsg{Err: &loadError{err: errors.New("echo")}},
				listDetailMsg{err: context.Canceled},
				keyPress('l'),
			}
		},
		itemIDs: func(v nav.View) []string {
			var ids []string
			for _, it := range v.(*ECSTaskDefinitions).items {
				ids = append(ids, it.ARN)
			}
			return ids
		},
		wantIDs:  []string{"arn:r1", "arn:r2"},
		rowKeys:  []string{"enter", "$"},
		file:     "ecs_taskdefs.go",
		typeName: "ECSTaskDefinitions",
		coreType: reflect.TypeFor[listCore[aws.TaskDefinitionRef]](),
	},
	{
		name: "logs_streams",
		build: func(t *testing.T, ok func(context.Context) bool) nav.View {
			t.Helper()
			svc := new(awstest.MockLogsService)
			svc.Test(t)
			svc.On("ListLogStreamsPage", mock.MatchedBy(ok), "g", mock.Anything).Return(&aws.LogStreamPage{Streams: []aws.LogStream{{Name: "s1"}}}, nil)
			svc.On("ListLogStreamsPage", mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("wrong context"))
			return NewLogStreams(svc, "g")
		},
		buildNil: func() nav.View { return NewLogStreams(nil, "g") },
		coreMsgs: func(v nav.View) []tea.Msg {
			c := &v.(*LogStreams).listCore
			tok := "t2"
			return []tea.Msg{
				listPageMsg[aws.LogStream]{gen: c.loadGen, items: []aws.LogStream{{Name: "s1"}}, next: &tok, page: 1},
				listPageMsg[aws.LogStream]{gen: c.loadGen, items: []aws.LogStream{{Name: "s2"}}, page: 2},
				listPageMsg[aws.LogStream]{gen: c.loadGen - 1, items: []aws.LogStream{{Name: "s-old"}}, page: 3},
				listFailedMsg{gen: c.loadGen - 1, err: errors.New("old")},
				msg.ErrorMsg{Err: &loadError{err: errors.New("echo")}},
				keyPress('l'),
			}
		},
		itemIDs: func(v nav.View) []string {
			var ids []string
			for _, it := range v.(*LogStreams).items {
				ids = append(ids, it.Name)
			}
			return ids
		},
		wantIDs:  []string{"s1", "s2"},
		file:     "logs_streams.go",
		typeName: "LogStreams",
		coreType: reflect.TypeFor[listCore[aws.LogStream]](),
	},
}

// replyStep stands for a prompt reply in a list of messages. The test runs
// it at its turn. It asks the view with real key presses and returns the
// reply to the real request.
type replyStep func(t *testing.T, v nav.View) tea.Msg

// press sends m to v, checks that Update returns v, and returns the command.
func press(t *testing.T, v nav.View, m tea.Msg) tea.Cmd {
	t.Helper()
	got, cmd := v.Update(m)
	require.Same(t, v, got, "%T", m)
	return cmd
}

// pickColumnAt asks for the sort picker and picks the column at index i.
func pickColumnAt(i int) replyStep {
	return func(t *testing.T, v nav.View) tea.Msg {
		t.Helper()
		req := sortRequest(t, v)
		require.Less(t, i, len(req.Columns))
		return pickColumn(t, req, req.Columns[i])
	}
}

// clearSortStep asks for the sort picker and clears the sort.
func clearSortStep(t *testing.T, v nav.View) tea.Msg {
	t.Helper()
	return clearSort(sortRequest(t, v))
}

// cancelActionStep presses key for the action picker and cancels it.
func cancelActionStep(key rune) replyStep {
	return func(t *testing.T, v nav.View) tea.Msg {
		t.Helper()
		return cancelPick(requestOf[msg.RequestActionPickerMsg](t, press(t, v, keyPress(key))))
	}
}

// declineActionStep presses key for the action picker, picks label, and
// answers no to the confirm that follows.
func declineActionStep(key rune, label string) replyStep {
	return func(t *testing.T, v nav.View) tea.Msg {
		t.Helper()
		picker := requestOf[msg.RequestActionPickerMsg](t, press(t, v, keyPress(key)))
		req := requestOf[msg.RequestConfirmMsg](t, press(t, v, pickLabel(t, picker, label)))
		return confirmReply(req, false)
	}
}

// declineKeyStep presses key for a confirm and answers no.
func declineKeyStep(key rune) replyStep {
	return func(t *testing.T, v nav.View) tea.Msg {
		t.Helper()
		return confirmReply(requestOf[msg.RequestConfirmMsg](t, press(t, v, keyPress(key))), false)
	}
}

// conformanceFilter is the filter text that the shared messages of
// TestListConformance_UpdateReturnsSamePointer leave on the list.
const conformanceFilter = "x"

// unfiltered runs step with no filter, so a key that acts on rows finds the
// cursor row. Then it puts conformanceFilter back.
func unfiltered(step replyStep) replyStep {
	return func(t *testing.T, v nav.View) tea.Msg {
		t.Helper()
		press(t, v, ui.FilterChangedMsg{Text: ""})
		reply := step(t, v)
		press(t, v, ui.FilterChangedMsg{Text: conformanceFilter})
		return reply
	}
}

// conformanceLoaded builds the view at width 160 and runs its first load.
func conformanceLoaded(t *testing.T, tc conformanceCase) nav.View {
	t.Helper()
	v := tc.build(t, func(context.Context) bool { return true })
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 20})
	pump(v, v.Init(), 5)
	return v
}

func TestListConformance_Interfaces(t *testing.T) {
	for _, tc := range conformanceCases {
		t.Run(tc.name, func(t *testing.T) {
			var v any = tc.buildNil()
			_, isView := v.(nav.View)
			_, isCapturer := v.(nav.InputCapturer)
			_, isSetter := v.(nav.ContextSetter)
			assert.True(t, isView)
			assert.True(t, isCapturer)
			assert.True(t, isSetter)
		})
	}
}

func TestListConformance_UpdateReturnsSamePointer(t *testing.T) {
	for _, tc := range conformanceCases {
		t.Run(tc.name, func(t *testing.T) {
			v := conformanceLoaded(t, tc)
			_, reload := v.Update(keyPress('r'))
			load := netLoadMsg(reload)
			msgs := []tea.Msg{
				spinner.TickMsg{},
				tea.WindowSizeMsg{Width: 60, Height: 20},
				tea.WindowSizeMsg{Width: 160, Height: 20},
				keyPress('s'), keyPress('S'), keyPress('y'), keyPress('d'), keyPress(' '), keyPress('z'),
				tea.KeyPressMsg{Code: tea.KeyDown}, tea.KeyPressMsg{Code: tea.KeyUp},
				tea.KeyPressMsg{Code: tea.KeyEnter},
				pickColumnAt(0),
				replyStep(clearSortStep),
				msg.PickerResultMsg{Selected: 0},
				msg.ConfirmResultMsg{Confirmed: true},
				ui.FilterChangedMsg{Text: conformanceFilter},
				ui.FilterChangedMsg{Text: "x", Source: "side"},
				msg.FocusResourceMsg{ResourceID: "missing"},
				keyPress('/'), keyPress('x'), tea.KeyPressMsg{Code: tea.KeyEnter},
				keyPress('/'), tea.KeyPressMsg{Code: tea.KeyEscape},
				load,
				msg.ErrorMsg{Err: errors.New("x")},
				keyPress('r'),
			}
			deliver := func(msgs []tea.Msg) {
				for _, m := range msgs {
					if step, ok := m.(replyStep); ok {
						m = step(t, v)
					}
					got, _ := v.Update(m)
					require.Same(t, v, got, "%T", m)
				}
			}
			deliver(msgs)
			// Build the core messages only now: the 'r' above raised the
			// load generation, and messages built earlier would be stale.
			deliver(tc.coreMsgs(v))
			assert.Equal(t, tc.wantIDs, tc.itemIDs(v), "the current-generation pages were applied")
			deliver([]tea.Msg{tea.KeyPressMsg{Code: tea.KeyEscape}, struct{}{}})
		})
	}
}

func TestListConformance_SetContextReachesFetch(t *testing.T) {
	type ctxKey struct{}
	for _, tc := range conformanceCases {
		t.Run(tc.name, func(t *testing.T) {
			// The fetch gets the navigator's context, with the load timeout
			// on top: every default load has one.
			v := tc.build(t, func(ctx context.Context) bool {
				_, hasDeadline := ctx.Deadline()
				return ctx.Value(ctxKey{}) == "nav" && hasDeadline
			})
			v.(nav.ContextSetter).SetContext(context.WithValue(t.Context(), ctxKey{}, "nav"))
			v.Update(tea.WindowSizeMsg{Width: 160, Height: 20})
			seen := pump(v, v.Init(), 5)
			_, failed := firstOfType[msg.ErrorMsg](seen)
			require.False(t, failed, "the fetch got the navigator's context")
			assert.Regexp(t, `^1/1 `, plain(v.Footer()))
		})
	}
}

// The app binds these letters globally, so a view must not.
var globalLetters = []string{"q", "P", "R", "T", "W", "E", "L", "?", ":"}

func TestListConformance_NoGlobalLetters(t *testing.T) {
	for _, tc := range conformanceCases {
		t.Run(tc.name, func(t *testing.T) {
			v := conformanceLoaded(t, tc)
			for _, h := range v.KeyMap() {
				for _, k := range h.Keys() {
					assert.NotContains(t, globalLetters, k, "KeyMap binds a global letter")
				}
			}
			for _, k := range globalLetters {
				_, cmd := v.Update(keyPress([]rune(k)[0]))
				assert.Empty(t, collectMsgs(cmd), "key %q does something", k)
			}
		})
	}
}

func TestListConformance_RWKeysMarked(t *testing.T) {
	for _, tc := range conformanceCases {
		t.Run(tc.name, func(t *testing.T) {
			for _, h := range tc.buildNil().KeyMap() {
				rw := slices.ContainsFunc(h.Keys(), func(k string) bool { return slices.Contains(tc.rwKeys, k) })
				if rw {
					assert.Equal(t, ui.ModeReadWrite, h.Mode, "%v", h.Keys())
				} else {
					assert.Equal(t, ui.ModeAny, h.Mode, "%v", h.Keys())
				}
			}
		})
	}
}

// After a failed reload the core keeps the old items, so a cursor row
// exists behind the error screen. No row key may act on it: the core keys
// (enter, d, y, space) and the row keys of the view. r with a selection
// reloads the whole list, which clears the error.
func TestListConformance_ErrorScreenBlocksRowKeys(t *testing.T) {
	for _, tc := range conformanceCases {
		t.Run(tc.name, func(t *testing.T) {
			v := conformanceLoaded(t, tc)
			v.Update(keyPress(' ')) // select the cursor row (MultiSelect lists)
			selected := plain(v.Footer())
			v.Update(msg.ErrorMsg{Err: errors.New("throttled")})
			require.Equal(t, "\n  Error: throttled", plain(v.View().Content))

			keys := []tea.KeyPressMsg{{Code: tea.KeyEnter}, keyPress('d'), keyPress('y'), keyPress(' ')}
			for _, k := range tc.rowKeys {
				keys = append(keys, keyPress([]rune(k)[0]))
			}
			for _, k := range keys {
				got, cmd := v.Update(k)
				require.Same(t, v, got)
				assert.Empty(t, collectMsgs(cmd), "key %q acts on a hidden row", k.String())
			}
			assert.Equal(t, selected, plain(v.Footer()), "space did not change the selection")

			v.Update(keyPress('r'))
			assert.NotContains(t, plain(v.Footer()), "Refreshing", "r is not a selective refresh on the error screen")
			assert.NotContains(t, plain(v.View().Content), "Error", "r starts a full reload, which clears the error")
		})
	}
}

// TestListConformance_FitsTheWindow checks that no line of a list is wider
// than the window, also when no column set fits and the columns shrink
// (L28). A wider line wraps, for example the header separator.
func TestListConformance_FitsTheWindow(t *testing.T) {
	for _, tc := range conformanceCases {
		t.Run(tc.name, func(t *testing.T) {
			v := conformanceLoaded(t, tc)
			for w := 30; w <= 200; w++ {
				v.Update(tea.WindowSizeMsg{Width: w, Height: 20})
				for line := range strings.SplitSeq(v.View().Content, "\n") {
					if got := ansi.StringWidth(line); got > w {
						t.Fatalf("width %d: a line is %d wide: %q", w, got, plain(line))
					}
				}
			}
		})
	}
}

// loadContexts are the msg.ErrorMsg contexts of the default loads. A nil
// service fails like any load, with the context of the load (PR 4 to 9).
var loadContexts = map[string]string{
	"sg_list":              "listing security groups",
	"vpc_list":             "listing VPCs",
	"subnet_list":          "listing subnets",
	"ami_list":             "listing owned AMIs",
	"ec2_list":             "listing EC2 instances",
	"sqs_queues":           "listing SQS queues",
	"s3_list":              "listing S3 buckets",
	"logs_groups":          "listing log groups",
	"lambda_functions":     "listing functions",
	"logs_streams":         "listing log streams",
	"lambda_aliases":       "listing aliases",
	"lambda_versions":      "listing versions",
	"lambda_triggers":      "listing triggers",
	"lambda_invocations":   "listing invocations",
	"ecs_clusters":         "listing clusters",
	"ecs_services":         "listing services",
	"ecs_tasks":            "listing tasks",
	"ecs_containers":       "listing containers",
	"ecs_task_families":    "listing task definition families",
	"ecs_task_definitions": "listing task definitions",
}

// Every list of the registry runs the conformance cases.
func TestListConformance_CoversEveryServiceView(t *testing.T) {
	names := map[string]bool{}
	for _, tc := range conformanceCases {
		names[tc.name] = true
	}
	assertCoversServiceViews(t, names)
}

func TestListConformance_NilServiceGivesErrorMsg(t *testing.T) {
	for _, tc := range conformanceCases {
		t.Run(tc.name, func(t *testing.T) {
			v := tc.buildNil()
			v.Update(tea.WindowSizeMsg{Width: 160, Height: 20})
			var seen []tea.Msg
			require.NotPanics(t, func() { seen = pump(v, v.Init(), 5) })
			em, ok := firstOfType[msg.ErrorMsg](seen)
			require.True(t, ok)
			require.ErrorIs(t, em.Err, errNoClient)
			require.Contains(t, loadContexts, tc.name, "add the load context of a new case")
			assert.Equal(t, loadContexts[tc.name], em.Context)
			assert.Equal(t, "\n  Error: AWS client not initialized", plain(v.View().Content))
			assert.Contains(t, v.View().Content, fgSGR(ui.ActiveTheme.Error))
		})
	}
}

var footerFormat = regexp.MustCompile(`^\d+/\d+ \S`)

func TestListConformance_FooterFormat(t *testing.T) {
	for _, tc := range conformanceCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Regexp(t, footerFormat, plain(tc.buildNil().Footer()))
			assert.Regexp(t, footerFormat, plain(conformanceLoaded(t, tc).Footer()))
		})
	}
}

// The view must not shadow the core: no field or method with the name of a
// core field or method (also the ones viewCtx promotes, such as ctx,
// SetContext and viewContext), and no second viewCtx.
func TestListConformance_NoShadowing(t *testing.T) {
	coreMethods := listCoreMethodNames(t)
	require.Contains(t, coreMethods, "update", "the parser found the core methods")
	require.Contains(t, coreMethods, "viewContext", "the parser found the viewCtx methods")
	require.Contains(t, coreMethods, "SetContext", "the parser found the viewCtx methods")

	for _, tc := range conformanceCases {
		t.Run(tc.name, func(t *testing.T) {
			// coreNames holds every field and method name of *listCore[T],
			// promoted ones included.
			coreNames := maps.Clone(coreMethods)
			for _, f := range reflect.VisibleFields(tc.coreType) {
				coreNames[f.Name] = true
			}
			for m := range reflect.PointerTo(tc.coreType).Methods() {
				coreNames[m.Name] = true
			}
			require.Contains(t, coreNames, "ctx", "the promoted viewCtx field is checked")
			require.Contains(t, coreNames, "items", "the core fields are checked")

			vt := reflect.TypeOf(tc.buildNil()).Elem()
			embeds := 0
			for f := range vt.Fields() {
				if f.Type == tc.coreType {
					embeds++
					continue
				}
				assert.False(t, coreNames[f.Name], "field %s shadows a core field or method", f.Name)
			}
			assert.Equal(t, 1, embeds, "the view embeds its listCore once")
			assert.Equal(t, 1, countViewCtx(vt), "viewCtx is embedded once, in listCore")

			for _, name := range methodNames(t, tc.file, tc.typeName) {
				if slices.Contains(tc.allowMethods, name) {
					continue
				}
				assert.False(t, coreNames[name], "method %s.%s shadows a core field or method", tc.typeName, name)
			}
		})
	}
}

// countViewCtx counts the viewCtx embeds reachable through embedded fields.
func countViewCtx(t reflect.Type) int {
	n := 0
	for f := range t.Fields() {
		if !f.Anonymous {
			continue
		}
		if f.Type == reflect.TypeFor[viewCtx]() {
			n++
			continue
		}
		if f.Type.Kind() == reflect.Struct {
			n += countViewCtx(f.Type)
		}
	}
	return n
}

// listCoreMethodNames parses the core files and returns the names of the
// methods of *listCore: the ones declared on listCore and the ones that the
// embedded viewCtx promotes.
func listCoreMethodNames(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, src := range []struct{ file, typeName string }{
		{"listcore.go", "listCore"},
		{"listcore_ui.go", "listCore"},
		{"context.go", "viewCtx"},
	} {
		for _, name := range methodNames(t, src.file, src.typeName) {
			out[name] = true
		}
	}
	return out
}

// methodNames parses file and returns the methods declared on typeName or
// *typeName (also with type parameters).
func methodNames(t *testing.T, file, typeName string) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.SkipObjectResolution)
	require.NoError(t, err)
	var out []string
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || len(fn.Recv.List) == 0 {
			continue
		}
		if receiverName(fn.Recv.List[0].Type) == typeName {
			out = append(out, fn.Name.Name)
		}
	}
	return out
}

func receiverName(e ast.Expr) string {
	switch e := e.(type) {
	case *ast.StarExpr:
		return receiverName(e.X)
	case *ast.IndexExpr:
		return receiverName(e.X)
	case *ast.IndexListExpr:
		return receiverName(e.X)
	case *ast.Ident:
		return e.Name
	}
	return ""
}
