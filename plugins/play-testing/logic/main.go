// Command logic is the Play testing plugin (id "play-testing"): Android's
// TestFlight. It follows an app's builds on Google Play's testing tracks
// (internal, closed and open), writes each new build's release notes from
// the work finished since the one before, promotes a build to the next
// track, and asks the product's Builds plugin for a new build.
//
// It never uploads: the app's own CI (GitHub Actions) builds the app bundle
// and uploads it to a Play track. Play testing asks for that build with
// "public.ci_request", naming the Enplace track it's for, and picks the
// build up once it's on Play.
//
// It builds on Google Play: the package and whether the Play Console is
// connected come from its "public.play", and the Play Console key is its
// key, shared. Each Enplace track says which Play track its builds go to
// (the per-track "play_track" setting). It shares its builds as
// "public.beta", the same shape as TestFlight's, and "public.builds" for
// Release cycle.
package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/JustinGuedes/enplace-plugins/plugins/google-play/play"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

func main() { plugin.Run(handle) }

// Build is one version code on Play's testing tracks.
type Build struct {
	Code    string    `json:"code"`
	Version string    `json:"version"`
	Tracks  []string  `json:"tracks"` // Play tracks it's live on, earliest first
	Draft   bool      `json:"draft,omitempty"`
	Notes   string    `json:"notes,omitempty"`
	Seen    time.Time `json:"seen"` // when Enplace first saw it on Play
}

// Status is what the last check found, stored per product.
type Status struct {
	CheckedAt time.Time `json:"checked_at"`
	Package   string    `json:"package,omitempty"`
	Connected bool      `json:"connected"`
	Builds    []Build   `json:"builds,omitempty"`
	Error     string    `json:"error,omitempty"`
	Notes     []string  `json:"notes,omitempty"`
}

const keyStatus = "status"

type run struct {
	req *plugin.Request
	res *plugin.Response
	pid string
}

func handle(req *plugin.Request) (*plugin.Response, error) {
	req.SignIn("google") // the Google Play account this product uses (ENP-351)
	r := &run{req: req, res: &plugin.Response{}, pid: req.ProjectID}
	if r.pid == "" {
		return r.res, nil
	}
	switch req.Hook {
	case plugin.HookSchedule:
		r.refresh()
	case plugin.HookAction:
		if req.Action != nil {
			r.action(req.Action)
		}
	case plugin.HookRender:
		for _, slot := range req.Slots {
			switch slot {
			case "project_card:play_testing":
				r.card(slot, r.overview())
			case plugin.SlotSettingsPanel:
				r.card(slot, r.panel())
			}
		}
	case plugin.HookPage:
		if req.Page == "play_testing" {
			r.res.Page = r.page()
		}
	}
	return r.res, nil
}

func (r *run) card(slot string, blocks []plugin.Block) {
	if r.res.Cards == nil {
		r.res.Cards = map[string][]plugin.Block{}
	}
	r.res.Cards[slot] = blocks
}

func (r *run) setting(k string) string {
	v, _ := r.req.ProjectSettings[r.pid][k].(string)
	return strings.TrimSpace(v)
}

func (r *run) on(k string) bool {
	v, ok := r.req.ProjectSettings[r.pid][k].(bool)
	return v || !ok
}

func (r *run) language() string { return orElse(r.setting("language"), "en-US") }

// peer reads what a linked plugin published for this product, or for its
// project when the plugin is on the project instead.
func (r *run) peer(id, key string, v any) bool {
	st, ok := r.req.Peers[id]
	if !ok {
		return false
	}
	if st.GetProject(r.pid, key, v) {
		return true
	}
	if parent, _, found := strings.Cut(r.pid, "/"); found {
		return st.GetProject(parent, key, v)
	}
	return false
}

// Store is what Google Play shares about the Play Console.
type Store struct {
	Package   string `json:"package"`
	Connected bool   `json:"connected"`
	Email     string `json:"email"`
	Error     string `json:"error"`
}

func (r *run) store() (Store, bool) {
	var s Store
	ok := r.peer("google-play", "public.play", &s)
	return s, ok
}

func (r *run) status() *Status {
	var s *Status
	r.req.State.GetProject(r.pid, keyStatus, &s)
	return s
}

func call(method, u string, headers map[string]string, body []byte) (int, []byte, error) {
	res, err := plugin.Fetch(plugin.FetchRequest{Method: method, URL: u, Headers: headers, Body: body})
	if err != nil {
		return 0, nil, err
	}
	return res.Status, res.Body, nil
}

