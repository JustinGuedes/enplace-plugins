//go:build !wasip1

package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/JustinGuedes/enplace-plugins/plugins/testflight/beta"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin/plugintest"
)

func testBuildOf(t *testing.T, res *plugin.Response) plugin.TestBuild {
	t.Helper()
	var tb plugin.TestBuild
	if res.State == nil || !res.State.GetProject(pid, plugin.KeyTestBuild, &tb) {
		t.Fatal("no test build published")
	}
	return tb
}

// A finished round asks for one build, and follows it until it's on the
// phone, with the round's list as its What to test.
func TestRoundBuildFollowsTheBuildToThePhone(t *testing.T) {
	req := request(t, plugin.HookAction, nil)
	readyStatus(req, beta.Build{ID: "b12", Version: "1.0", Number: "12"})
	ask, _ := json.Marshal(plugin.TestBuildAsk{ID: "round-3", Round: 3, Term: "Round", WhatToTest: "Round 3: 2 changes"})
	req.Action = &plugin.Action{Name: "test_build", Args: ask}

	// Builds come from Xcode by hand: it can't, and says why.
	res := plugintest.Call(t, handle, req)
	if tb := testBuildOf(t, res); tb.State != plugin.TestFailed || !strings.Contains(tb.Note, "Archive") || tb.Round != 3 {
		t.Fatalf("by hand: %+v", tb)
	}

	publish(req, "app", "public.build", map[string]any{"id": "xcode-cloud", "name": "Xcode Cloud", "ready": true})
	res = plugintest.Call(t, handle, req)
	var q map[string]any
	if !res.State.GetProject(pid, "public.build_request", &q) || q["build"] != "13" || !strings.Contains(q["reason"].(string), "Round 3") {
		t.Fatalf("request %v", q)
	}
	if tb := testBuildOf(t, res); tb.State != plugin.TestBuilding || tb.ID != "round-3" || tb.Name != "1.0 (13)" {
		t.Fatalf("asked: %+v", tb)
	}
	keep(req, res)

	r := &run{req: req, res: &plugin.Response{}, pid: pid}
	if got := r.roundText("13"); got != "Round 3: 2 changes" {
		t.Fatalf("what to test %q", got)
	}

	// Apple has it, still processing; then it's ready.
	up := plugintest.Now.Add(20 * time.Minute)
	r.trackTestBuild(&Status{AppID: "900", Builds: []beta.Build{{Version: "1.0", Number: "13", Uploaded: up, State: beta.Processing}}})
	if tb := testBuildOf(t, r.res); tb.State != plugin.TestBuilding || tb.Note != "Apple is processing it." {
		t.Fatalf("processing: %+v", tb)
	}
	r.res = &plugin.Response{}
	r.trackTestBuild(&Status{AppID: "900", Builds: []beta.Build{{Version: "1.0", Number: "13", Uploaded: up, State: beta.Ready}}})
	tb := testBuildOf(t, r.res)
	if tb.State != plugin.TestReady || tb.Link != "https://beta.itunes.apple.com/v1/app/900" || tb.ReadyAt.IsZero() {
		t.Fatalf("ready: %+v", tb)
	}

	// A build Apple turned down fails, with why.
	r.res = &plugin.Response{}
	r.trackTestBuild(&Status{AppID: "900", Builds: []beta.Build{{Version: "1.0", Number: "13", Uploaded: up, State: beta.Failed}}})
	if tb := testBuildOf(t, r.res); tb.State != plugin.TestFailed || tb.Note == "" {
		t.Fatalf("failed: %+v", tb)
	}
}

// A round that ends while a build is on its way rides on that build.
func TestRoundBuildJoinsABuildOnItsWay(t *testing.T) {
	req := request(t, plugin.HookAction, nil)
	readyStatus(req, beta.Build{ID: "b12", Version: "1.0", Number: "12"})
	publish(req, "app", "public.build", map[string]any{"id": "xcode-cloud", "name": "Xcode Cloud", "ready": true})
	req.State.PutProject(pid, "public.build_request", map[string]any{"id": "testflight-1", "at": plugintest.Now.Add(-10 * time.Minute), "for": "13"})
	ask, _ := json.Marshal(plugin.TestBuildAsk{ID: "round-4", Round: 4, Term: "Round"})
	req.Action = &plugin.Action{Name: "test_build", Args: ask}
	res := plugintest.Call(t, handle, req)
	var q map[string]any
	if res.State.GetProject(pid, "public.build_request", &q) {
		t.Fatalf("asked again: %v", q)
	}
	if tb := testBuildOf(t, res); tb.State != plugin.TestBuilding || tb.Round != 4 {
		t.Fatalf("joined: %+v", tb)
	}
}
