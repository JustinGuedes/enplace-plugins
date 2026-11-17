package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/JustinGuedes/enplace-plugins/plugins/versions/versionfile"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin/plugintest"
)

const pid = plugintest.ProjectID

func request(hook string, settings map[string]any) *plugin.Request {
	req := plugintest.Request(hook)
	req.Plugin.ID = "versions"
	req.ProjectSettings = map[string]map[string]any{pid: settings}
	req.Peers = map[string]plugin.State{}
	return req
}

// publish puts what a linked plugin published on the request.
func publish(req *plugin.Request, plugin_, key string, v any) {
	st, ok := req.Peers[plugin_]
	if !ok {
		st = plugin.State{}
	}
	st.PutProject(pid, key, v)
	req.Peers[plugin_] = st
}

// carry keeps what a response stored, for the next request.
func carry(req *plugin.Request, res *plugin.Response) {
	if res.State == nil {
		return
	}
	for k, v := range res.State.Projects[pid] {
		if req.State.Projects == nil {
			req.State.Projects = map[string]map[string]json.RawMessage{}
		}
		if req.State.Projects[pid] == nil {
			req.State.Projects[pid] = map[string]json.RawMessage{}
		}
		req.State.Projects[pid][k] = v
	}
}

func got[T any](t *testing.T, res *plugin.Response, key string) T {
	t.Helper()
	var v T
	if res.State == nil || !res.State.GetProject(pid, key, &v) {
		t.Fatalf("nothing stored as %s", key)
	}
	return v
}

const tocka = "// The app's version.\nMARKETING_VERSION = 0.3.0\nCURRENT_PROJECT_VERSION = 42\n"

func files(text string) Files {
	return Files{SHA: "abc", Matched: []string{"Version.xcconfig", "Tocka.xcodeproj/project.pbxproj"},
		Files: []File{{Path: "Version.xcconfig", Text: text}, {Path: "Tocka.xcodeproj/project.pbxproj", Text: "MARKETING_VERSION = \"$(MARKETING_VERSION)\";"}}}
}

func TestReadsTheVersionAndAsksForFiles(t *testing.T) {
	req := request(plugin.HookSchedule, nil)
	res := plugintest.Call(t, handle, req)
	if w := got[Want](t, res, keyWant); len(w.Patterns) == 0 || w.Read != readFiles || len(w.Paths) != 0 {
		t.Fatalf("want %+v", w)
	}
	if f := got[Found](t, res, keyFound); f.Version != "" || !strings.Contains(f.Note, "GitHub") {
		t.Fatalf("with no code plugin: %+v", f)
	}

	publish(req, "github", keyFiles, files(tocka))
	res = plugintest.Call(t, handle, req)
	f := got[Found](t, res, keyFound)
	if f.Version != "0.3.0" || f.Build != "42" || f.File != "Version.xcconfig" || !f.Auto {
		t.Fatalf("found %+v", f)
	}
	if p := got[Published](t, res, keyPublished); p.Version != "0.3.0" || p.Build != "42" || p.Style != versionfile.Short || p.Bump != versionfile.Minor {
		t.Fatalf("published %+v", p)
	}
}

func TestOldSettingsCarryOver(t *testing.T) {
	req := request(plugin.HookSchedule, map[string]any{"style": "Short (1.4)", "bump": "Minor"})
	publish(req, "github", keyCode, codeInfo{Repo: "a/b", VersionFile: "Version.xcconfig"})
	publish(req, "github", keyFiles, Files{SHA: "abc", Files: []File{{Path: "Version.xcconfig", Text: tocka}}})
	publish(req, "releases", keyNumbering, numbering{Style: versionfile.Semantic, Bump: versionfile.Patch})
	res := plugintest.Call(t, handle, req)
	if w := got[Want](t, res, keyWant); len(w.Paths) != 1 || w.Paths[0] != "Version.xcconfig" {
		t.Fatalf("GitHub's old file setting should be read: %+v", w)
	}
	if f := got[Found](t, res, keyFound); f.Version != "0.3.0" || f.Auto {
		t.Fatalf("found %+v", f)
	}
	if p := got[Published](t, res, keyPublished); p.Style != versionfile.Semantic || p.Bump != versionfile.Patch {
		t.Fatalf("Release cycle's numbering should carry over: %+v", p)
	}

	// Set here, it's this plugin's from then on.
	req.ProjectSettings[pid]["bump"] = "Major"
	res = plugintest.Call(t, handle, req)
	carry(req, res)
	req.ProjectSettings[pid]["bump"] = "Minor"
	res = plugintest.Call(t, handle, req)
	if p := got[Published](t, res, keyPublished); p.Style != versionfile.Short || p.Bump != versionfile.Minor {
		t.Fatalf("own numbering: %+v", p)
	}
}