func (r *run) client(pkg string) (*play.Client, error) {
	return play.New(call, r.req.Secrets["service_account"], pkg, r.req.Now)
}

// refresh reads the testing tracks, writes a new build's release notes,
// and publishes the builds.
func (r *run) refresh() {
	was := r.status()
	st, _ := r.store()
	s := &Status{CheckedAt: r.req.Now, Package: st.Package, Connected: st.Connected}
	defer func() {
		r.res.SetProject(r.pid, keyStatus, s)
		r.publish(s)
	}()
	if !s.Connected || s.Package == "" || r.req.Secrets["service_account"] == "" {
		s.keep(was)
		return
	}
	c, err := r.client(s.Package)
	var ts []play.Track
	if err == nil {
		ts, err = c.Tracks()
	}
	if err != nil {
		s.Error = err.Error()
		s.keep(was)
		return
	}
	s.Builds = builds(ts, seen(was), r.req.Now)
	if r.on("release_notes") {
		r.writeNotes(c, ts, s)
	}
}

func (s *Status) keep(was *Status) {
	if was != nil && was.Package == s.Package {
		s.Builds = was.Builds
	}
}

// seen is when each build was first seen, from the last check.
func seen(s *Status) map[string]time.Time {
	out := map[string]time.Time{}
	if s != nil {
		for _, b := range s.Builds {
			out[b.Code] = b.Seen
		}
	}
	return out
}

// order is where a Play track sits on the way to production.
func order(t string) int {
	switch t {
	case play.Internal:
		return 0
	case play.Closed:
		return 1
	case play.Open:
		return 3
	case play.Production:
		return 4
	}
	return 2 // a closed track of the person's own naming
}

// builds gathers every version code on the tracks, newest first.
func builds(ts []play.Track, was map[string]time.Time, now time.Time) []Build {
	by := map[string]*Build{}
	sort.SliceStable(ts, func(i, j int) bool { return order(ts[i].Track) < order(ts[j].Track) })
	for _, t := range ts {
		for _, rel := range t.Releases {
			code := rel.Code()
			if code == "" {
				continue
			}
			b := by[code]
			if b == nil {
				b = &Build{Code: code, Version: rel.Name, Seen: now, Draft: true}
				if at, ok := was[code]; ok && !at.IsZero() {
					b.Seen = at
				}
				by[code] = b
			}
			if rel.Status != play.Draft {
				b.Draft = false
				b.Tracks = append(b.Tracks, t.Track)
			}
			if b.Notes == "" {
				b.Notes = rel.NotesIn("")
			}
		}
	}
	out := []Build{}
	for _, b := range by {
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool { return codeNum(out[i].Code) > codeNum(out[j].Code) })
	if len(out) > 10 {
		out = out[:10]
	}
	return out
}

func codeNum(c string) int64 { n, _ := strconv.ParseInt(c, 10, 64); return n }

// latest is the newest build on a testing track, if any.
func latest(s *Status) (Build, bool) {
	if s != nil {
		for _, b := range s.Builds {
			if len(b.Tracks) > 0 || b.Draft {
				return b, true
			}
		}
	}
	return Build{}, false
}

// writeNotes fills in the newest build's release notes, while it's new and
// has none, from the work finished since the build before it.
func (r *run) writeNotes(c *play.Client, ts []play.Track, s *Status) {
	if len(s.Builds) == 0 {
		return
	}
	b := s.Builds[0]
	if b.Notes != "" || r.req.Now.Sub(b.Seen) > 7*24*time.Hour || len(b.Tracks) == 0 {
		return
	}
	since := b.Seen.Add(-14 * 24 * time.Hour)
	if len(s.Builds) > 1 {
		since = s.Builds[1].Seen
	}
	text := whatsNew(r.req.Work, r.pid, since, b.Seen)
	if text == "" {
		return
	}
	lang := r.language()
	err := c.Change(func(ts []play.Track, e *play.Edit) (bool, error) {
		t, _ := play.Find(ts, b.Tracks[0])
		for i, rel := range t.Releases {
			if rel.Code() == b.Code {
				t.Releases[i] = play.WithNotes(rel, lang, text)
				return true, e.SetTrack(t)
			}
		}
		return false, nil
	})
	if err != nil {
		s.Notes = append(s.Notes, "Couldn't write the release notes: "+err.Error())
		return
	}
	s.Builds[0].Notes = text
}

