package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/JustinGuedes/enplace-plugins/plugins/testflight/beta"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
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

// open is the build item still in flight for this product, if any.
func (r *run) open() *plugin.Work {
	for i, w := range r.req.Work {
		if w.ProjectID == r.pid && w.StageKind != "done" && hasTag(w, "testflight") {
			return &r.req.Work[i]
		}
	}
	return nil
}

func (r *run) waiting() bool {
	for _, op := range r.req.Waiting {
		if op.Kind == "work.create" && op.ProjectID == r.pid {
			return true
		}
	}
	return false
}

// CI is what a CI plugin (Xcode Cloud) shares as "public.ci".
type CI struct {
	Name     string `json:"name"`
	Ready    bool   `json:"ready"`
	Runs     []Run  `json:"runs"`
	Requests map[string]struct {
		State string `json:"state"`
		Run   int    `json:"run"`
		Error string `json:"error"`
	} `json:"requests"`
}

// Run is one CI run.
type Run struct {
	Number int    `json:"number"`
	Status string `json:"status"`
}

func (r *run) ci() (CI, bool) {
	var c CI
	ok := r.peer("xcode-cloud", "public.ci", &c)
	return c, ok
}

// asked is the last build asked of CI, if any.
type asked struct {
	ID string    `json:"id"`
	At time.Time `json:"at"`
}

// building says where the last Xcode Cloud build TestFlight asked for is:
// its run number while it's queued or running, or why it didn't start.
func (r *run) building() (number int, failed string) {
	var q asked
	if !r.req.State.GetProject(r.pid, "public.ci_request", &q) || q.ID == "" {
		return 0, ""
	}
	c, _ := r.ci()
	a, ok := c.Requests[q.ID]
	switch {
	case !ok:
		if r.req.Now.Sub(q.At) < time.Hour {
			return -1, "" // asked; Xcode Cloud hasn't answered yet
		}
		return 0, ""
	case a.State == "failed":
		return 0, a.Error
	}
	for _, x := range c.Runs {
		if x.Number == a.Run && (x.Status == "queued" || x.Status == "running") {
			return x.Number, ""
		}
	}
	return 0, ""
}

// send gets a new build made: Xcode Cloud starts one, or a work item asks
// an agent on the person's Mac to build and upload it.
func (r *run) send() {
	s := r.status()
	switch {
	case s == nil || s.AppID == "":
		r.res.Toast = "Finish the TestFlight setup first: Enplace can't see the app in App Store Connect yet."
		return
	case r.mode() == byHand:
		r.res.Toast = "Builds come from Xcode for this app: Product → Archive, then Distribute App."
		return
	case r.mode() == byCloud:
		r.sendToCloud()
		return
	case r.open() != nil:
		r.res.Toast = r.open().Key + " is already on it."
		return
	case r.waiting():
		r.res.Toast = "A build is already waiting in Approvals."
		return
	}
	version, number := r.next(s)
	name := s.AppName
	if name == "" {
		name = "the app"
	}
	title := fmt.Sprintf("Upload %s %s (%s) to TestFlight", name, version, number)
	r.res.Ops = append(r.res.Ops, plugin.Op{Kind: "work.create", ProjectID: r.pid, Asked: true, Params: map[string]any{
		"title": title,
		"body":  buildBody(s, version, number, r.req.Secrets),
		"tags":  []string{"testflight", "release"},
		"doer":  "agent",
	}})
	r.res.Toast = "Sent: an agent on your Mac builds " + version + " (" + number + ") and uploads it."
	r.res.Log = title
}

// buildBody is what the agent needs: everything but the private key, which
// stays on the Mac.
func buildBody(s *Status, version, number string, sec map[string]string) string {
	keyID := strings.TrimSpace(sec["asc_key_id"])
	lines := []string{
		"Build the app from the repo's main branch and upload it to TestFlight. Follow the **upload-to-testflight** skill.",
		"",
		"- Bundle ID: `" + s.BundleID + "`",
		"- Version: `" + version + "` (MARKETING_VERSION)",
		"- Build number: `" + number + "` (CURRENT_PROJECT_VERSION)",
		"- App Store Connect key ID: `" + keyID + "`, file `~/.appstoreconnect/private_keys/AuthKey_" + keyID + ".p8`",
		"- Issuer ID: `" + strings.TrimSpace(sec["asc_issuer_id"]) + "`",
		"",
		"Set the version and build number on the command line, don't commit them. When the upload is done, add a note with what you uploaded and hand it back. Enplace gives the build to the testers and writes What to test once Apple has processed it.",
	}
	return strings.Join(lines, "\n")
}

// sendToCloud asks the Xcode Cloud plugin for a build. Its workflow's
// TestFlight post-action uploads it; this plugin takes it from there.
func (r *run) sendToCloud() {
	c, ok := r.ci()
	switch {
	case !ok:
		r.res.Toast = "Switch on the Xcode Cloud plugin on this app first, or pick another way to make builds."
		return
	case !c.Ready:
		r.res.Toast = "Xcode Cloud isn't ready for this app yet: see the steps in its settings."
		return
	}
	if n, _ := r.building(); n != 0 {
		r.res.Toast = "Xcode Cloud is already building one."
		return
	}
	r.res.SetProject(r.pid, "public.ci_request", map[string]any{
		"id": fmt.Sprintf("testflight-%d", r.req.Now.Unix()), "reason": "A build for TestFlight", "at": r.req.Now,
	})
	r.res.Toast = "Asked Xcode Cloud for a build. It shows here once Apple has it."
	r.res.Log = "Asked Xcode Cloud for a TestFlight build"
}
