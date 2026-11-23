package main

import (
	"time"

	"github.com/JustinGuedes/enplace-plugins/plugins/testflight/beta"
)

// detailEvery is how often usage, sizes and feedback are read without
// the person asking: they change slowly and each build costs a call.
const detailEvery = time.Hour

// detailBuilds is how many of the newest testable builds get their usage read.
const detailBuilds = 6

// details reads how testers use the builds, how big they are and the
// latest feedback: hourly, or now when forced. Between reads each build
// keeps what was read last.
func (r *run) details(c *beta.Client, s *Status, was *Status, force bool) {
	old := map[string]*beta.Detail{}
	if was != nil {
		for _, b := range was.Builds {
			old[b.ID] = b.Detail
		}
		s.Usage, s.Feedback, s.DetailAt = was.Usage, was.Feedback, was.DetailAt
	}
	for i := range s.Builds {
		s.Builds[i].Detail = old[s.Builds[i].ID]
	}
	if !force && r.req.Now.Sub(s.DetailAt) < detailEvery {
		return
	}
	s.DetailAt = r.req.Now
	read := 0
	for i := range s.Builds {
		b := &s.Builds[i]
		if b.State == beta.Expired || b.State == beta.Failed || b.State == beta.Processing || read == detailBuilds || fetched >= maxFetched-4 {
			continue
		}
		read++
		d, err := c.Usage(b.ID)
		if err != nil {
			s.Notes = append(s.Notes, "Couldn't read how testers use "+label(*b)+": "+err.Error())
			break
		}
		if b.Detail != nil {
			d.DownloadBytes, d.InstallBytes = b.Detail.DownloadBytes, b.Detail.InstallBytes
		}
		// A build's size never changes: read it once.
		if d.DownloadBytes == 0 && b.BundleID != "" && fetched < maxFetched-4 {
			d.DownloadBytes, d.InstallBytes, _ = c.Sizes(b.BundleID)
		}
		d.At = r.req.Now
		b.Detail = &d
	}
	if fetched < maxFetched-2 {
		if u, err := c.TesterUsage(s.AppID); err == nil {
			s.Usage = u
		}
	}
	if fetched < maxFetched-2 {
		if f, err := c.Feedback(s.AppID, 20); err == nil {
			s.Feedback = f
		}
	}
}

// Shared is the builds as "public.beta": a shape any beta-testing plugin
// can share (Google Play's testing tracks too), so Enplace draws them the
// same way. Nothing in it is TestFlight's own.
type Shared struct {
	Provider  string         `json:"provider"`
	CheckedAt time.Time      `json:"checked_at"`
	DetailAt  time.Time      `json:"detail_at,omitempty"`
	URL       string         `json:"url,omitempty"`   // the app's testing page at the store
	Group     string         `json:"group,omitempty"` // who gets every build
	SetupLeft int            `json:"setup_left"`      // setup steps not done yet
	Error     string         `json:"error,omitempty"`
	Builds    []SharedBuild  `json:"builds"`
	Testers   []SharedTester `json:"testers"`
	Feedback  []SharedNote   `json:"feedback"`
}

// SharedBuild is one build. State is processing, needs_answer, ready,
// expired or failed. Counts are nil until read.
type SharedBuild struct {
	ID            string    `json:"id"`
	Version       string    `json:"version"`
	Number        string    `json:"number"`
	State         string    `json:"state"`
	StateText     string    `json:"state_text"`
	Uploaded      time.Time `json:"uploaded"`
	Expires       time.Time `json:"expires,omitzero"`
	WhatToTest    string    `json:"what_to_test,omitempty"`
	Groups        []string  `json:"groups,omitempty"`
	Installs      *int      `json:"installs,omitempty"`
	Sessions      *int      `json:"sessions,omitempty"`
	Crashes       *int      `json:"crashes,omitempty"`
	Feedback      *int      `json:"feedback,omitempty"`
	Invites       *int      `json:"invites,omitempty"`
	MinOS         string    `json:"min_os,omitempty"`
	SDK           string    `json:"sdk,omitempty"`
	Encryption    *bool     `json:"encryption,omitempty"`
	DownloadBytes int64     `json:"download_bytes,omitempty"`
	InstallBytes  int64     `json:"install_bytes,omitempty"`
	URL           string    `json:"url,omitempty"`
}

// SharedTester is someone who gets the builds, with their use over 30 days.
type SharedTester struct {
	Name     string `json:"name,omitempty"`
	Email    string `json:"email"`
	State    string `json:"state"` // invited, joined, installed, removed
	Sessions *int   `json:"sessions,omitempty"`
	Crashes  *int   `json:"crashes,omitempty"`
}

