package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin/plugintest"
)

// Everything the manifest says the plugin draws opens on a fresh workspace.
func TestSmoke(t *testing.T) { plugintest.Smoke(t, handle, "../manifest.json") }

// request is a content project (Monday 2 March 2026) with two channels and
// an app it promotes.
func request(hook string) *plugin.Request {
	req := plugintest.Request(hook)
	req.Plugin = plugin.Info{ID: "content", Version: "1.0.0"}
	p := &req.Projects[0]
	p.Products = []plugin.ProductInfo{
		{Key: "tiktok", Name: "TikTok", Type: "channel", Stage: "live"},
		{Key: "youtube", Name: "YouTube", Type: "channel", Stage: "live"},
		{Key: "site", Name: "Site", Type: "website", Stage: "live"},
	}
	p.Stages = []plugin.Stage{
		{Key: "ideas", Name: "Ideas", Kind: "later"}, {Key: "script", Name: "Script", Kind: "todo"},
		{Key: "edit", Name: "Edit", Kind: "doing"}, {Key: "ready", Name: "Ready", Kind: "review"},
		{Key: "posted", Name: "Posted", Kind: "done"},
	}
	req.Projects = append(req.Projects, plugin.Project{ID: "p2", Key: "TOC", Name: "Tocka", Colour: "#ffc069"})
	return req
}

func work(id, title, stage, kind, due string) plugin.Work {
	return plugin.Work{ID: id, Key: "ACM-" + id, ProjectID: plugintest.ProjectID, Title: title, Stage: stage, StageKind: kind, Due: due, Fields: map[string]string{}}
}

func state(t *testing.T, req *plugin.Request, key string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if req.State.Projects == nil {
		req.State.Projects = map[string]map[string]json.RawMessage{}
	}
	if req.State.Projects[plugintest.ProjectID] == nil {
		req.State.Projects[plugintest.ProjectID] = map[string]json.RawMessage{}
	}
	req.State.Projects[plugintest.ProjectID][key] = b
}

func TestParseDays(t *testing.T) {
	got := daysText(parseDays("fri, Monday and wed"))
	if got != "Mon Wed Fri" {
		t.Fatalf("got %q", got)
	}
	if daysText(parseDays("every day")) != "Every day" || len(parseDays("")) != 0 {
		t.Fatal("every day or empty")
	}
}

// A new piece posts to every channel until someone picks.
func TestNewPiecePostsEverywhere(t *testing.T) {
	req := request(plugin.HookEvent)
	w := work("1", "4 phones, 1 bomb", "script", "todo", "")
	req.Work = []plugin.Work{w}
	req.Event = &plugin.Event{Type: "work.created", Work: w}
	res := plugintest.Call(t, handle, req)
	if len(res.Ops) != 1 || !strings.Contains(string(mustJSON(res.Ops[0].Params)), "TikTok · YouTube") {
		t.Fatalf("ops: %s", mustJSON(res.Ops))
	}
}

// Taking a channel off changes where it posts and the label.
func TestToggleChannel(t *testing.T) {
	req := request(plugin.HookAction)
	req.Work = []plugin.Work{work("1", "Trailer", "edit", "doing", "2026-03-04")}
	req.Action = &plugin.Action{Name: "toggle", Args: raw("work", "1", "channel", "tiktok")}
	res := plugintest.Call(t, handle, req)
	if len(res.Ops) != 1 || !strings.Contains(string(mustJSON(res.Ops[0].Params)), `"content.post_to":"YouTube"`) {
		t.Fatalf("ops: %s", mustJSON(res.Ops))
	}
	var pieces map[string]*piece
	_ = json.Unmarshal(res.State.Projects[plugintest.ProjectID][keyPieces], &pieces)
	if p := pieces["1"]; p == nil || !p.Picked || len(p.To) != 1 || p.To[0] != "youtube" {
		t.Fatalf("pieces: %s", mustJSON(pieces))
	}
}

