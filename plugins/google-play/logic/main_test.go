package main

import (
	"strings"
	"testing"
	"time"

	"github.com/JustinGuedes/enplace-plugins/plugins/google-play/play"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin/plugintest"
)

var now = time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

func request(hook string, settings map[string]any, st *Status) *plugin.Request {
	req := plugintest.Request(hook)
	req.ProjectID, req.Now = "p/android", now
	req.ProjectSettings = map[string]map[string]any{"p/android": settings}
	if st != nil {
		req.State.PutProject("p/android", keyStatus, st)
	}
	return req
}

// The setup ticks steps off as checks see them, and the panel folds to a
// line once connected.
func TestSetupStepsTickOff(t *testing.T) {
	r := &run{req: request(plugin.HookRender, map[string]any{"package_name": "com.demo"}, nil), pid: "p/android"}
	if left := r.stepsLeft(nil); len(left) != 3 || left[0] != "Switch on the Google Play APIs" {
		t.Fatalf("fresh: %v", left)
	}
	invited := &Status{Key: true, Email: "enplace@demo.iam.gserviceaccount.com", Reason: play.ReasonNotInvited}
	left := r.stepsLeft(invited)
	if len(left) != 1 || left[0] != "Invite it in the Play Console" {
		t.Fatalf("not invited: %v", left)
	}
	if steps := r.steps(invited); !strings.Contains(steps[3].How, invited.Email) {
		t.Errorf("the invite step doesn't name the email: %q", steps[3].How)
	}
	if left := r.stepsLeft(&Status{Key: true, Reason: play.ReasonAPIOff}); left[0] != "Switch on the Google Play APIs" {
		t.Errorf("api off: %v", left)
	}
	req := request(plugin.HookRender, map[string]any{"package_name": "com.demo"}, &Status{Key: true, Connected: true, Email: "e@x", CheckedAt: now})
	req.Slots = []string{plugin.SlotSettingsPanel}
	res := plugintest.Call(t, handle, req)
	if b := res.Cards[plugin.SlotSettingsPanel][0]; !strings.HasPrefix(b.Text, "Connected to the Play Console as e@x") {
		t.Fatalf("panel = %+v", b)
	}
}

func TestRolloutButtonsGoOneStepUp(t *testing.T) {
	bs := rolloutButtons(play.Release{Name: "0.7.1", Status: play.InProgress, UserFraction: 0.1})
	labels := []string{}
	for _, b := range bs {
		labels = append(labels, b.Label)
		if b.Confirm == "" {
			t.Errorf("%s changes production without asking", b.Label)
		}
	}
	if got := strings.Join(labels, ","); got != "Halt,Raise to 20%,Release to everyone" {
		t.Fatalf("buttons = %s", got)
	}
	if bs := rolloutButtons(play.Release{Status: play.Halted, UserFraction: 0.2}); len(bs) != 1 || bs[0].Label != "Resume" {
		t.Fatalf("halted = %+v", bs)
	}
}

// What's live comes from production once connected, else the typed version.
func TestPublishesWhatsLive(t *testing.T) {
	r := &run{req: request(plugin.HookSchedule, map[string]any{"play_version": "0.6.0"}, nil), res: &plugin.Response{}, pid: "p/android"}
	r.publish(&Status{})
	var v map[string]string
	if !r.res.State.GetProject("p/android", "public.version", &v) || v["version"] != "0.6.0" {
		t.Fatalf("typed: %v", v)
	}
	live := play.Release{Name: "0.7.1", VersionCodes: []string{"190"}, Status: play.InProgress, UserFraction: 0.2}
	r.res = &plugin.Response{}
	r.publish(&Status{Connected: true, Live: &live, Reviews: []play.Review{{Stars: 5}, {Stars: 4}}, Vitals: &play.Vitals{Crash: 0.004}})
	var store map[string]any
	r.res.State.GetProject("p/android", "public.store", &store)
	r.res.State.GetProject("p/android", "public.version", &v)
	if v["version"] != "0.7.1" || store["rating"] != 4.5 || store["live"].(map[string]any)["rollout"] != 0.2 {
		t.Fatalf("version %v, store %v", v, store)
	}
}

