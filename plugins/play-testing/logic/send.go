package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/JustinGuedes/enplace-plugins/plugins/google-play/play"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

// playTrack is the Play track an Enplace track's builds go to: its
// play_track setting, else one that fits its purpose.
func playTrack(t plugin.Track) string {
	v, _ := t.Settings["play_track"].(string)
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "internal testing", "internal":
		return play.Internal
	case "closed testing", "closed", "alpha":
		return play.Closed
	case "open testing", "open", "beta":
		return play.Open
	case "":
	default:
		return v // a closed track of the person's own naming
	}
	switch t.Purpose {
	case "internal":
		return play.Internal
	case "external":
		return play.Closed
	}
	return ""
}

// trackMap says which Play track each of the product's tracks feeds.
func (r *run) trackMap() map[string]string {
	out := map[string]string{}
	for _, t := range r.req.Tracks[r.pid] {
		if p := playTrack(t); t.On && t.Makes && p != "" {
			out[t.Name] = play.TrackName(p)
		}
	}
	return out
}

// enplaceTrack is the Enplace track that feeds a Play track.
func (r *run) enplaceTrack(p string) string {
	for _, t := range r.req.Tracks[r.pid] {
		if t.On && playTrack(t) == p {
			return t.Key
		}
	}
	return ""
}

// track is the track Send to testers builds: the first it's a step on
// that makes builds for people, your own team's first.
func (r *run) track() (plugin.Track, bool) {
	ts := r.req.Tracks[r.pid]
	for _, purpose := range []string{"internal", "external"} {
		for _, t := range ts {
			if t.On && t.Makes && t.Purpose == purpose && playTrack(t) != "" {
				return t, true
			}
		}
	}
	return plugin.Track{}, false
}

// CI is what a Builds plugin shares ("public.ci").
type CI struct {
	Provider string `json:"provider"`
	Name     string `json:"name"`
	Ready    bool   `json:"ready"`
	Runs     []struct {
		Status   string    `json:"status"`
		Finished time.Time `json:"finished"`
	} `json:"runs"`
	Requests map[string]struct {
		State string `json:"state"`
		Error string `json:"error"`
	} `json:"requests"`
}

// notAndroid are Builds plugins that only build for Apple, so they never
// build this app even when they're on the project (ENP-367).
var notAndroid = map[string]bool{"google-play": true, "xcode-cloud": true}

// ci is the Builds plugin that makes this product's builds: one that shares
// its runs for this product first, else one shared for the whole project.
func (r *run) ci() (CI, bool) {
	ids := make([]string, 0, len(r.req.Peers))
	for id := range r.req.Peers {
		if !notAndroid[id] {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, own := range []bool{true, false} {
		for _, id := range ids {
			var c CI
			st := r.req.Peers[id]
			found := st.GetProject(r.pid, "public.ci", &c)
			if !own && !found {
				found = r.peer(id, "public.ci", &c)
			}
			if found {
				if c.Name == "" {
					c.Name = id
				}
				return c, true
			}
		}
	}
	return CI{}, false
}

type asked struct {
	ID    string    `json:"id"`
	At    time.Time `json:"at"`
	Track string    `json:"track"`
}

// building says where the last build asked for is: in flight, or why it
// didn't start. It counts as in flight for two hours, or until a newer
// build is on Play.
func (r *run) building(s *Status) (where, failed string) {
	var q asked
	if !r.req.State.GetProject(r.pid, "public.ci_request", &q) || q.ID == "" || r.req.Now.Sub(q.At) > 2*time.Hour {
		return "", ""
	}
	if b, ok := latest(s); ok && b.Seen.After(q.At) {
		return "", ""
	}
	c, _ := r.ci()
	a, answered := c.Requests[q.ID]
	switch {
	case answered && a.State == "failed":
		return "", a.Error
	case answered:
		return c.Name + " is building it", ""
	}
	return "Asked " + orElse(c.Name, "the Builds plugin"), ""
}

// send asks the product's Builds plugin for a build on the track testers
// get. The app's CI uploads it to Play; it shows here once it's there.
func (r *run) send() {
	s := r.status()
	st, _ := r.store()
	t, ok := r.track()
	c, hasCI := r.ci()
	switch {
	case !st.Connected:
		r.res.Toast = "Connect the Play Console in Google Play's settings first"
		return
	case !ok:
		r.res.Toast = "No track makes builds for testers yet: set one up under Tracks"
		return
	case !hasCI:
		r.res.Toast = "Add a Builds plugin, like GitHub Actions, to make the app's builds"
		return
	case !c.Ready:
		r.res.Toast = c.Name + " isn't ready to build yet"
		return
	}
	if where, _ := r.building(s); where != "" {
		r.res.Toast = where + "."
		return
	}
	r.res.SetProject(r.pid, "public.ci_request", map[string]any{
		"id": fmt.Sprintf("play-testing-%d", r.req.Now.Unix()), "at": r.req.Now, "reason": "A build for " + play.TrackName(playTrack(t)), "track": t.Key,
	})
	r.res.Toast = "Asked " + c.Name + " for a build. It shows here once it's on " + play.TrackName(playTrack(t)) + "."
	r.res.Log = "Asked " + c.Name + " for a Play testing build"
}
