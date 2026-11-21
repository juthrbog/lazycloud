package main

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
)

// The stream kinds of the logs seeder. A Lambda kind writes invocations:
// the platform lines (START, END, REPORT) and a line of the function, as
// Lambda writes them. Events then counts invocations, not lines. See
// docs/design/lambda.md, "Seed and tests".
const (
	streamKindLambdaText     = "lambda_text"      // the text format
	streamKindLambdaJSON     = "lambda_json"      // the JSON format
	streamKindLambdaJSONWarn = "lambda_json_warn" // JSON with SystemLogLevel WARN: only failed invocations
)

// invocation is one seeded invocation.
type invocation struct {
	at        time.Time
	requestID string
	duration  float64 // ms
	billed    int     // ms
	memoryMB  int
	usedMB    int
	initMS    float64 // 0: a warm start
	status    string  // "" (success), "error" or "timeout"
	errorType string
}

// seedInvocations returns n invocations spread over the 50 minutes before
// now, oldest first. Every 10th is a cold start, the 8th is an error and
// the 14th is a timeout.
func seedInvocations(n int, now time.Time) []invocation {
	span := 50 * time.Minute
	out := make([]invocation, n)
	for i := range n {
		inv := invocation{
			at:        now.Add(-span + span*time.Duration(i)/time.Duration(n)),
			requestID: fmt.Sprintf("8f1c%04x-0000-4000-8000-%012d", i, i),
			duration:  12.5 + float64(i%40)*3.25,
			memoryMB:  256,
			usedMB:    60 + i%30,
		}
		if i%10 == 0 {
			inv.initMS = 180.4 + float64(i%7)*10
		}
		switch i % 20 {
		case 7:
			inv.status, inv.errorType = "error", "Runtime.Unknown"
		case 13:
			inv.status, inv.duration = "timeout", 3000
		}
		inv.billed = int(inv.duration+0.999) + int(inv.initMS)
		out[i] = inv
	}
	return out
}

// lambdaEvents returns the log events of n invocations in the given kind,
// oldest first, as PutLogEvents needs.
func lambdaEvents(kind string, n int, now time.Time) []cwltypes.InputLogEvent {
	var events []cwltypes.InputLogEvent
	add := func(at time.Time, m string) {
		events = append(events, cwltypes.InputLogEvent{Timestamp: aws.Int64(at.UnixMilli()), Message: aws.String(m)})
	}
	for _, inv := range seedInvocations(n, now) {
		start := inv.at.Add(-time.Duration((inv.duration + inv.initMS) * float64(time.Millisecond)))
		switch kind {
		case streamKindLambdaText:
			add(start, fmt.Sprintf("START RequestId: %s Version: $LATEST\n", inv.requestID))
			add(start.Add(time.Millisecond), textBody(inv))
			add(inv.at, fmt.Sprintf("END RequestId: %s\n", inv.requestID))
			add(inv.at, textReport(inv))
		case streamKindLambdaJSON, streamKindLambdaJSONWarn:
			warn := kind == streamKindLambdaJSONWarn
			if !warn {
				add(start, jsonLine(start, "platform.start", map[string]any{"requestId": inv.requestID, "version": "$LATEST"}))
			}
			if !warn || inv.status != "" {
				add(start.Add(time.Millisecond), jsonApp(start.Add(time.Millisecond), inv))
				add(inv.at, jsonReport(inv))
			}
		}
	}
	return events
}

func textBody(inv invocation) string {
	switch inv.status {
	case "error":
		return fmt.Sprintf("[ERROR] ValueError: boom\tRequestId: %s\n", inv.requestID)
	case "timeout":
		return fmt.Sprintf("%s Task timed out after 3.00 seconds\n", inv.at.UTC().Format("2006-01-02T15:04:05.000Z"))
	}
	return "processed order\n"
}

// textReport writes a REPORT line as Lambda does: fields split by tabs, a
// tab and a newline at the end.
func textReport(inv invocation) string {
	s := fmt.Sprintf("REPORT RequestId: %s\tDuration: %.2f ms\tBilled Duration: %d ms\tMemory Size: %d MB\tMax Memory Used: %d MB\t",
		inv.requestID, inv.duration, inv.billed, inv.memoryMB, inv.usedMB)
	if inv.initMS > 0 {
		s += fmt.Sprintf("Init Duration: %.2f ms\t", inv.initMS)
	}
	switch inv.status {
	case "error":
		s += "Status: error\tError Type: " + inv.errorType + "\t"
	case "timeout":
		s += "Status: timeout\t"
	}
	return s + "\n"
}

func jsonLine(at time.Time, typ string, record map[string]any) string {
	b, _ := json.Marshal(map[string]any{"time": at.UTC().Format(time.RFC3339Nano), "type": typ, "record": record})
	return string(b)
}

func jsonApp(at time.Time, inv invocation) string {
	level, msg := "INFO", "processed order"
	if inv.status != "" {
		level, msg = "ERROR", "the call failed: "+inv.status
	}
	b, _ := json.Marshal(map[string]any{"timestamp": at.UTC().Format(time.RFC3339Nano), "level": level,
		"requestId": inv.requestID, "message": msg})
	return string(b)
}

func jsonReport(inv invocation) string {
	metrics := map[string]any{"durationMs": inv.duration, "billedDurationMs": inv.billed,
		"memorySizeMB": inv.memoryMB, "maxMemoryUsedMB": inv.usedMB}
	if inv.initMS > 0 {
		metrics["initDurationMs"] = inv.initMS
	}
	status := "success"
	if inv.status != "" {
		status = inv.status
	}
	record := map[string]any{"requestId": inv.requestID, "metrics": metrics, "status": status}
	if inv.errorType != "" {
		record["errorType"] = inv.errorType
	}
	return jsonLine(inv.at, "platform.report", record)
}