// With a link for every channel it posts to, the piece moves to done.
func TestLinksMoveToDone(t *testing.T) {
	req := request(plugin.HookAction)
	req.Work = []plugin.Work{work("1", "Trailer", "ready", "review", "2026-03-02")}
	state(t, req, keyPieces, map[string]piece{"1": {To: []string{"youtube"}, Picked: true}})
	req.Action = &plugin.Action{Name: "links", Args: raw("work", "1", "link:youtube", "https://youtu.be/x")}
	res := plugintest.Call(t, handle, req)
	if len(res.Ops) != 1 || res.Ops[0].Kind != "work.move" {
		t.Fatalf("ops: %s", mustJSON(res.Ops))
	}
	req.Action = &plugin.Action{Name: "links", Args: raw("work", "1", "link:youtube", "youtu.be/x")}
	if res := plugintest.Call(t, handle, req); !strings.Contains(res.Toast, "https://") {
		t.Fatalf("a link without https should be refused: %q", res.Toast)
	}
}

// The week card counts each channel's days, and an empty slot within two
// days makes it loud; Today's brief names the next post that isn't ready.
func TestWeekAndBrief(t *testing.T) {
	req := request(plugin.HookRender)
	state(t, req, keyRhythm, map[string][]time.Weekday{"tiktok": {time.Monday, time.Wednesday, time.Friday}})
	req.Work = []plugin.Work{
		work("1", "4 phones, 1 bomb", "edit", "doing", "2026-03-02"), // today, not ready
		work("2", "Dragon alarm", "ready", "review", "2026-03-06"),
	}
	req.Slots = []string{"project_card:week", "project_card:coming", plugin.SlotBrief, plugin.SlotDeadlines}
	res := plugintest.Call(t, handle, req)
	week := mustJSON(res.Cards["project_card:week"])
	if !strings.Contains(string(week), `"value":"2 of 3"`) || !strings.Contains(string(week), "Wednesday has nothing planned") {
		t.Fatalf("week: %s", week)
	}
	if res.Signals["project_card:week"].Level != plugin.LevelLoud {
		t.Fatalf("an empty Wednesday slot should be loud: %+v", res.Signals)
	}
	if res.Brief == nil || !strings.Contains(res.Brief.Text, "4 phones, 1 bomb posts today") || !strings.Contains(res.Brief.Text, "still in Edit") {
		t.Fatalf("brief: %+v", res.Brief)
	}
	if d, ok := res.Deadlines["1"]; !ok || !strings.Contains(d.Reason, "TikTok · YouTube") {
		t.Fatalf("deadlines: %+v", res.Deadlines)
	}
	if _, ok := res.Deadlines["2"]; ok {
		t.Fatal("a ready piece has no deadline")
	}
}

// Promotes offers the workspace's other projects; the apps card warns
// about one with nothing for three weeks.
func TestPromotes(t *testing.T) {
	req := request(plugin.HookRender)
	old := work("1", "Old trailer", "posted", "done", "2026-02-01")
	old.Fields["content.promotes"] = "p2"
	req.Work = []plugin.Work{old}
	req.Slots = []string{plugin.SlotWorkFields, "project_card:apps"}
	res := plugintest.Call(t, handle, req)
	if opts := res.Fields["promotes"].Options; len(opts) != 2 || opts[1].Label != "Tocka" {
		t.Fatalf("promotes: %+v", opts)
	}
	if !strings.Contains(string(mustJSON(res.Cards["project_card:apps"])), "Nothing in 3 weeks") {
		t.Fatalf("apps: %s", mustJSON(res.Cards["project_card:apps"]))
	}
}

// The panel's first line says when and where it posts, so a folded panel
// still says it.
func TestPanelFirstLine(t *testing.T) {
	req := request(plugin.HookRender)
	req.Work = []plugin.Work{work("1", "Trailer", "edit", "doing", "2026-03-04")}
	req.WorkID = "1"
	req.Slots = []string{plugin.SlotWorkPanel}
	res := plugintest.Call(t, handle, req)
	if got := string(mustJSON(res.Cards[plugin.SlotWorkPanel])); !strings.Contains(got, "Posts Wednesday · TikTok, YouTube") {
		t.Fatalf("panel: %s", got)
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