// A reply picked by its label in the form goes to that review.
func TestReplyFindsTheReviewByLabel(t *testing.T) {
	rv := play.Review{ID: "r1", Author: "Sam", Stars: 2, Text: "Crashes on the forge screen every time I open it"}
	if l := label(rv); !strings.HasPrefix(l, "★★☆☆☆ Sam: Crashes") || !strings.HasSuffix(l, "…") {
		t.Fatalf("label = %q", l)
	}
	r := &run{req: request(plugin.HookAction, nil, &Status{Reviews: []play.Review{rv}}), res: &plugin.Response{}, pid: "p/android"}
	r.reply(label(rv), "  ")
	if r.res.Toast != "Pick a review and write a reply" {
		t.Fatalf("empty reply: %q", r.res.Toast)
	}
}

func TestPageShowsSetupUntilConnected(t *testing.T) {
	req := request(plugin.HookPage, map[string]any{"package_name": "com.demo"}, nil)
	req.Page = "play"
	res := plugintest.Call(t, handle, req)
	if res.Page == nil || res.Page.Blocks[0].ID != "setup" {
		t.Fatalf("page = %+v", res.Page)
	}
	live := play.Release{Name: "0.7.1", VersionCodes: []string{"190"}, Status: play.InProgress, UserFraction: 0.2}
	req = request(plugin.HookPage, map[string]any{"package_name": "com.demo"}, &Status{Connected: true, Live: &live,
		Tracks: []Shelf{{Track: play.Internal, Release: play.Release{Name: "0.8.0", VersionCodes: []string{"214"}}}}})
	req.Page = "play"
	res = plugintest.Call(t, handle, req)
	ids := []string{}
	for _, b := range res.Page.Blocks {
		ids = append(ids, b.ID)
	}
	if got := strings.Join(ids, ","); !strings.HasPrefix(got, "production,tracks,vitals,reviews") {
		t.Fatalf("sections = %s", got)
	}
}

// Each step that asks for something goes straight to where it's typed: the
// package name and the Google Play account on the Settings tab (ENP-361).
func TestStepsGoToSettings(t *testing.T) {
	r := &run{req: request(plugin.HookRender, map[string]any{}, nil), pid: "p/android"}
	steps := r.steps(nil)
	if b := steps[0].Buttons; len(b) == 0 || b[0].Href != "settings#fields" {
		t.Errorf("package step buttons = %+v", b)
	}
	req := request(plugin.HookRender, map[string]any{}, nil)
	req.Slots = []string{plugin.SlotSettingsPanel}
	res := plugintest.Call(t, handle, req)
	blocks := res.Cards[plugin.SlotSettingsPanel]
	var add bool
	for _, b := range blocks[len(blocks)-1].Buttons {
		add = add || b.Href == "settings#account"
	}
	if !add {
		t.Errorf("no Add the account button: %+v", blocks[len(blocks)-1])
	}
}

// The key comes from the product's Google Play account (ENP-351).
func TestSignsInWithTheGoogleAccount(t *testing.T) {
	req := request(plugin.HookRender, map[string]any{"package_name": "com.demo"}, nil)
	req.Connections = map[string]plugin.Account{"google": {Values: map[string]string{"service_account": "{}"}}}
	r := &run{req: req, pid: "p/android"}
	req.SignIn("google")
	if r.stepsLeft(nil)[0] != "Switch on the Google Play APIs" {
		t.Fatalf("left: %v", r.stepsLeft(nil))
	}
	for _, s := range r.steps(nil) {
		if s.Title == "Add your Google Play account" && !s.Done {
			t.Fatal("the account's key isn't seen")
		}
	}
}
