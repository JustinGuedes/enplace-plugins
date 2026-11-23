// Command logic is the Google Play plugin (id "google-play"): an Android
// app's store, production and what people say about it, from the Play
// Console. It reads production's live release and staged rollout, the
// latest reviews and the crash and ANR rates, and can raise, halt or finish
// a rollout and reply to a review.
//
// It holds the Play Console key and the steps that connect it, shared with
// Play testing (until connections take them over). It publishes:
//   - "public.version": what's live, so Release cycle numbers releases from it;
//   - "public.play": whether the Play Console is connected, for Play testing;
//   - "public.store": production in a shape any store plugin can share.
package main

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/JustinGuedes/enplace-plugins/plugins/google-play/play"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

func main() { plugin.Run(handle) }

// Status is what the last check found, stored per product.
type Status struct {
	CheckedAt time.Time `json:"checked_at"`
	Package   string    `json:"package,omitempty"`
	// Key says a key is set, and Email is its service account's, for the
	// Play Console invite.
	Key       bool   `json:"key"`
	Email     string `json:"email,omitempty"`
	Connected bool   `json:"connected"`
	// Reason is why it isn't connected (play.Reason*), Error in words.
	Reason string `json:"reason,omitempty"`
	Error  string `json:"error,omitempty"`
	// Tracks are every track's live release; Live is production's.
	Tracks  []Shelf       `json:"tracks,omitempty"`
	Live    *play.Release `json:"live,omitempty"`
	Reviews []play.Review `json:"reviews,omitempty"`
	Vitals  *play.Vitals  `json:"vitals,omitempty"`
	Notes   []string      `json:"notes,omitempty"`
}

// Shelf is one track and the release on it.
type Shelf struct {
	Track   string       `json:"track"`
	Release play.Release `json:"release"`
}

const keyStatus = "status"

type run struct {
	req *plugin.Request
	res *plugin.Response
	pid string
}

// keyName is the Google Play account's key, from its connection (ENP-351).
const keyName = "service_account"

