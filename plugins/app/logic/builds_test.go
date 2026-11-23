//go:build !wasip1

package main

import (
	"strings"
	"testing"
	"time"

	"github.com/JustinGuedes/enplace-plugins/plugins/app/store"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin/plugintest"
)

const pid = plugintest.ProjectID

func buildsRun(settings map[string]any) *run {
	req := plugintest.Request(plugin.HookSchedule)
	settings["app_store"] = "com.guedio.planit"
	req.ProjectSettings = map[string]map[string]any{pid: settings}
	req.Peers = map[string]plugin.State{}
	req.Secrets = map[string]string{"asc_key_id": "KEY1", "asc_issuer_id": "iss", "asc_private_key": "-----BEGIN PRIVATE KEY-----"}
	req.State.PutProject(pid, "info", &Info{Ref: "com.guedio.planit/", App: store.App{BundleID: "com.guedio.planit", Name: "Plan It"}})
	return &run{req: req, res: &plugin.Response{}, pid: pid}
}

func publish(r *run, id, k string, v any) {
	st := r.req.Peers[id]
	st.PutProject(pid, k, v)
	r.req.Peers[id] = st
}

func asked(r *run) {
	publish(r, "testflight", "public.build_request", BuildRequest{ID: "testflight-1", At: plugintest.Now.Add(-time.Minute), Version: "1.1", Build: "13"})
}

// The track's Builds step makes its builds (ENP-351); without a build
// plugin it's the person, or an agent on the Mac when they say so.
func TestBuilderIsTheTracksBuildsStep(t *testing.T) {
	r := buildsRun(map[string]any{})
	if b := r.builder(""); b.ID != handID {
		t.Fatalf("with no builder plugin: %+v", b)
	}
	r.req.ProjectSettings[pid]["builder"] = byMac
	if b := r.builder("internal-beta"); b.ID != macID {
		t.Fatalf("the Mac without a plugin: %+v", b)
	}
	publish(r, "xcode-cloud", "public.builder", map[string]any{"name": "Xcode Cloud", "ready": false, "note": "Press Set it up.", "tracks": []string{"release"}})
	publish(r, "github-actions", "public.builder", map[string]any{"name": "GitHub Actions", "ready": true, "tracks": []string{"internal-beta"}})
	if b := r.builder("release"); b.ID != "xcode-cloud" || b.Ready {
		t.Fatalf("the release track's builder, ready or not: %+v", b)
	}
	if b := r.builder("internal-beta"); b.ID != "github-actions" {
		t.Fatalf("the beta track's builder: %+v", b)
	}
	if b := r.builder(""); b.ID != "github-actions" {
		t.Fatalf("no track takes a ready builder: %+v", b)
	}
	if b := r.builder("main"); b.ID != "github-actions" {
		t.Fatalf("a track no builder names takes a ready one: %+v", b)
	}

	// The request goes to its track's builder, and App says who builds
	// each track.
	r.req.Tracks = map[string][]plugin.Track{pid: {{Key: "internal-beta"}, {Key: "release"}}}
	publish(r, "testflight", "public.build_request", BuildRequest{ID: "testflight-1", At: plugintest.Now.Add(-time.Minute), Track: "release"})
	r.routeBuilds()
	var q BuildRequest
	if !r.res.State.GetProject(pid, "public.build_request", &q) || q.To != "xcode-cloud" {
		t.Fatalf("passed on %+v", q)
	}
	var b Builder
	r.res.State.GetProject(pid, "public.build", &b)
	if b.ID != "xcode-cloud" || b.ByTrack["internal-beta"].ID != "github-actions" || b.ByTrack["release"].ID != "xcode-cloud" {
		t.Fatalf("public.build %+v", b)
	}
}

// A plugin builder gets the request passed on, and the asker sees its
// answers through App.
func TestPassesTheRequestToThePluginBuilder(t *testing.T) {
	r := buildsRun(map[string]any{})
	asked(r)
	publish(r, "xcode-cloud", "public.builder", map[string]any{"name": "Xcode Cloud", "ready": true,
		"requests": map[string]any{"testflight-1": map[string]any{"state": "started", "run": 9}}})
	r.routeBuilds()
	var q BuildRequest
	if !r.res.State.GetProject(pid, "public.build_request", &q) || q.ID != "testflight-1" || q.By != "testflight" || len(r.res.Ops) != 0 {
		t.Fatalf("passed on %+v ops %+v", q, r.res.Ops)
	}
	var b Builder
	r.res.State.GetProject(pid, "public.build", &b)
	if b.ID != "xcode-cloud" || b.Requests["testflight-1"].Run != 9 {
		t.Fatalf("public.build %+v", b)
	}
}

func TestTheMacGetsAWorkItemOnce(t *testing.T) {
	r := buildsRun(map[string]any{"builder": byMac})
	asked(r)
	r.routeBuilds()
	if len(r.res.Ops) != 1 || r.res.Ops[0].Kind != "work.create" || !r.res.Ops[0].Asked {
		t.Fatalf("ops %+v", r.res.Ops)
	}
	p := r.res.Ops[0].Params.(map[string]any)
	body := p["body"].(string)
	if p["title"] != "Build Plan It 1.1 (13) and upload it" || !strings.Contains(body, "upload-a-build") || !strings.Contains(body, "AuthKey_KEY1.p8") || strings.Contains(body, "PRIVATE KEY") {
		t.Fatalf("item %v", p)
	}
	var q any
	if r.res.State.GetProject(pid, "public.build_request", &q) && q != nil {
		t.Fatalf("passed a Mac build on: %v", q)
	}

	// Answered once: the next run files nothing more.
	var mine map[string]Answer
	r.res.State.GetProject(pid, "mac_requests", &mine)
	r2 := buildsRun(map[string]any{"builder": byMac})
	asked(r2)
	r2.req.State.PutProject(pid, "mac_requests", mine)
	r2.routeBuilds()
	if len(r2.res.Ops) != 0 {
		t.Fatalf("filed twice: %+v", r2.res.Ops)
	}

	// A build item in flight is reused for a new request.
	r3 := buildsRun(map[string]any{"builder": byMac})
	publish(r3, "testflight", "public.build_request", BuildRequest{ID: "testflight-2", At: plugintest.Now})
	r3.req.Work = []plugin.Work{{Key: "PLAN-20", ProjectID: pid, Tags: []string{"build"}, StageKind: "doing"}}
	r3.routeBuilds()
	var b Builder
	r3.res.State.GetProject(pid, "public.build", &b)
	if len(r3.res.Ops) != 0 || b.Requests["testflight-2"].Work != "PLAN-20" {
		t.Fatalf("ops %+v build %+v", r3.res.Ops, b)
	}
}
