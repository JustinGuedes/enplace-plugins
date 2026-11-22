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

// withDefault is an app with only the Default workflow Xcode made.
func withDefault(extra map[string]string) map[string]string {
	m := map[string]string{
		"GET /v1/apps":                     `{"data":[{"type":"apps","id":"900"}]}`,
		"GET /v1/apps/900/ciProduct":       `{"data":{"type":"ciProducts","id":"cp1"}}`,
		"GET /v1/ciProducts/cp1/workflows": `{"data":[{"id":"wfd","attributes":{"name":"Default","isEnabled":true}}]}`,
		"GET /v1/ciWorkflows/wfd": `{"data":{"id":"wfd","attributes":{"name":"Default","isEnabled":true,"containerFilePath":"PlanIt.xcodeproj",
			"actions":[{"name":"Archive - iOS","actionType":"ARCHIVE","scheme":"PlanIt","platform":"IOS","buildDistributionAudience":null,"isRequiredToPass":true}],
			"branchStartCondition":{"source":{"isAllMatch":false,"patterns":[{"pattern":"main","isPrefix":false}]}}},
			"relationships":{"repository":{"data":{"type":"scmRepositories","id":"r1"}},"xcodeVersion":{"data":{"type":"ciXcodeVersions","id":"x1"}},"macOsVersion":{"data":{"type":"ciMacOsVersions","id":"m1"}}}}}`,
		"POST /v1/ciWorkflows":             `{"data":{"type":"ciWorkflows","id":"wfe"}}`,
		"GET /v1/ciProducts/cp1/buildRuns": `{"data":[]}`,
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func sentLike(sent []string, prefix string) string {
	for _, x := range sent {
		if strings.HasPrefix(x, prefix) {
			return x
		}
	}
	return ""
}

func TestSetItUpMakesEnplacesWorkflowFromDefault(t *testing.T) {
	sent := fake(t, withDefault(nil))
	req := request(t, plugin.HookAction)
	req.Action = &plugin.Action{Name: "setup"}
	res := plugintest.Call(t, handle, req)
	made := sentLike(*sent, "POST /v1/ciWorkflows")
	for _, want := range []string{`"name":"Enplace store build"`, `"containerFilePath":"PlanIt.xcodeproj"`, `"scheme":"PlanIt"`, `"buildDistributionAudience":"APP_STORE_ELIGIBLE"`,
		`"manualBranchStartCondition":{"source":{"isAllMatch":true}}`, `"id":"cp1"`, `"id":"r1"`, `"id":"x1"`, `"id":"m1"`} {
		if !strings.Contains(made, want) {
			t.Fatalf("made %s, lacks %s (toast %q)", made, want, res.Toast)
		}
	}
	s := status(t, res)
	if s.Own != "wfe" || s.Branch != "main" || !strings.Contains(res.Toast, "Made") {
		t.Fatalf("status %+v toast %q", s, res.Toast)
	}
	var b map[string]any
	res.State.GetProject(pid, "public.builder", &b)
	if b["name"] != "Xcode Cloud" || b["ready"] != true {
		t.Fatalf("public.builder %v", b)
	}
}

func TestSetItUpBeforeXcodeCloudIsOn(t *testing.T) {
	fake(t, map[string]string{"GET /v1/apps": `{"data":[{"type":"apps","id":"900"}]}`})
	req := request(t, plugin.HookAction)
	req.Action = &plugin.Action{Name: "setup"}
	res := plugintest.Call(t, handle, req)
	if !strings.Contains(res.Toast, "in Xcode first") {
		t.Fatalf("toast %q", res.Toast)
	}
	var b map[string]any
	res.State.GetProject(pid, "public.builder", &b)
	if b["ready"] != false || !strings.Contains(b["note"].(string), "Create Workflow") {
		t.Fatalf("public.builder %v", b)
	}
}

const ownWorkflows = `{"data":[{"id":"wfd","attributes":{"name":"Default","isEnabled":true}},{"id":"wfe","attributes":{"name":"Enplace store build","isEnabled":true}}]}`

func ownWorkflow(audience string) string {
	return `{"data":{"id":"wfe","attributes":{"name":"Enplace store build","isEnabled":true,"containerFilePath":"PlanIt.xcodeproj",
		"actions":[{"name":"Archive - iOS","actionType":"ARCHIVE","scheme":"PlanIt","platform":"IOS","buildDistributionAudience":` + audience + `,"isRequiredToPass":true}],
		"manualBranchStartCondition":{"source":{"isAllMatch":true}}},
		"relationships":{"repository":{"data":{"type":"scmRepositories","id":"r1"}}}}}`
}

// A store build App passes on starts Enplace's workflow, only when the
// app's Builds come from picks Xcode Cloud.
func TestStartsAStoreBuildWhenPicked(t *testing.T) {
	sent := fake(t, withDefault(map[string]string{
		"GET /v1/ciProducts/cp1/workflows":         ownWorkflows,
		"GET /v1/ciWorkflows/wfe":                  ownWorkflow(`"APP_STORE_ELIGIBLE"`),
		"GET /v1/ciWorkflows/wfe/repository":       `{"data":{"type":"scmRepositories","id":"r1"}}`,
		"GET /v1/scmRepositories/r1/gitReferences": `{"data":[{"id":"ref-main","attributes":{"name":"main","kind":"BRANCH"}}]}`,
		"POST /v1/ciBuildRuns":                     `{"data":{"type":"ciBuildRuns","id":"run9","attributes":{"number":9}}}`,
	}))
	req := request(t, plugin.HookSchedule)
	req.ProjectSettings[pid] = map[string]any{}
	publish(req, "app", "public.build_request", Request{ID: "testflight-5", At: plugintest.Now.Add(-time.Minute)})

	// Not picked: left alone.
	publish(req, "app", "public.app", map[string]any{"bundle_id": "com.guedio.planit", "builder": map[string]string{"id": "mac"}})
	plugintest.Call(t, handle, req)
	if x := sentLike(*sent, "POST /v1/ciBuildRuns"); x != "" {
		t.Fatalf("started without being picked: %s", x)
	}

	publish(req, "app", "public.app", map[string]any{"bundle_id": "com.guedio.planit", "builder": map[string]string{"id": "xcode-cloud"}})
	*sent = nil
	res := plugintest.Call(t, handle, req)
	started := sentLike(*sent, "POST /v1/ciBuildRuns")
	if !strings.Contains(started, `"id":"wfe"`) || !strings.Contains(started, `"id":"ref-main"`) {
		t.Fatalf("started %q (sent %v)", started, *sent)
	}
	if x := sentLike(*sent, "PATCH"); x != "" {
		t.Fatalf("fixed a workflow that was fine: %s", x)
	}
	var b struct {
		Requests map[string]Answer `json:"requests"`
	}
	res.State.GetProject(pid, "public.builder", &b)
	if a := b.Requests["testflight-5"]; a.State != "started" || a.Run != 9 {
		t.Fatalf("answer %+v", b)
	}
}

// Changed in Xcode, or a new "Builds it makes": it's put back.
func TestPutsItsWorkflowBack(t *testing.T) {
	sent := fake(t, withDefault(map[string]string{
		"GET /v1/ciProducts/cp1/workflows": ownWorkflows,
		"GET /v1/ciWorkflows/wfe":          ownWorkflow(`"INTERNAL_ONLY"`),
		"PATCH /v1/ciWorkflows/wfe":        `{}`,
	}))
	req := request(t, plugin.HookSchedule)
	plugintest.Call(t, handle, req)
	fix := sentLike(*sent, "PATCH /v1/ciWorkflows/wfe")
	if !strings.Contains(fix, `"buildDistributionAudience":"APP_STORE_ELIGIBLE"`) {
		t.Fatalf("fix %q", fix)
	}
	*sent = nil
	req.ProjectSettings[pid]["makes"] = "TestFlight internal only"
	plugintest.Call(t, handle, req)
	if x := sentLike(*sent, "PATCH"); x != "" {
		t.Fatalf("fixed what's already right: %s", x)
	}
	req.ProjectSettings[pid]["makes"] = "Don't upload"
	*sent = nil
	plugintest.Call(t, handle, req)
	if fix := sentLike(*sent, "PATCH"); !strings.Contains(fix, `"buildDistributionAudience":null`) {
		t.Fatalf("fix %q", fix)
	}
}

// Update Xcode Cloud copies Enplace's workflow again from the Default
// one: a new scheme and Xcode version there reach it, and the toast says so.
func TestUpdateXcodeCloudCopiesFromDefaultAgain(t *testing.T) {
	own := `{"data":{"id":"wfe","attributes":{"name":"Enplace store build","isEnabled":true,"containerFilePath":"PlanIt.xcodeproj",
		"actions":[{"name":"Archive - iOS","actionType":"ARCHIVE","scheme":"OldScheme","platform":"IOS","buildDistributionAudience":"APP_STORE_ELIGIBLE","isRequiredToPass":true}],
		"manualBranchStartCondition":{"source":{"isAllMatch":true}}},
		"relationships":{"repository":{"data":{"type":"scmRepositories","id":"r1"}},"xcodeVersion":{"data":{"type":"ciXcodeVersions","id":"x0"}},"macOsVersion":{"data":{"type":"ciMacOsVersions","id":"m1"}}}}}`
	sent := fake(t, withDefault(map[string]string{
		"GET /v1/ciProducts/cp1/workflows": ownWorkflows,
		"GET /v1/ciWorkflows/wfe":          own,
		"PATCH /v1/ciWorkflows/wfe":        `{}`,
	}))
	req := request(t, plugin.HookAction)
	req.Action = &plugin.Action{Name: "update"}
	res := plugintest.Call(t, handle, req)
	var redo string
	for _, x := range *sent {
		if strings.HasPrefix(x, "PATCH /v1/ciWorkflows/wfe") && strings.Contains(x, "relationships") {
			redo = x
		}
	}
	for _, want := range []string{`"scheme":"PlanIt"`, `"id":"x1"`, `"id":"m1"`} {
		if !strings.Contains(redo, want) {
			t.Fatalf("redo %q lacks %s (sent %v)", redo, want, *sent)
		}
	}
	if !strings.Contains(res.Toast, "Updated Enplace store build") || !strings.Contains(res.Toast, "scheme PlanIt") || !strings.Contains(res.Toast, "Xcode version") {
		t.Fatalf("toast %q", res.Toast)
	}
	s := status(t, res)
	if !s.InStep || s.Kept.IsZero() {
		t.Fatalf("status %+v", s)
	}

	// The panel says it matches and offers Update Xcode Cloud.
	req.State.PutProject(pid, keyStatus, s)
	req.Hook, req.Slots = plugin.HookRender, []string{plugin.SlotSettingsPanel}
	out := plugintest.Call(t, handle, req).Cards[plugin.SlotSettingsPanel]
	all, _ := json.Marshal(out)
	for _, want := range []string{"matches these settings", "Enplace last changed it just now: scheme PlanIt", `"label":"Update Xcode Cloud"`, "files a bug"} {
		if !strings.Contains(string(all), want) {
			t.Fatalf("panel lacks %q: %s", want, all)
		}
	}
}

// Update Xcode Cloud makes the workflow again when it was deleted.
func TestUpdateXcodeCloudMakesItAgain(t *testing.T) {
	sent := fake(t, withDefault(nil))
	req := request(t, plugin.HookAction)
	req.State.PutProject(pid, keyStatus, &Status{ProductID: "cp1", Own: "gone"})
	req.Action = &plugin.Action{Name: "update"}
	res := plugintest.Call(t, handle, req)
	if sentLike(*sent, "POST /v1/ciWorkflows") == "" || !strings.Contains(res.Toast, "Made") {
		t.Fatalf("toast %q sent %v", res.Toast, *sent)
	}
}

// Nothing to change: it says so.
func TestUpdateXcodeCloudWhenItsRight(t *testing.T) {
	own := strings.Replace(ownWorkflow(`"APP_STORE_ELIGIBLE"`), `"relationships":{`, `"relationships":{"xcodeVersion":{"data":{"type":"ciXcodeVersions","id":"x1"}},"macOsVersion":{"data":{"type":"ciMacOsVersions","id":"m1"}},`, 1)
	sent := fake(t, withDefault(map[string]string{"GET /v1/ciProducts/cp1/workflows": ownWorkflows, "GET /v1/ciWorkflows/wfe": own}))
	req := request(t, plugin.HookAction)
	req.Action = &plugin.Action{Name: "update"}
	res := plugintest.Call(t, handle, req)
	if sentLike(*sent, "PATCH") != "" || !strings.Contains(res.Toast, "already matches") {
		t.Fatalf("toast %q sent %v", res.Toast, *sent)
	}
}
