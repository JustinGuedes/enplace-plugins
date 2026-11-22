//go:build !wasip1

package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin/plugintest"
)

// runs answers the app's latest runs: number, result, made how long ago.
func runs(list ...string) string {
	data := []string{}
	for _, x := range list {
		parts := strings.Split(x, " ") // "12 FAILED 5m"
		d, _ := time.ParseDuration(parts[2])
		at := plugintest.Now.Add(-d).Format(time.RFC3339)
		data = append(data, `{"id":"run`+parts[0]+`","attributes":{"number":`+parts[0]+`,"createdDate":"`+at+`","finishedDate":"`+at+`","executionProgress":"COMPLETE","completionStatus":"`+parts[1]+`","sourceCommit":{"commitSha":"abcdef123","message":"Add trips"}},
			"relationships":{"workflow":{"data":{"type":"ciWorkflows","id":"wf1"}},"sourceBranchOrTag":{"data":{"type":"scmGitReferences","id":"ref-main"}}}}`)
	}
	return `{"data":[` + strings.Join(data, ",") + `],"included":[{"type":"ciWorkflows","id":"wf1","attributes":{"name":"TestFlight"}},{"type":"scmGitReferences","id":"ref-main","attributes":{"name":"main"}}]}`
}

func failing(list ...string) map[string]string {
	m := map[string]string{}
	for k, v := range linked {
		m[k] = v
	}
	m["GET /v1/ciProducts/cp1/buildRuns"] = runs(list...)
	m["GET /v1/ciBuildRuns/run12/actions"] = `{"data":[{"id":"a1","attributes":{"name":"Build - iOS","actionType":"BUILD","executionProgress":"COMPLETE","completionStatus":"FAILED"}},
		{"id":"a2","attributes":{"name":"Test - iOS","actionType":"TEST","executionProgress":"COMPLETE","completionStatus":"FAILED"}}]}`
	m["GET /v1/ciBuildActions/a1/issues"] = `{"data":[{"attributes":{"issueType":"WARNING","message":"unused variable"}},
		{"attributes":{"issueType":"ERROR","message":"cannot find 'Trip' in scope","fileSource":{"path":"PlanIt/TripList.swift","lineNumber":12}}}]}`
	m["GET /v1/ciBuildActions/a2/issues"] = `{"data":[]}`
	m["GET /v1/ciBuildActions/a2/testResults"] = `{"data":[{"attributes":{"className":"TripTests","name":"testPacking()","status":"FAILURE","message":"XCTAssertEqual failed","fileSource":{"path":"PlanItTests/TripTests.swift","lineNumber":40}}},
		{"attributes":{"className":"TripTests","name":"testName()","status":"SUCCESS"}}]}`
	m["GET /v1/ciBuildRuns/run13/actions"] = `{"data":[{"id":"a3","attributes":{"name":"Build - iOS","actionType":"BUILD","executionProgress":"COMPLETE","completionStatus":"FAILED"}}]}`
	m["GET /v1/ciBuildActions/a3/issues"] = `{"data":[{"attributes":{"issueType":"ERROR","message":"missing return"}}]}`
	return m
}

func params(t *testing.T, op plugin.Op) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(op.Params)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return m
}

// watching is a schedule request that has looked before (Since an hour ago).
func watching(t *testing.T, runsBefore string) *plugin.Request {
	req := request(t, plugin.HookSchedule)
	s := &Status{ProductID: "cp1", Since: plugintest.Now.Add(-time.Hour)}
	if runsBefore != "" {
		_ = json.Unmarshal([]byte(runsBefore), &s.Runs)
	}
	req.State.PutProject(pid, keyStatus, s)
	return req
}

func TestTheFirstLookFilesNothing(t *testing.T) {
	fake(t, failing("12 FAILED 5m"))
	res := plugintest.Call(t, handle, request(t, plugin.HookSchedule))
	if len(res.Ops) != 0 || status(t, res).Since.IsZero() {
		t.Fatalf("ops %+v", res.Ops)
	}
}

