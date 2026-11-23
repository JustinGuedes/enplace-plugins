//go:build !wasip1

package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	"github.com/JustinGuedes/enplace-plugins/plugins/xcode-cloud/cloud"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin/plugintest"
)

const pid = plugintest.ProjectID

// fake stands in for App Store Connect: each answer by "METHOD path". It
// returns every call it was sent, with its body.
func fake(t *testing.T, answers map[string]string) *[]string {
	sent := []string{}
	plugin.HostFunc = func(raw []byte) []byte {
		var in struct {
			Fetch plugin.FetchRequest `json:"fetch"`
		}
		_ = json.Unmarshal(raw, &in)
		m := in.Fetch.Method
		if m == "" {
			m = "GET"
		}
		path, _, _ := strings.Cut(strings.TrimPrefix(in.Fetch.URL, "https://api.appstoreconnect.apple.com"), "?")
		sent = append(sent, m+" "+path+" "+string(in.Fetch.Body))
		status, body := 404, `{"errors":[{"detail":"not here"}]}`
		if a, ok := answers[m+" "+path]; ok {
			status, body = 200, a
		}
		out, _ := json.Marshal(map[string]any{"fetch": plugin.FetchResponse{Status: status, Body: []byte(body)}})
		return out
	}
	t.Cleanup(func() { plugin.HostFunc = nil })
	return &sent
}