func handle(req *plugin.Request) (*plugin.Response, error) {
	req.SignIn("google") // the Google Play account this product uses
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
			case "project_card:play":
				r.card(slot, r.overview())
				r.res.Signal(slot, r.level(), "")
			case plugin.SlotSettingsPanel:
				r.card(slot, r.panel())
			}
		}
	case plugin.HookPage:
		if req.Page == "play" {
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

func (r *run) pkg() string { return r.setting("package_name") }

// storeURL is the app's Google Play page.
func (r *run) storeURL() string {
	if u := r.setting("play_url"); u != "" {
		return u
	}
	if p := r.pkg(); p != "" {
		return "https://play.google.com/store/apps/details?id=" + p
	}
	return ""
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

func (r *run) client() (*play.Client, error) {
	return play.New(call, r.req.Secrets[keyName], r.pkg(), r.req.Now)
}

// refresh reads the Play Console and publishes what's live.
func (r *run) refresh() {
	was := r.status()
	s := &Status{CheckedAt: r.req.Now, Package: r.pkg(), Key: r.req.Secrets[keyName] != ""}
	defer func() {
		r.res.SetProject(r.pid, keyStatus, s)
		r.publish(s)
	}()
	if !s.Key {
		return
	}
	k, err := play.ParseKey(r.req.Secrets[keyName])
	if err != nil {
		s.Reason, s.Error = play.ReasonKey, err.Error()
		return
	}
	s.Email = k.Email
	if s.Package == "" {
		return
	}
	c, err := r.client()
	if err == nil {
		err = r.read(c, s)
	}
	if err != nil {
		s.Error = err.Error()
		if e, ok := err.(*play.Error); ok {
			s.Reason = e.Reason
		}
		s.keep(was)
		return
	}
	s.Connected = true
	if s.Reviews, err = c.Reviews(20); err != nil {
		s.Notes = append(s.Notes, "Couldn't read reviews: "+err.Error()+". The service account needs Reply to reviews.")
	}
	if v, err := c.Vitals(r.req.Now); err != nil {
		s.Notes = append(s.Notes, "Couldn't read crash rates: "+err.Error()+". Switch on the Play Developer Reporting API too.")
	} else if v.Day != "" {
		s.Vitals = &v
	}
}

// read fills in the tracks and what's live on production.
func (r *run) read(c *play.Client, s *Status) error {
	ts, err := c.Tracks()
	if err != nil {
		return err
	}
	for _, t := range ts {
		if rel, ok := play.Live(t); ok {
			s.Tracks = append(s.Tracks, Shelf{Track: t.Track, Release: rel})
			if t.Track == play.Production {
				s.Live = &rel
			}
		}
	}
	return nil
}

// keep holds on to what was known through a hiccup.
func (s *Status) keep(was *Status) {
	if was != nil && was.Package == s.Package && s.Reason == play.ReasonOther {
		s.Tracks, s.Live, s.Reviews, s.Vitals = was.Tracks, was.Live, was.Reviews, was.Vitals
	}
}

// version is what's live on Google Play: production's release, or the
// version typed by hand before the Play Console is connected.
func (r *run) version(s *Status) string {
	if s != nil && s.Live != nil && s.Live.Name != "" {
		return s.Live.Name
	}
	return r.setting("play_version")
}

func (r *run) publish(s *Status) {
	if v := r.version(s); v != "" {
		r.res.SetProject(r.pid, "public.version", map[string]string{"version": v, "store": "Google Play"})
	} else {
		r.res.SetProject(r.pid, "public.version", nil)
	}
	r.res.SetProject(r.pid, "public.play", map[string]any{
		"package": s.Package, "connected": s.Connected, "email": s.Email, "reason": s.Reason, "error": s.Error,
	})
	store := map[string]any{"provider": "google-play", "name": "Google Play", "url": r.storeURL(), "checked_at": s.CheckedAt}
	if s.Live != nil {
		live := map[string]any{"version": s.Live.Name, "build": s.Live.Code(), "state": s.Live.Status}
		if s.Live.Status != play.Completed {
			live["rollout"] = s.Live.UserFraction
		}
		store["live"] = live
	}
	if avg, n := rating(s.Reviews); n > 0 {
		store["rating"], store["ratings"] = avg, n
	}
	if s.Vitals != nil {
		store["crash_rate"], store["anr_rate"] = s.Vitals.Crash, s.Vitals.ANR
	}
	r.res.SetProject(r.pid, "public.store", store)
}

// rating is the average stars of the reviews Play hands out (the last
// week's); Play's API has no all-time rating.
func rating(rs []play.Review) (float64, int) {
	sum, n := 0, 0
	for _, v := range rs {
		if v.Stars > 0 {
			sum += v.Stars
			n++
		}
	}
	if n == 0 {
		return 0, 0
	}
	return float64(sum) / float64(n), n
}

func (r *run) action(a *plugin.Action) {
	args := map[string]string{}
	_ = json.Unmarshal(a.Args, &args)
	switch a.Name {
	case "refresh":
		r.refresh()
		r.res.Toast = r.checked()
	case "rollout":
		r.rollout(args)
	case "reply":
		r.reply(args["review"], args["text"])
	case "tool_google_play":
		r.res.Data = r.tool()
	}
}

// checked says how a Check went, in a toast.
func (r *run) checked() string {
	var s *Status
	if r.res.State != nil {
		r.res.State.GetProject(r.pid, keyStatus, &s)
	}
	switch {
	case s == nil || !s.Key:
		return "Add the Play Console key first"
	case s.Package == "":
		return "Add the app's package name first"
	case s.Connected:
		return "Connected to the Play Console"
	}
	return "Not connected yet: " + s.Error
}

// rollout raises, halts, resumes or finishes production's staged rollout.
func (r *run) rollout(args map[string]string) {
	f, _ := strconv.ParseFloat(args["to"], 64)
	halt, resume := args["do"] == "halt", args["do"] == "resume"
	c, err := r.client()
	if err == nil {
		err = c.Change(func(ts []play.Track, e *play.Edit) (bool, error) {
			t, err := play.Rollout(ts, f, halt, resume)
			if err != nil {
				return false, err
			}
			return true, e.SetTrack(t)
		})
	}
	if err != nil {
		r.res.Toast = "Couldn't change the rollout: " + err.Error()
		return
	}
	switch {
	case halt:
		r.res.Log, r.res.Toast = "Halted the Google Play rollout", "Halted. Nobody new gets this release until you resume it."
	case resume:
		r.res.Log, r.res.Toast = "Resumed the Google Play rollout", "Resumed the rollout"
	case f >= 1:
		r.res.Log, r.res.Toast = "Released to everyone on Google Play", "Released to everyone"
	default:
		r.res.Log = "Raised the Google Play rollout to " + percent(f)
		r.res.Toast = r.res.Log
	}
	r.refresh()
}

func (r *run) reply(pick, text string) {
	id := ""
	if s := r.status(); s != nil {
		for _, v := range s.Reviews {
			if label(v) == pick || v.ID == pick {
				id = v.ID
			}
		}
	}
	text = strings.TrimSpace(text)
	if id == "" || text == "" {
		r.res.Toast = "Pick a review and write a reply"
		return
	}
	c, err := r.client()
	if err == nil {
		err = c.Reply(id, text)
	}
	if err != nil {
		r.res.Toast = "Couldn't reply: " + err.Error()
		return
	}
	r.res.Log, r.res.Toast = "Replied to a Google Play review", "Replied. It shows on Google Play in a few minutes."
	r.refresh()
}

// tool is what agents get from get_google_play.
func (r *run) tool() map[string]any {
	s := r.status()
	out := map[string]any{"package": r.pkg(), "url": r.storeURL(), "version_live": r.version(s)}
	if s == nil {
		out["connected"] = false
		return out
	}
	out["connected"], out["checked_at"] = s.Connected, s.CheckedAt
	if s.Error != "" {
		out["error"] = s.Error
	}
	if left := r.stepsLeft(s); len(left) > 0 {
		out["setup_left"] = left
	}
	out["tracks"], out["reviews"] = s.Tracks, s.Reviews
	if s.Live != nil {
		out["production"] = s.Live
	}
	if s.Vitals != nil {
		out["crash_rate"], out["anr_rate"] = s.Vitals.Crash, s.Vitals.ANR
	}
	return out
}