func TestAFailedBuildFilesABugWithItsOutput(t *testing.T) {
	fake(t, failing("12 FAILED 5m", "11 SUCCEEDED 2h"))
	req := watching(t, "")
	res := plugintest.Call(t, handle, req)
	if len(res.Ops) != 1 || res.Ops[0].Kind != "work.create" || res.Ops[0].ProjectID != pid {
		t.Fatalf("ops %+v", res.Ops)
	}
	p := params(t, res.Ops[0])
	body := p["body"].(string)
	if p["title"] != "Fix the failing Xcode Cloud build: TestFlight on main" || p["doer"] != "agent" || p["priority"] != 3.0 {
		t.Fatalf("params %v", p)
	}
	for _, want := range []string{"build **12**", "`abcdef1` Add trips", "Build - iOS: failed", "error: PlanIt/TripList.swift:12: cannot find 'Trip' in scope",
		"test: TripTests.testPacking() PlanItTests/TripTests.swift:40: XCTAssertEqual failed", "runs **TestFlight** on **main** again"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "unused variable") || strings.Contains(body, "testName") {
		t.Fatalf("body has noise:\n%s", body)
	}
	s := status(t, res)
	if b := s.Bugs["TestFlight|main"]; b == nil || b.Run != 12 {
		t.Fatalf("bugs %+v", s.Bugs)
	}

	// Next check: the bug is in Work now, the same run isn't filed again.
	req.State.PutProject(pid, keyStatus, s)
	req.Work = []plugin.Work{{ID: "w1", Key: "PLAN-9", ProjectID: pid, Title: p["title"].(string), Tags: bugTags, StageKind: "todo", CreatedAt: plugintest.Now}}
	res = plugintest.Call(t, handle, req)
	if len(res.Ops) != 0 {
		t.Fatalf("filed twice: %+v", res.Ops)
	}
	s = status(t, res)
	if s.Bugs["TestFlight|main"].Key != "PLAN-9" {
		t.Fatalf("bugs %+v", s.Bugs["TestFlight|main"])
	}

	// It fails again: a note on PLAN-9, for its agent.
	fake(t, failing("13 FAILED 1m", "12 FAILED 5m"))
	req.State.PutProject(pid, keyStatus, s)
	res = plugintest.Call(t, handle, req)
	if len(res.Ops) != 1 || res.Ops[0].Kind != "work.note" {
		t.Fatalf("ops %+v", res.Ops)
	}
	n := params(t, res.Ops[0])
	if n["id"] != "w1" || n["for_agent"] != true || !strings.Contains(n["body"].(string), "build 13 failed again") || !strings.Contains(n["body"].(string), "missing return") {
		t.Fatalf("note %v", n)
	}

	// It passes: noted once.
	s = status(t, res)
	fake(t, failing("14 SUCCEEDED 1m", "13 FAILED 5m"))
	req.State.PutProject(pid, keyStatus, s)
	res = plugintest.Call(t, handle, req)
	if len(res.Ops) != 1 || !strings.Contains(params(t, res.Ops[0])["body"].(string), "build 14 of TestFlight on main passed") {
		t.Fatalf("ops %+v", res.Ops)
	}
	req.State.PutProject(pid, keyStatus, status(t, res))
	if res = plugintest.Call(t, handle, req); len(res.Ops) != 0 {
		t.Fatalf("noted twice: %+v", res.Ops)
	}
}

func TestADoneBugBuildsAgain(t *testing.T) {
	sent := fake(t, failing("12 FAILED 1h"))
	req := watching(t, `[{"id":"run12","number":12,"status":"failed","workflow":"TestFlight","branch":"main"}]`)
	var s *Status
	req.State.GetProject(pid, keyStatus, &s)
	s.Bugs = map[string]*Bug{"TestFlight|main": {Workflow: "TestFlight", Branch: "main", Title: "Fix it", Run: 12, WorkID: "w1", Key: "PLAN-9"}}
	req.State.PutProject(pid, keyStatus, s)
	req.Work = []plugin.Work{{ID: "w1", Key: "PLAN-9", ProjectID: pid, Title: "Fix it", Tags: bugTags, StageKind: "done"}}
	res := plugintest.Call(t, handle, req)
	started := sentLike(*sent, "POST /v1/ciBuildRuns")
	if !strings.Contains(started, `"id":"wf1"`) || !strings.Contains(started, `"id":"ref-main"`) {
		t.Fatalf("started %q", started)
	}
	s = status(t, res)
	if len(s.Bugs) != 0 || s.Requests["fixed-PLAN-9"].Run != 8 || len(res.Ops) != 0 {
		t.Fatalf("status %+v ops %+v", s, res.Ops)
	}
}

func TestDoNothingFilesNothing(t *testing.T) {
	fake(t, failing("12 FAILED 5m"))
	req := watching(t, "")
	req.ProjectSettings[pid]["on_fail"] = failNone
	if res := plugintest.Call(t, handle, req); len(res.Ops) != 0 {
		t.Fatalf("ops %+v", res.Ops)
	}
}

func TestAnOldFailureIsNotFiled(t *testing.T) {
	fake(t, failing("12 FAILED 3h"))
	if res := plugintest.Call(t, handle, watching(t, "")); len(res.Ops) != 0 {
		t.Fatalf("ops %+v", res.Ops)
	}
}
