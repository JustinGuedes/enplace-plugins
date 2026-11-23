//go:build !wasip1

package main

import (
	"strings"
	"testing"
	"time"

	"github.com/JustinGuedes/enplace-plugins/plugins/testflight/beta"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin/plugintest"
)

func setUp() *Status {
	return &Status{CheckedAt: plugintest.Now, Keys: true, BundleID: "com.guedio.planit", AppID: "900", IDReady: true,
		Group:   &beta.Group{ID: "g1", Name: "Team", AllBuild: true},
		Testers: []beta.Tester{{ID: "t1", Email: "justin@example.com", Name: "Justin", State: "INSTALLED"}},
		Builds:  []beta.Build{{ID: "b12", Version: "1.0", Number: "12", State: beta.Ready, Uploaded: plugintest.Now.Add(-time.Hour)}}}
}

func panelFor(t *testing.T, s *Status) []plugin.Block {
	req := request(t, plugin.HookRender, map[string]any{"testers": "justin@example.com", "group": "Team"})
	req.State.PutProject(pid, keyStatus, s)
	req.Slots = []string{plugin.SlotSettingsPanel}
	return plugintest.Call(t, handle, req).Cards[plugin.SlotSettingsPanel]
}

func TestFinishedSetupFoldsAway(t *testing.T) {
	p := panelFor(t, setUp())
	if len(p) != 1 || p[0].Type != plugin.BlockSection || !p[0].Collapsed || p[0].Text != "TestFlight is set up" {
		t.Fatalf("panel %+v", p)
	}
	if steps := p[0].Blocks[0].Items; len(steps) != 7 || !steps[6].Done {
		t.Fatalf("steps inside %+v", steps)
	}
}

func TestABrokenStepComesBackAlone(t *testing.T) {
	s := setUp()
	s.Error = "App Store Connect refused the key (401)"
	p := panelFor(t, s)
	if p[0].Tone != "warn" || !strings.Contains(p[0].Text, "stopped working: add your App Store Connect key") {
		t.Fatalf("lead %+v", p[0])
	}
	if items := p[1].Items; len(items) != 1 || items[0].Title != "2. Add your App Store Connect key" {
		t.Fatalf("only the broken step %+v", items)
	}
	if !strings.Contains(p[2].Text, "other 6 steps") {
		t.Fatalf("rest %+v", p[2])
	}
}