func request(t *testing.T, hook string) *plugin.Request {
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalPKCS8PrivateKey(k)
	req := plugintest.Request(hook)
	req.Plugin.ID = "xcode-cloud"
	req.ProjectSettings = map[string]map[string]any{pid: {"branch": "main"}}
	req.Secrets = map[string]string{"asc_issuer_id": "iss", "asc_key_id": "KEY1", "asc_private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))}
	req.Peers = map[string]plugin.State{}
	publish(req, "app", "public.app", map[string]string{"bundle_id": "com.guedio.planit"})
	return req
}

func publish(req *plugin.Request, id, k string, v any) {
	st := req.Peers[id]
	st.PutProject(pid, k, v)
	req.Peers[id] = st
}

var linked = map[string]string{
	"GET /v1/apps":                             `{"data":[{"type":"apps","id":"900"}]}`,
	"GET /v1/apps/900/ciProduct":               `{"data":{"type":"ciProducts","id":"cp1"}}`,
	"GET /v1/ciProducts/cp1/workflows":         `{"data":[{"id":"wf-off","attributes":{"name":"Archive","isEnabled":false}},{"id":"wf1","attributes":{"name":"TestFlight","isEnabled":true}}]}`,
	"GET /v1/ciWorkflows/wf1/repository":       `{"data":{"type":"scmRepositories","id":"r1"}}`,
	"GET /v1/scmRepositories/r1/gitReferences": `{"data":[{"id":"ref-main","attributes":{"name":"main","kind":"BRANCH"}},{"id":"ref-tag","attributes":{"name":"main","kind":"TAG"}}]}`,
	"POST /v1/ciBuildRuns":                     `{"data":{"type":"ciBuildRuns","id":"run8","attributes":{"number":8}}}`,
	"GET /v1/ciProducts/cp1/buildRuns": `{"data":[{"id":"run7","attributes":{"number":7,"executionProgress":"COMPLETE","completionStatus":"SUCCEEDED","startReason":"GIT_REF_CHANGE","createdDate":"2026-11-20T10:00:00Z","startedDate":"2026-11-20T10:02:00Z","finishedDate":"2026-11-20T10:12:00Z","sourceCommit":{"commitSha":"abc","message":"Trips\nmore"}},
		"relationships":{"workflow":{"data":{"type":"ciWorkflows","id":"wf1"}},"sourceBranchOrTag":{"data":{"type":"scmGitReferences","id":"ref-main"}}}}],
		"included":[{"type":"ciWorkflows","id":"wf1","attributes":{"name":"TestFlight"}},{"type":"scmGitReferences","id":"ref-main","attributes":{"name":"main"}}]}`,
}

func status(t *testing.T, res *plugin.Response) *Status {
	t.Helper()
	var s *Status
	if res.State == nil || !res.State.GetProject(pid, keyStatus, &s) {
		t.Fatal("no status")
	}
	return s
}

func TestStartsWhatAnotherPluginAsksForOnce(t *testing.T) {
	sent := fake(t, linked)
	req := request(t, plugin.HookSchedule)
	publish(req, "testflight", "public.ci_request", Request{ID: "testflight-1", At: plugintest.Now.Add(-time.Minute)})
	res := plugintest.Call(t, handle, req)
	s := status(t, res)
	if s.ProductID != "cp1" || len(s.Workflows) != 2 || len(s.Runs) != 1 || s.Runs[0].Workflow != "TestFlight" || s.Runs[0].Branch != "main" || s.Runs[0].Status != "passed" || s.Runs[0].Message != "Trips" ||
		s.Runs[0].Trigger != "push" || s.Runs[0].Finished.Sub(s.Runs[0].Started) != 10*time.Minute {
		t.Fatalf("status %+v", s)
	}
	if a := s.Requests["testflight-1"]; a.State != "started" || a.Run != 8 || a.By != "testflight" {
		t.Fatalf("answer %+v (sent %v)", a, *sent)
	}
	started := ""
	for _, x := range *sent {
		if strings.HasPrefix(x, "POST /v1/ciBuildRuns") {
			started = x
		}
	}
	if !strings.Contains(started, `"id":"wf1"`) || !strings.Contains(started, `"id":"ref-main"`) {
		t.Fatalf("started %s", started)
	}
	var ci map[string]any
	res.State.GetProject(pid, "public.ci", &ci)
	if ci["provider"] != "xcode-cloud" || ci["ready"] != true || len(ci["runs"].([]any)) != 1 {
		t.Fatalf("public.ci %v", ci)
	}

	// The same request isn't started twice.
	req.State.PutProject(pid, keyStatus, s)
	*sent = nil
	plugintest.Call(t, handle, req)
	for _, x := range *sent {
		if strings.HasPrefix(x, "POST") {
			t.Fatalf("started again: %v", *sent)
		}
	}
}

func TestAnOldRequestIsNeverStarted(t *testing.T) {
	sent := fake(t, linked)
	req := request(t, plugin.HookSchedule)
	publish(req, "testflight", "public.ci_request", Request{ID: "testflight-0", At: plugintest.Now.Add(-3 * time.Hour)})
	plugintest.Call(t, handle, req)
	for _, x := range *sent {
		if strings.HasPrefix(x, "POST") {
			t.Fatalf("started an old request: %v", *sent)
		}
	}
}

func TestAnAppWithoutXcodeCloud(t *testing.T) {
	fake(t, map[string]string{"GET /v1/apps": `{"data":[{"type":"apps","id":"900"}]}`})
	req := request(t, plugin.HookSchedule)
	res := plugintest.Call(t, handle, req)
	s := status(t, res)
	if !s.NotSetUp || s.Error != "" {
		t.Fatalf("status %+v", s)
	}
	req.State.PutProject(pid, keyStatus, s)
	req.Hook, req.Slots = plugin.HookRender, []string{"project_card:ci", plugin.SlotSettingsPanel}
	out := plugintest.Call(t, handle, req)
	if c := out.Cards["project_card:ci"]; c[0].Value != "Not set up yet" || !strings.Contains(c[1].Text, "Connect Xcode Cloud once") {
		t.Fatalf("card %+v", c)
	}
	if items := out.Cards[plugin.SlotSettingsPanel][1].Items; !items[0].Done || !items[1].Done || items[2].Done {
		t.Fatalf("steps %+v", items)
	}
}

// Only a passed run of Enplace's own workflow is known to make something;
// a run that only checks the code makes nothing.
func TestArtifactOnlyForOwnWorkflow(t *testing.T) {
	req := &plugin.Request{ProjectSettings: map[string]map[string]any{pid: {"makes": "TestFlight internal only"}}}
	r := &run{req: req, res: &plugin.Response{}, pid: pid}
	s := &Status{Own: "wf-own"}
	for _, c := range []struct {
		x    cloud.Run
		want string
	}{
		{cloud.Run{Workflow: cloud.OwnName, Status: "passed"}, "testflight_internal"},
		{cloud.Run{Workflow: cloud.OwnName, Status: "failed"}, ""},
		{cloud.Run{Workflow: "Main checks", Status: "passed"}, ""},
	} {
		if got := r.artifact(s, c.x); got != c.want {
			t.Errorf("%+v: got %q, want %q", c.x, got, c.want)
		}
	}
}

// A request for a track starts that track's workflow on its branch, and
// each run is tagged with the track naming its workflow (ENP-350).
func TestTracks(t *testing.T) {
	sent := fake(t, linked)
	req := request(t, plugin.HookSchedule)
	req.Tracks = map[string][]plugin.Track{pid: {
		{Key: "main", Name: "Main builds", Purpose: "check", On: true, Settings: map[string]any{"workflow": "Archive", "branch": "main"}},
		{Key: "internal-beta", Name: "Internal beta", Purpose: "internal", Makes: true, On: true, Settings: map[string]any{"workflow": "TestFlight", "branch": "main"}},
	}}
	publish(req, "testflight", "public.ci_request", Request{ID: "tf-2", Track: "internal-beta", At: plugintest.Now.Add(-time.Minute)})
	res := plugintest.Call(t, handle, req)
	started := ""
	for _, x := range *sent {
		if strings.HasPrefix(x, "POST /v1/ciBuildRuns") {
			started = x
		}
	}
	if !strings.Contains(started, `"id":"wf1"`) {
		t.Fatalf("started %s", started)
	}
	var ci struct {
		Runs []cloud.Run `json:"runs"`
	}
	res.State.GetProject(pid, "public.ci", &ci)
	if len(ci.Runs) != 1 || ci.Runs[0].Track != "internal-beta" {
		t.Fatalf("runs %+v", ci.Runs)
	}

	// With one track, every run is on it; with none naming the workflow, none.
	r := &run{req: req, pid: pid}
	if got := r.trackOfRun(cloud.Run{Workflow: "Nightly"}); got != "" {
		t.Fatalf("unnamed workflow on %q", got)
	}
	req.Tracks[pid] = req.Tracks[pid][:1]
	if got := r.trackOfRun(cloud.Run{Workflow: "Nightly"}); got != "main" {
		t.Fatalf("one track: %q", got)
	}
}