func TestBumpsTheCodeWhenAReleaseIsCut(t *testing.T) {
	req := request(plugin.HookSchedule, nil)
	publish(req, "github", keyFiles, files(tocka))
	rels := []release{{ID: "r1", Version: "0.4.0", Cutoff: "2026-03-05", ShipDate: "2026-03-06", State: "planned"}}
	publish(req, "releases", keyReleases, rels)
	res := plugintest.Call(t, handle, req)
	carry(req, res)
	if b := got[*Bump](t, res, keyBumpOut); b != nil {
		t.Fatalf("bumped before the cut-off: %+v", b)
	}

	req.Now, req.Date = req.Now.AddDate(0, 0, 3), "2026-03-05"
	res = plugintest.Call(t, handle, req)
	carry(req, res)
	b := got[*Bump](t, res, keyBumpOut)
	if b == nil || b.Version != "0.4.0" || b.Build != "43" || b.Release != "r1" || b.Branch != "enplace/version-0.4.0-43" {
		t.Fatalf("bump %+v", b)
	}
	text, ok := versionfile.Apply(tocka, b.Edits)
	if !ok || !strings.Contains(text, "MARKETING_VERSION = 0.4.0\nCURRENT_PROJECT_VERSION = 43") {
		t.Fatalf("edits make %q", text)
	}

	// GitHub opened it: the card says so and offers no second bump.
	publish(req, "github", keyBumpsIn, map[string]Bumped{b.ID: {State: "opened", Number: 12, URL: "https://github.com/a/b/pull/12"}})
	r := request(plugin.HookRender, nil)
	r.Peers, r.State, r.Slots = req.Peers, req.State, []string{cardSlot}
	card := plugintest.Call(t, handle, r).Cards[cardSlot]
	if !strings.Contains(card[1].Text, "#12") || card[len(card)-1].Buttons[0].URL == "" || len(card[len(card)-1].Buttons) != 1 {
		t.Fatalf("card %+v", card)
	}

	// Merged: the code says 0.4.0, the bump is done and isn't asked again.
	publish(req, "github", keyFiles, files(strings.ReplaceAll(strings.ReplaceAll(tocka, "0.3.0", "0.4.0"), "42", "43")))
	res = plugintest.Call(t, handle, req)
	carry(req, res)
	if b := got[*Bump](t, res, keyBumpOut); b != nil {
		t.Fatalf("still bumping after the merge: %+v", b)
	}
	publish(req, "github", keyFiles, files(tocka)) // even if the code went back
	if b := got[*Bump](t, plugintest.Call(t, handle, req), keyBumpOut); b != nil {
		t.Fatalf("bumped one release twice: %+v", b)
	}
}

func TestBumpButton(t *testing.T) {
	req := request(plugin.HookAction, map[string]any{"style": "Semantic (1.4.0)", "bump": "Patch", "bump_code": bumpOnPress})
	req.Action = &plugin.Action{Name: "bump"}
	res := plugintest.Call(t, handle, req)
	if !strings.Contains(res.Toast, "can't bump") {
		t.Fatalf("toast %q", res.Toast)
	}
	publish(req, "github", keyFiles, files(tocka))
	res = plugintest.Call(t, handle, req)
	carry(req, res)
	b := got[*Bump](t, res, keyBumpOut)
	if b == nil || b.Version != "0.3.1" || b.Build != "43" || res.Log == "" {
		t.Fatalf("bump %+v, %+v", b, res)
	}
	// A second press while GitHub works on it asks for nothing new.
	res = plugintest.Call(t, handle, req)
	if res.State != nil && res.State.Projects[pid][keyBumpOut] != nil {
		if b2 := got[*Bump](t, res, keyBumpOut); b2 != nil && b2.ID != b.ID {
			t.Fatalf("asked twice: %+v", b2)
		}
	}
	// It failed: the card says why and offers to try again.
	publish(req, "github", keyBumpsIn, map[string]Bumped{b.ID: {State: "failed", Error: "The token can't write to this repo."}})
	r := request(plugin.HookRender, nil)
	r.ProjectSettings, r.Peers, r.State, r.Slots = req.ProjectSettings, req.Peers, req.State, []string{cardSlot, plugin.SlotSettingsPanel}
	out := plugintest.Call(t, handle, r)
	card := out.Cards[cardSlot]
	if card[1].Tone != "warn" || card[len(card)-1].Buttons[0].Action != "bump" || out.Signals[cardSlot].Level != plugin.LevelNormal {
		t.Fatalf("card %+v", card)
	}

	tool := request(plugin.HookAction, nil)
	tool.Action, tool.State, tool.Peers = &plugin.Action{Name: "tool_version"}, req.State, req.Peers
	data, _ := json.Marshal(plugintest.Call(t, handle, tool).Data)
	if !strings.Contains(string(data), `"version":"0.3.0"`) || !strings.Contains(string(data), "failed") {
		t.Fatalf("tool %s", data)
	}
}
