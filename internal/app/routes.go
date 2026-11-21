package app

import (
	"fmt"
	"time"

	appmsg "github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/nav"
	"github.com/juthrbog/lazycloud/internal/ui"
	"github.com/juthrbog/lazycloud/internal/views"
)

// route tells the app how to open a view.
type route struct {
	// build makes the view. p holds the Params of the NavigateMsg.
	build func(m Model, p map[string]string) nav.View
	// root is the service view that the app opens again after a region or
	// profile switch, for example "s3_list" for the objects of a bucket. A
	// service view is its own root. It is empty for a view without AWS
	// data, so the app looks further down the stack.
	root string
	// noSession is true for a view that works without an AWS session.
	noSession bool
}

// routes maps each view ID to its route. Every view ID of the registry
// must be here (TestRoutes).
var routes = map[string]route{
	"ec2_list": {root: "ec2_list", build: func(m Model, _ map[string]string) nav.View {
		return views.NewEC2List(m.svc.EC2, m.svc.Client)
	}},
	"ami_list": {root: "ami_list", build: func(m Model, _ map[string]string) nav.View {
		return views.NewAMIList(m.svc.EC2)
	}},
	"sg_list": {root: "sg_list", build: func(m Model, _ map[string]string) nav.View {
		return views.NewSGList(m.svc.EC2)
	}},
	"vpc_list": {root: "vpc_list", build: func(m Model, _ map[string]string) nav.View {
		return views.NewVPCList(m.svc.EC2)
	}},
	"subnet_list": {root: "subnet_list", build: func(m Model, p map[string]string) nav.View {
		return views.NewSubnetList(m.svc.EC2, p["vpc_id"])
	}},
	"s3_list": {root: "s3_list", build: func(m Model, _ map[string]string) nav.View {
		return views.NewS3List(m.svc.S3, m.activeRegion())
	}},
	"s3_objects": {root: "s3_list", build: func(m Model, p map[string]string) nav.View {
		return views.NewS3Objects(m.svc.S3, p["bucket"], p["prefix"])
	}},
	"s3_versions": {root: "s3_list", build: func(m Model, p map[string]string) nav.View {
		return views.NewS3Versions(m.svc.S3, p["bucket"], p["key"])
	}},
	"sqs_queues": {root: "sqs_queues", build: func(m Model, _ map[string]string) nav.View {
		return views.NewSQSQueues(m.svc.SQS)
	}},
	"sqs_messages": {root: "sqs_queues", build: func(m Model, p map[string]string) nav.View {
		return views.NewSQSMessages(m.svc.SQS, p["queue_url"], p["queue_name"], p["fifo"] == "true")
	}},
	"logs_groups": {root: "logs_groups", build: func(m Model, _ map[string]string) nav.View {
		return views.NewLogGroups(m.svc.Logs)
	}},
	"logs_streams": {root: "logs_groups", build: func(m Model, p map[string]string) nav.View {
		return views.NewLogStreams(m.svc.Logs, p["log_group"])
	}},
	"logs_events": {root: "logs_groups", build: func(m Model, p map[string]string) nav.View {
		return views.NewLogEvents(m.svc.Logs, p)
	}},
	"lambda_functions": {root: "lambda_functions", build: func(m Model, _ map[string]string) nav.View {
		return views.NewLambdaFunctions(m.svc.Lambda)
	}},
	"lambda_aliases": {root: "lambda_functions", build: func(m Model, p map[string]string) nav.View {
		return views.NewLambdaAliases(m.svc.Lambda, p["function"], p["log_group"])
	}},
	"lambda_versions": {root: "lambda_functions", build: func(m Model, p map[string]string) nav.View {
		return views.NewLambdaVersions(m.svc.Lambda, p["function"], p["log_group"])
	}},
	"lambda_triggers": {root: "lambda_functions", build: func(m Model, p map[string]string) nav.View {
		return views.NewLambdaTriggers(m.svc.Lambda, p["function"])
	}},
	"lambda_invocations": {root: "lambda_functions", build: func(m Model, p map[string]string) nav.View {
		return views.NewLambdaInvocations(m.svc.Logs, p)
	}},
	"ecs_clusters": {root: "ecs_clusters", build: func(m Model, _ map[string]string) nav.View {
		return views.NewECSClusters(m.svc.ECS)
	}},
	"ecs_services": {root: "ecs_clusters", build: func(m Model, p map[string]string) nav.View {
		return views.NewECSServices(m.svc.ECS, p["cluster"])
	}},
	"ecs_tasks": {root: "ecs_clusters", build: func(m Model, p map[string]string) nav.View {
		return views.NewECSTasks(m.svc.ECS, p["cluster"], p["service"])
	}},
	"eventlog": {noSession: true, build: func(Model, map[string]string) nav.View {
		return views.NewEventLog()
	}},
	"content": {noSession: true, build: func(_ Model, p map[string]string) nav.View {
		format := ui.ContentFormat(p["format"])
		if format == "" {
			format = ui.FormatAuto
		}
		// A unique ID, so that two content views are never the same view.
		id := fmt.Sprintf("content:%s:%d", p["title"], time.Now().UnixNano())
		return views.NewContentViewer(id, p["title"], p["content"], format)
	}},
}

// needsSession reports whether the view needs an AWS session. An unknown
// view needs one, so the app shows the session error and not a blank view.
func needsSession(viewID string) bool {
	r, ok := routes[viewID]
	return !ok || !r.noSession
}

// resolveView makes the view that n asks for, or returns nil for an
// unknown view ID.
func (m Model) resolveView(n appmsg.NavigateMsg) nav.View {
	r, ok := routes[n.ViewID]
	if !ok {
		return nil
	}
	return r.build(m, n.Params)
}
