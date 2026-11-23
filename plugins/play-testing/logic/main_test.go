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

func TestBuildsMergeTracksNewestFirst(t *testing.T) {
	ts := []play.Track{
		{Track: play.Closed, Releases: []play.Release{{Name: "0.8.0", VersionCodes: []string{"211"}, Status: play.Completed}}},
		{Track: play.Internal, Releases: []play.Release{
			{Name: "0.8.0", VersionCodes: []string{"214"}, Status: play.Completed, Notes: []play.Note{{Language: "en-US", Text: "Gear forge"}}},
		}},
		{Track: play.Production, Releases: []play.Release{{Name: "0.7.1", VersionCodes: []string{"190"}, Status: play.Completed}}},
		{Track: play.Open, Releases: []play.Release{{Name: "0.8.0", VersionCodes: []string{"211"}, Status: play.Draft}}},
	}
	earlier := now.Add(-48 * time.Hour)
	bs := builds(ts, map[string]time.Time{"211": earlier}, now)
	if len(bs) != 3 || bs[0].Code != "214" || bs[1].Code != "211" || bs[2].Code != "190" {
		t.Fatalf("builds = %+v", bs)
	}
	if bs[0].Notes != "Gear forge" || !bs[0].Seen.Equal(now) {
		t.Errorf("newest = %+v", bs[0])
	}
	if got := strings.Join(bs[1].Tracks, ","); got != "alpha" || !bs[1].Seen.Equal(earlier) || bs[1].Draft {
		t.Errorf("211 = %+v (a draft on open doesn't count as being on it)", bs[1])
	}
}

func TestPlayTrackFollowsSettingThenPurpose(t *testing.T) {
	for _, tc := range []struct {
		tr   plugin.Track
		want string
	}{
		{plugin.Track{Purpose: "internal"}, play.Internal},
		{plugin.Track{Purpose: "external"}, play.Closed},
		{plugin.Track{Purpose: "check"}, ""},
		{plugin.Track{Purpose: "internal", Settings: map[string]any{"play_track": "Open testing"}}, play.Open},
		{plugin.Track{Purpose: "external", Settings: map[string]any{"play_track": "friends"}}, "friends"},
	} {
		if got := playTrack(tc.tr); got != tc.want {
			t.Errorf("%+v: %q, want %q", tc.tr, got, tc.want)
		}
	}
}

func TestPromoteGoesOneTrackAlong(t *testing.T) {
	b := promoteButton(Build{Code: "214", Version: "0.8.0", Tracks: []string{play.Internal}})
	if b == nil || !strings.Contains(string(b.Args), `"to":"alpha"`) || b.Confirm == "" {
		t.Fatalf("internal: %+v", b)
	}
	b = promoteButton(Build{Code: "214", Version: "0.8.0", Tracks: []string{play.Internal, play.Open}})
	if b == nil || b.Label != "Release" || !strings.Contains(b.Confirm, "10%") {
		t.Fatalf("open: %+v", b)
	}
	if promoteButton(Build{Code: "190", Tracks: []string{play.Production}}) != nil {
		t.Fatal("production has nowhere to go")
	}
}

func TestWhatsNewFitsPlaysLimit(t *testing.T) {
	work := []plugin.Work{}
	for i := 0; i < 30; i++ {
		work = append(work, plugin.Work{ProjectID: "p/android", StageKind: "done", DoneAt: now.Add(-time.Hour), Title: strings.Repeat("x", 70)})
	}
	work = append(work, plugin.Work{ProjectID: "p/ios", StageKind: "done", DoneAt: now.Add(-time.Hour), Title: "iOS only"})
	got := whatsNew(work, "p/android", now.Add(-24*time.Hour), now)
	if len(got) > 500 || strings.Contains(got, "iOS only") || got == "" {
		t.Fatalf("notes (%d chars) = %q", len(got), got)
	}
}

// Send to testers asks the CI for a build on the testers' track, once.
func TestSendAsksTheBuildsPlugin(t *testing.T) {
	req := plugintest.Request(plugin.HookAction)
	req.ProjectID, req.Now = "p/android", now
	req.Action = &plugin.Action{Name: "send"}
	req.Tracks = map[string][]plugin.Track{"p/android": {
		{Key: "main", Purpose: "check", On: true},
		{Key: "beta", Name: "Internal beta", Purpose: "internal", Makes: true, On: true},
	}}
	req.Peers = map[string]plugin.State{}
	gp, ci := plugin.State{}, plugin.State{}
	gp.PutProject("p/android", "public.play", map[string]any{"package": "com.demo", "connected": true})
	ci.PutProject("p", "public.ci", map[string]any{"name": "GitHub Actions", "ready": true})
	req.Peers["google-play"], req.Peers["github-actions"] = gp, ci
	res := plugintest.Call(t, handle, req)
	var q map[string]any
	if res.State == nil || !res.State.GetProject("p/android", "public.ci_request", &q) || q["track"] != "beta" {
		t.Fatalf("request = %v, toast %q", q, res.Toast)
	}
	if !strings.Contains(res.Toast, "GitHub Actions") || !strings.Contains(res.Toast, "Internal testing") {
		t.Errorf("toast = %q", res.Toast)
	}
	// Asked again while it's building: nothing new is asked.
	req.State.PutProject("p/android", "public.ci_request", q)
	req.Now = now.Add(10 * time.Minute)
	again := plugintest.Call(t, handle, req)
	if again.State != nil && again.State.GetProject("p/android", "public.ci_request", &q) {
		t.Fatalf("asked twice: %v", q)
	}
}

// Without the Play Console connected the setup says so and nothing is read.
func TestSetupWaitsForGooglePlay(t *testing.T) {
	req := plugintest.Request(plugin.HookRender)
	req.ProjectID, req.Slots = "p/android", []string{plugin.SlotSettingsPanel}
	res := plugintest.Call(t, handle, req)
	list := res.Cards[plugin.SlotSettingsPanel][1]
	if list.Items[0].Done || !strings.Contains(list.Items[0].Sub, "Google Play page") {
		t.Fatalf("first step = %+v", list.Items[0])
	}
}

// Builds are shared in the shape the Testing page reads (public.beta).
func TestPublishesTheSharedBetaShape(t *testing.T) {
	req := plugintest.Request(plugin.HookSchedule)
	req.ProjectID, req.Now = "p/android", now
	r := &run{req: req, res: &plugin.Response{}, pid: "p/android"}
	r.publish(&Status{CheckedAt: now, Package: "com.demo", Builds: []Build{
		{Code: "214", Version: "0.8.0", Tracks: []string{play.Internal, play.Closed}, Notes: "• Forge", Seen: now},
		{Code: "215", Version: "0.8.1", Draft: true, Seen: now},
	}})
	var got struct {
		Provider  string `json:"provider"`
		URL       string `json:"url"`
		SetupLeft int    `json:"setup_left"`
		Builds    []struct {
			Number    string   `json:"number"`
			State     string   `json:"state"`
			StateText string   `json:"state_text"`
			Groups    []string `json:"groups"`
		} `json:"builds"`
	}
	if !r.res.State.GetProject("p/android", "public.beta", &got) {
		t.Fatal("nothing shared")
	}
	b := got.Builds[0]
	if got.Provider != "play-testing" || got.URL != "https://play.google.com/apps/testing/com.demo" || got.SetupLeft == 0 ||
		b.Number != "214" || b.State != "ready" || len(b.Groups) != 2 || got.Builds[1].StateText != "Draft on Play" {
		t.Fatalf("shared = %+v", got)
	}
}