func TestBuildsAreSharedWithUsageReadHourly(t *testing.T) {
	now := plugintest.Now
	f := fake(t, map[string]string{
		"GET /v1/apps":                                       `{"data":[{"type":"apps","id":"900"}]}`,
		"GET /v1/apps/900/betaGroups":                        `{"data":[{"id":"g1","attributes":{"name":"Team","isInternalGroup":true,"hasAccessToAllBuilds":true}}]}`,
		"GET /v1/betaGroups/g1/betaTesters":                  `{"data":[{"id":"t1","attributes":{"email":"justin@example.com","firstName":"Justin","state":"INSTALLED"}}]}`,
		"GET /v1/builds":                                     build("b12", "12", "VALID", "IN_BETA_TESTING", now.Add(-time.Hour), "Try the map"),
		"GET /v1/builds/b12/metrics/betaBuildUsages":         `{"data":[{"dataPoints":[{"values":{"installCount":2,"sessionCount":9,"crashCount":1,"feedbackCount":1,"inviteCount":3}}]}]}`,
		"GET /v1/apps/900/metrics/betaTesterUsages":          `{"data":[{"dataPoints":[{"values":{"sessionCount":9,"crashCount":1}}],"dimensions":{"betaTesters":{"data":{"type":"betaTesters","id":"t1"}}}}]}`,
		"GET /v1/apps/900/betaFeedbackScreenshotSubmissions": `{"data":[{"id":"f1","attributes":{"createdDate":"` + now.Add(-30*time.Minute).Format(time.RFC3339) + `","comment":"Price shows 0","email":"justin@example.com","deviceModel":"iPhone16,1","osVersion":"26.1","screenshots":[{"url":"https://example.com/s.png"}]},"relationships":{"build":{"data":{"type":"builds","id":"b12"}}}}]}`,
		"GET /v1/apps/900/betaFeedbackCrashSubmissions":      `{"data":[]}`,
	})
	req := request(t, plugin.HookSchedule, map[string]any{"testers": "justin@example.com", "group": "Team"})
	res := plugintest.Call(t, handle, req)
	var sh Shared
	if !res.State.GetProject(pid, "public.beta", &sh) || len(sh.Builds) != 1 {
		t.Fatalf("public.beta %+v (sent %v)", sh, f.sent)
	}
	b := sh.Builds[0]
	if b.Number != "12" || b.Version != "1.0" || b.State != "ready" || b.WhatToTest != "Try the map" || b.Installs == nil || *b.Installs != 2 || *b.Sessions != 9 || *b.Crashes != 1 || len(b.Groups) != 1 || b.Groups[0] != "Team" {
		t.Fatalf("build %+v", b)
	}
	if len(sh.Testers) != 1 || sh.Testers[0].State != "installed" || sh.Testers[0].Sessions == nil || *sh.Testers[0].Sessions != 9 {
		t.Fatalf("testers %+v", sh.Testers)
	}
	if len(sh.Feedback) != 1 || sh.Feedback[0].From != "Justin" || sh.Feedback[0].Build != "12" || sh.Feedback[0].Image == "" {
		t.Fatalf("feedback %+v", sh.Feedback)
	}

	// Ten minutes on, the scheduled check leaves usage alone but keeps it.
	keep(req, res)
	req.Now = now.Add(10 * time.Minute)
	f.sent = nil
	res = plugintest.Call(t, handle, req)
	for _, k := range f.sent {
		if strings.Contains(k, "metrics") || strings.Contains(k, "Feedback") {
			t.Fatalf("read usage again within the hour: %v", f.sent)
		}
	}
	res.State.GetProject(pid, "public.beta", &sh)
	if sh.Builds[0].Installs == nil || *sh.Builds[0].Installs != 2 || len(sh.Feedback) != 1 {
		t.Fatalf("lost usage between reads %+v", sh)
	}

	// Check now reads it straight away.
	keep(req, res)
	f.sent = nil
	req.Hook, req.Action = plugin.HookAction, &plugin.Action{Name: "refresh"}
	plugintest.Call(t, handle, req)
	if !strings.Contains(strings.Join(f.sent, " "), "betaBuildUsages") {
		t.Fatalf("Check now didn't read usage: %v", f.sent)
	}
}

func TestEditWhatToTestAndStopABuild(t *testing.T) {
	f := fake(t, map[string]string{
		"PATCH /v1/betaBuildLocalizations/Lb12": `{}`,
		"PATCH /v1/builds/b12":                  `{}`,
	})
	req := request(t, plugin.HookAction, nil)
	s := setUp()
	s.Builds[0].NotesID = "Lb12"
	req.State.PutProject(pid, keyStatus, s)
	req.Action = &plugin.Action{Name: "what_to_test", Args: []byte(`{"build":"b12","text":"Try the merchant"}`)}
	if res := plugintest.Call(t, handle, req); res.Toast != "Saved What to test" {
		t.Fatalf("toast %q (sent %v)", res.Toast, f.sent)
	}
	if !strings.Contains(f.bodies["PATCH /v1/betaBuildLocalizations/Lb12"], "Try the merchant") {
		t.Fatalf("notes %v", f.bodies)
	}
	req.Action = &plugin.Action{Name: "expire", Args: []byte(`{"build":"b12"}`)}
	plugintest.Call(t, handle, req)
	if !strings.Contains(f.bodies["PATCH /v1/builds/b12"], `"expired":true`) {
		t.Fatalf("expire %v", f.bodies)
	}
}