// SharedNote is one piece of feedback from a tester.
type SharedNote struct {
	ID      string    `json:"id"`
	Kind    string    `json:"kind"` // screenshot or crash
	At      time.Time `json:"at"`
	Comment string    `json:"comment,omitempty"`
	From    string    `json:"from,omitempty"`
	Device  string    `json:"device,omitempty"`
	OS      string    `json:"os,omitempty"`
	Build   string    `json:"build,omitempty"` // the build's number
	Image   string    `json:"image,omitempty"`
}

func num(n int) *int { return &n }

func (r *run) shared(s *Status) Shared {
	out := Shared{Provider: "TestFlight", CheckedAt: s.CheckedAt, DetailAt: s.DetailAt, Error: s.Error,
		SetupLeft: left(r.steps(s)), Builds: []SharedBuild{}, Testers: []SharedTester{}, Feedback: []SharedNote{}}
	if s.AppID != "" {
		out.URL = "https://appstoreconnect.apple.com/apps/" + s.AppID + "/testflight/ios"
	}
	if s.Group != nil {
		out.Group = s.Group.Name
	}
	numbers := map[string]string{}
	for _, b := range s.Builds {
		numbers[b.ID] = b.Number
		sb := SharedBuild{ID: b.ID, Version: b.Version, Number: b.Number, State: b.State, StateText: beta.StateName(b.State),
			Uploaded: b.Uploaded, Expires: b.Expires, WhatToTest: b.WhatToTest, MinOS: b.MinOS, SDK: b.SDK, Encryption: b.Encryption}
		if s.Group != nil && s.Group.AllBuild && b.State != beta.Failed {
			sb.Groups = []string{s.Group.Name}
		}
		if d := b.Detail; d != nil {
			sb.Installs, sb.Sessions, sb.Crashes, sb.Feedback, sb.Invites = num(d.Installs), num(d.Sessions), num(d.Crashes), num(d.Feedback), num(d.Invites)
			sb.DownloadBytes, sb.InstallBytes = d.DownloadBytes, d.InstallBytes
		}
		if s.AppID != "" {
			sb.URL = "https://appstoreconnect.apple.com/apps/" + s.AppID + "/testflight/ios/" + b.ID
		}
		out.Builds = append(out.Builds, sb)
	}
	for _, t := range s.Testers {
		st := SharedTester{Name: t.Name, Email: t.Email, State: map[string]string{"INSTALLED": "installed", "ACCEPTED": "joined", "INVITED": "invited", "REVOKED": "removed"}[t.State]}
		if st.State == "" {
			st.State = "invited"
		}
		if u, ok := s.Usage[t.ID]; ok {
			st.Sessions, st.Crashes = num(u.Sessions), num(u.Crashes)
		}
		out.Testers = append(out.Testers, st)
	}
	for _, n := range s.Feedback {
		from := n.Email
		for _, t := range s.Testers {
			if t.Email == n.Email && t.Name != "" {
				from = t.Name
			}
		}
		out.Feedback = append(out.Feedback, SharedNote{ID: n.ID, Kind: n.Kind, At: n.At, Comment: n.Comment, From: from,
			Device: n.Device, OS: n.OS, Build: numbers[n.Build], Image: n.Image})
	}
	return out
}

// find is the build with this ID from the last check.
func find(s *Status, id string) (beta.Build, bool) {
	if s != nil {
		for _, b := range s.Builds {
			if b.ID == id {
				return b, true
			}
		}
	}
	return beta.Build{}, false
}

// editWhatToTest writes a build's What to test as the person typed it.
func (r *run) editWhatToTest(id, text string) {
	s := r.status()
	b, ok := find(s, id)
	if !ok {
		r.res.Toast = "That build isn't in TestFlight any more"
		return
	}
	c, err := r.client()
	if err == nil {
		err = c.SetWhatToTest(b, text)
	}
	if err != nil {
		r.res.Toast = "Couldn't save What to test: " + err.Error()
		return
	}
	r.res.Log = "Wrote What to test for " + label(b)
	r.refresh(false)
	r.res.Toast = "Saved What to test"
}

// expire stops testers installing a build.
func (r *run) expire(id string) {
	s := r.status()
	b, ok := find(s, id)
	if !ok {
		r.res.Toast = "That build isn't in TestFlight any more"
		return
	}
	c, err := r.client()
	if err == nil {
		err = c.Expire(b.ID)
	}
	if err != nil {
		r.res.Toast = "Couldn't stop it: " + err.Error()
		return
	}
	r.res.Log = "Stopped testing " + label(b)
	r.refresh(false)
	r.res.Toast = "Testers can't install " + label(b) + " any more"
}