// whatsNew lists the work finished on the product between two times, in
// Play's 500 characters.
func whatsNew(work []plugin.Work, pid string, from, to time.Time) string {
	lines := []string{}
	n := 0
	for _, w := range work {
		if w.ProjectID != pid || w.StageKind != "done" || w.DoneAt.Before(from) || w.DoneAt.After(to) {
			continue
		}
		line := "• " + w.Title
		if n+len(line)+1 > 480 || len(lines) == 8 {
			break
		}
		lines = append(lines, line)
		n += len(line) + 1
	}
	return strings.Join(lines, "\n")
}

// publish shares the builds in the shape every beta plugin does (the
// server draws them on the Testing page), and for Release cycle.
func (r *run) publish(s *Status) {
	type pub struct {
		ID         string    `json:"id"`
		Version    string    `json:"version"`
		Number     string    `json:"number"`
		State      string    `json:"state"`
		StateText  string    `json:"state_text"`
		Uploaded   time.Time `json:"uploaded"`
		WhatToTest string    `json:"what_to_test,omitempty"`
		Groups     []string  `json:"groups,omitempty"`
	}
	out, old := []pub{}, []map[string]any{}
	for _, b := range s.Builds {
		p := pub{ID: b.Code, Version: b.Version, Number: b.Code, State: "ready", StateText: state(b), Uploaded: b.Seen, WhatToTest: b.Notes}
		for _, t := range b.Tracks {
			p.Groups = append(p.Groups, play.TrackName(t))
		}
		out = append(out, p)
		old = append(old, map[string]any{"version": b.Version, "build": b.Code, "state": p.State, "uploaded": b.Seen})
	}
	shared := map[string]any{
		"provider": "play-testing", "checked_at": s.CheckedAt, "setup_left": len(r.stepsLeft(s)),
		"builds": out, "testers": []any{}, "feedback": []any{},
	}
	if s.Package != "" {
		shared["url"] = "https://play.google.com/apps/testing/" + s.Package
	}
	if t, ok := r.track(); ok {
		shared["group"] = play.TrackName(playTrack(t))
	}
	if s.Error != "" {
		shared["error"] = s.Error
	}
	r.res.SetProject(r.pid, "public.beta", shared)
	r.res.SetProject(r.pid, "public.builds", old)
}

func (r *run) action(a *plugin.Action) {
	args := map[string]string{}
	_ = json.Unmarshal(a.Args, &args)
	switch a.Name {
	case "refresh":
		r.refresh()
		r.res.Toast = "Checked Google Play"
	case "send":
		r.send()
	case "promote":
		r.promote(args["code"], args["to"])
	case "tool_play_testing":
		r.res.Data = r.tool()
	}
}

// promote puts a build on the next track. Production gets it as a staged
// rollout to 10% of users, which Google Play then raises.
func (r *run) promote(code, to string) {
	st, _ := r.store()
	if code == "" || to == "" || !st.Connected {
		r.res.Toast = "Connect the Play Console in Google Play's settings first"
		return
	}
	c, err := r.client(st.Package)
	if err == nil {
		err = c.Change(func(ts []play.Track, e *play.Edit) (bool, error) {
			rel, ok := release(ts, code)
			if !ok {
				return false, fmt.Errorf("build %s isn't on a track any more", code)
			}
			return true, e.SetTrack(play.Promote(ts, rel, to, 0.1))
		})
	}
	if err != nil {
		r.res.Toast = "Couldn't promote it: " + err.Error()
		return
	}
	r.res.Log = "Promoted build " + code + " to " + play.TrackName(to) + " on Google Play"
	r.res.Toast = r.res.Log
	if to == play.Production {
		r.res.Toast = "Rolling out to 10% of users. Raise it on the Google Play page."
	}
	r.refresh()
}

// release finds a build's release on the track furthest along.
func release(ts []play.Track, code string) (play.Release, bool) {
	var best play.Release
	found, at := false, -1
	for _, t := range ts {
		for _, rel := range t.Releases {
			if rel.Code() == code && order(t.Track) > at {
				best, found, at = rel, true, order(t.Track)
			}
		}
	}
	return best, found
}

// tool is what agents get from get_play_testing.
func (r *run) tool() map[string]any {
	s := r.status()
	out := map[string]any{"tracks": r.trackMap()}
	if left := r.stepsLeft(s); len(left) > 0 {
		out["setup_left"] = left
	}
	if s != nil {
		out["package"], out["builds"], out["checked_at"] = s.Package, s.Builds, s.CheckedAt
		if s.Error != "" {
			out["error"] = s.Error
		}
	}
	if where, failed := r.building(s); where != "" || failed != "" {
		out["building"], out["build_failed"] = where, failed
	}
	return out
}

func orElse(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
