package main

import (
	"fmt"
	"time"

	"github.com/JustinGuedes/enplace-plugins/plugins/testflight/beta"
)

// next is the version and build number the next upload gets: the version
// from Versions (or the latest build's), and a build number past every
// upload and past the one in the code.
func (r *run) next(s *Status) (version, number string) {
	var v struct {
		Version string `json:"version"`
		Build   string `json:"build"`
	}
	r.peer("versions", "public.versions", &v)
	version = v.Version
	var builds []beta.Build
	if s != nil {
		builds = s.Builds
	}
	if version == "" && len(builds) > 0 {
		version = builds[0].Version
	}
	if version == "" {
		version = "1.0"
	}
	return version, beta.NextNumber(builds, v.Build)
}

// Builder is who makes the app's builds, as App shares it ("public.build"):
// the one its "Builds come from" setting picks.
type Builder struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Ready    bool   `json:"ready"`
	Note     string `json:"note"`
	Requests map[string]struct {
		State string `json:"state"`
		Run   int    `json:"run"`
		Work  string `json:"work"`
		Error string `json:"error"`
	} `json:"requests"`
}

const byHand = "hand" // the person, from Xcode

// builder is who makes builds. Without App saying, it's the person.
func (r *run) builder() Builder {
	var b Builder
	if !r.peer("app", "public.build", &b) || b.ID == "" {
		return Builder{ID: byHand, Name: "you, in Xcode", Ready: true}
	}
	return b
}

// asked is the last build this plugin asked for, if any.
type asked struct {
	ID  string    `json:"id"`
	At  time.Time `json:"at"`
	For string    `json:"for"` // the build number asked for
}

func (r *run) asked() (asked, bool) {
	var q asked
	ok := r.req.State.GetProject(r.pid, "public.build_request", &q) && q.ID != ""
	return q, ok
}

// building says where the last build TestFlight asked for is: in flight
// (with a few words on where), or why it didn't start. A build counts as
// in flight for two hours, or until a newer one is in App Store Connect.
func (r *run) building(s *Status) (where string, failed string) {
	q, ok := r.asked()
	if !ok || r.req.Now.Sub(q.At) > 2*time.Hour {
		return "", ""
	}
	if s != nil && len(s.Builds) > 0 && s.Builds[0].Uploaded.After(q.At) {
		return "", ""
	}
	b := r.builder()
	a, answered := b.Requests[q.ID]
	switch {
	case !answered:
		if r.req.Now.Sub(q.At) > time.Hour {
			return "", ""
		}
		return "Asked " + b.Name, ""
	case a.State == "failed":
		return "", a.Error
	case a.Work != "":
		return "Building: " + a.Work, ""
	case a.Run > 0:
		return fmt.Sprintf("%s is on build %d", b.Name, a.Run), ""
	}
	return b.Name + " is building", ""
}

// send asks for a new build, from whoever the app's Builds come from
// setting picks. TestFlight takes it from there once it's in App Store
// Connect.
func (r *run) send() {
	version, number, why := r.ask("A build for TestFlight")
	if why != "" {
		r.res.Toast = why
		return
	}
	b := r.builder()
	r.res.Toast = "Asked " + b.Name + " for " + version + " (" + number + "). It shows here once Apple has it."
	r.res.Log = "Asked " + b.Name + " for a TestFlight build"
}

// ask asks the builder for the next build, and says the version and build
// number it asked for, or why it couldn't, in words for the person.
func (r *run) ask(reason string) (version, number, why string) {
	s := r.status()
	b := r.builder()
	switch {
	case s == nil || s.AppID == "":
		return "", "", "Finish the TestFlight setup first: Enplace can't see the app in App Store Connect yet."
	case b.ID == byHand:
		return "", "", "Builds come from Xcode for this app: Product → Archive, then Distribute App. Change it in the App Store plugin's Builds come from."
	case !b.Ready:
		return "", "", b.Name + " isn't ready to build yet. " + b.Note
	}
	if where, _ := r.building(s); where != "" {
		return "", "", where + "."
	}
	version, number = r.next(s)
	r.res.SetProject(r.pid, "public.build_request", map[string]any{
		"id": fmt.Sprintf("testflight-%d", r.req.Now.Unix()), "reason": reason, "at": r.req.Now,
		"version": version, "build": number, "for": number,
	})
	return version, number, ""
}
