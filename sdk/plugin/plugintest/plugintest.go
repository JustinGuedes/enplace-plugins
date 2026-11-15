// Package plugintest runs plugin logic in plain go test, the way Enplace
// calls it, without Enplace.
//
//	func TestDraws(t *testing.T) {
//		plugintest.Smoke(t, handle, "../manifest.json")
//
//		req := plugintest.Request(plugin.HookRender)
//		req.Slots = []string{plugin.SlotBrief}
//		res := plugintest.Call(t, handle, req)
//		...
//	}
package plugintest

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/bundle"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

// Now is the clock every Request starts with: Monday 2 March 2026, 09:00 UTC.
var Now = time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)

// ProjectID is the one project a Request starts with, switched on.
const ProjectID = "p1"

// Request is a request for hook as Enplace sends it: a fixed clock, a
// workspace and one project ("ACM") with the plugin on, and no state.
// Change whatever the test is about.
func Request(hook string) *plugin.Request {
	return &plugin.Request{
		Hook:      hook,
		Now:       Now,
		Date:      Now.Format(time.DateOnly),
		Timezone:  "UTC",
		WeekStart: 1,
		Workspace: plugin.Workspace{ID: "w1", Name: "Acme"},
		Plugin:    plugin.Info{ID: "test", Version: "1.0.0"},
		ProjectID: ProjectID,
		Settings:  map[string]any{},
		State:     plugin.State{Workspace: map[string]json.RawMessage{}},
		Projects: []plugin.Project{{
			ID: ProjectID, Key: "ACM", Name: "Acme", Colour: "#ffc069", On: true,
			Stages: []plugin.Stage{
				{Key: "todo", Name: "To do", Kind: "todo"},
				{Key: "doing", Name: "Doing", Kind: "doing"},
				{Key: "review", Name: "In review", Kind: "review"},
				{Key: "done", Name: "Done", Kind: "done"},
			},
		}},
	}
}

// Call runs h on req as Enplace does: the request and the response cross
// as JSON, so a field that doesn't survive the trip fails here as it would
// there. An error or a panic fails the test.
func Call(t testing.TB, h plugin.Handler, req *plugin.Request) *plugin.Response {
	t.Helper()
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("encoding the request: %v", err)
	}
	var in plugin.Request
	if err := json.Unmarshal(raw, &in); err != nil {
		t.Fatalf("decoding the request: %v", err)
	}
	res, err := call(h, &in)
	if err != nil {
		t.Fatalf("%s hook: %v", req.Hook, err)
	}
	if res == nil {
		return &plugin.Response{}
	}
	out, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("encoding the response: %v", err)
	}
	var back plugin.Response
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("decoding the response: %v", err)
	}
	return &back
}

// call runs h, turning a panic into an error: in Enplace a panic is a
// failed call too.
func call(h plugin.Handler, req *plugin.Request) (res *plugin.Response, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v", p)
		}
	}()
	return h(req)
}

// Smoke draws everything the plugin's manifest says it contributes on a
// fresh workspace: each render slot, page and work view. Logic that errors
// or panics on a first visit fails the test.
func Smoke(t *testing.T, h plugin.Handler, manifestPath string) {
	t.Helper()
	m := Manifest(t, manifestPath)
	c := m.Contributes
	var slots []string
	add := func(on bool, slot string) {
		if on {
			slots = append(slots, slot)
		}
	}
	add(c.Brief, plugin.SlotBrief)
	add(c.RailChip, plugin.SlotRailChip)
	add(c.SettingsPanel, plugin.SlotSettingsPanel)
	add(c.WorkPanel, plugin.SlotWorkPanel)
	add(len(c.WorkFields) > 0, plugin.SlotWorkFields)
	add(c.Deadlines, plugin.SlotDeadlines)
	for _, card := range c.Dashboard {
		slots = append(slots, "card:"+card.Key)
	}
	for _, card := range c.ProjectCards {
		slots = append(slots, "project_card:"+card.Key)
	}
	for _, slot := range slots {
		t.Run("render "+slot, func(t *testing.T) {
			req := Request(plugin.HookRender)
			req.Plugin = plugin.Info{ID: m.ID, Version: m.Version}
			req.Slots = []string{slot}
			if slot == plugin.SlotWorkPanel {
				req.WorkID, req.WorkKey = "w1", "ACM-1"
			}
			Call(t, h, req)
		})
	}
	for _, page := range c.Pages {
		t.Run("page "+page.Key, func(t *testing.T) {
			req := Request(plugin.HookPage)
			req.Plugin = plugin.Info{ID: m.ID, Version: m.Version}
			req.Page = page.Key
			if page.Where == "workspace" {
				req.ProjectID = ""
			}
			Call(t, h, req)
		})
	}
	for _, view := range c.WorkViews {
		t.Run("view "+view.Key, func(t *testing.T) {
			req := Request(plugin.HookView)
			req.Plugin = plugin.Info{ID: m.ID, Version: m.Version}
			req.View = view.Key
			Call(t, h, req)
		})
	}
}

// Manifest reads and checks a plugin's manifest.json.
func Manifest(t testing.TB, path string) *bundle.Manifest {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	m, err := bundle.ParseManifest(raw)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return m
}
