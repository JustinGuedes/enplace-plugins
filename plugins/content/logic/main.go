// Command logic is the Content plugin: planning videos and posts for a
// project's channels. It is built for WASI and runs in Enplace's sandbox.
//
// A project's channels are its products of type "channel" (a TikTok,
// Instagram or YouTube account). Each piece of work picks the channels it
// posts to (every channel until someone changes it), and its due date is
// the day it posts. The plugin keeps, per project:
//
//   - "pieces": by work id, the channels a piece posts to (when someone
//     picked them) and the link of each post once it's out.
//   - "rhythm": by channel key, the weekdays it posts on.
//
// The Post to field is a label the plugin keeps in step ("TikTok ·
// Instagram"), so cards show it. Format and Promotes are choices.
package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

func main() { plugin.Run(handle) }

const (
	keyPieces = "pieces"
	keyRhythm = "rhythm"
	// channelType is the product type of a channel.
	channelType = "channel"
	day         = "2006-01-02"
)

// piece is what the plugin keeps for one piece of content.
type piece struct {
	// To are the channel keys it posts to, once someone picked them; Picked
	// says they did (an empty To then means nowhere yet).
	To     []string `json:"to,omitempty"`
	Picked bool     `json:"picked,omitempty"`
	// Links are each channel's post, by channel key.
	Links map[string]string `json:"links,omitempty"`
}

// channel is one of a project's channels.
type channel struct {
	Key  string
	Name string
	Days []time.Weekday
}

// run is one call: the request, the response and the project it's for.
type run struct {
	req    *plugin.Request
	res    *plugin.Response
	loc    *time.Location
	today  time.Time
	p      *plugin.Project
	chans  []channel
	pieces map[string]*piece
	work   []plugin.Work
}

func handle(req *plugin.Request) (*plugin.Response, error) {
	r := &run{req: req, res: &plugin.Response{}}
	r.loc, _ = time.LoadLocation(req.Timezone)
	if r.loc == nil {
		r.loc = time.UTC
	}
	r.today, _ = time.Parse(day, req.Date)
	if r.today.IsZero() {
		n := req.Now.In(r.loc)
		r.today = time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
	}
	r.load(req.ProjectID)
	switch req.Hook {
	case plugin.HookRender:
		r.render()
	case plugin.HookEvent:
		r.event()
	case plugin.HookAction:
		if req.Action != nil && !req.ReadOnly && r.p != nil {
			if err := r.action(req.Action); err != nil {
				r.res.Toast = err.Error()
			}
		}
	}
	return r.res, nil
}

// load reads one project's channels, pieces and work.
func (r *run) load(projectID string) {
	for i := range r.req.Projects {
		if r.req.Projects[i].ID == projectID && r.req.Projects[i].On {
			r.p = &r.req.Projects[i]
		}
	}
	if r.p == nil {
		return
	}
	rhythm := map[string][]time.Weekday{}
	r.req.State.GetProject(r.p.ID, keyRhythm, &rhythm)
	for _, pr := range r.p.Products {
		if pr.Type == channelType {
			r.chans = append(r.chans, channel{Key: pr.Key, Name: pr.Name, Days: rhythm[pr.Key]})
		}
	}
	r.pieces = map[string]*piece{}
	r.req.State.GetProject(r.p.ID, keyPieces, &r.pieces)
	for _, w := range r.req.Work {
		if w.ProjectID == r.p.ID || strings.HasPrefix(w.ProjectID, r.p.ID+"/") {
			r.work = append(r.work, w)
		}
	}
}

func (r *run) field(key string) string { return r.req.Plugin.ID + "." + key }

func (r *run) channel(key string) *channel {
	for i := range r.chans {
		if r.chans[i].Key == key {
			return &r.chans[i]
		}
	}
	return nil
}

// postsTo are the channels a piece posts to, in the project's order: the
// ones picked, else the channel the work is on, else every channel.
func (r *run) postsTo(w plugin.Work) []channel {
	pc := r.pieces[w.ID]
	var keys []string
	switch {
	case pc != nil && pc.Picked:
		keys = pc.To
	case r.channel(w.Product) != nil:
		keys = []string{w.Product}
	default:
		return r.chans
	}
	out := []channel{}
	for _, c := range r.chans {
		if slices.Contains(keys, c.Key) {
			out = append(out, c)
		}
	}
	return out
}

// label is the Post to field's value: the channels' names.
func label(cs []channel) string {
	names := make([]string, len(cs))
	for i, c := range cs {
		names[i] = c.Name
	}
	return strings.Join(names, " · ")
}

// open is work that's still to be posted: not an idea set aside for later,
// not posted and not cancelled.
func open(w plugin.Work) bool {
	return w.StageKind != "done" && w.StageKind != "cancelled" && w.StageKind != "later" && w.StageKind != "parked"
}

// ready is a piece that can go out as it is.
func ready(w plugin.Work) bool { return w.StageKind == "review" || w.StageKind == "done" }

func (r *run) due(w plugin.Work) (time.Time, bool) {
	if w.Due == "" {
		return time.Time{}, false
	}
	d, err := time.Parse(day, w.Due)
	return d, err == nil
}

func (r *run) stageName(w plugin.Work) string {
	for _, s := range r.p.Stages {
		if s.Key == w.Stage {
			return s.Name
		}
	}
	return w.Stage
}

// upcoming are open pieces with a post day from today on, soonest first.
func (r *run) upcoming() []plugin.Work {
	out := []plugin.Work{}
	for _, w := range r.work {
		if d, ok := r.due(w); ok && open(w) && !d.Before(r.today) {
			out = append(out, w)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Due < out[j].Due })
	return out
}

// dayWord says a day the way a person would: Today, Tomorrow, Fri, 18 Nov.
func (r *run) dayWord(d time.Time) string {
	switch n := int(d.Sub(r.today).Hours() / 24); {
	case n == 0:
		return "Today"
	case n == 1:
		return "Tomorrow"
	case n > 1 && n < 7:
		return d.Weekday().String()[:3]
	}
	return d.Format("2 Jan")
}

// weekStart is the first day of this week.
func (r *run) weekStart() time.Time {
	back := (int(r.today.Weekday()) - r.req.WeekStart + 7) % 7
	return r.today.AddDate(0, 0, -back)
}

// slot is one day a channel posts on this week.
type slot struct {
	Date    time.Time
	Channel channel
	Pieces  []plugin.Work
}

// week is each channel's slots this week, with the pieces planned in each.
func (r *run) week(c channel) []slot {
	start := r.weekStart()
	out := []slot{}
	for i := 0; i < 7; i++ {
		d := start.AddDate(0, 0, i)
		if !slices.Contains(c.Days, d.Weekday()) {
			continue
		}
		s := slot{Date: d, Channel: c}
		for _, w := range r.work {
			if w.Due != d.Format(day) || w.StageKind == "cancelled" {
				continue
			}
			for _, x := range r.postsTo(w) {
				if x.Key == c.Key {
					s.Pieces = append(s.Pieces, w)
				}
			}
		}
		out = append(out, s)
	}
	return out
}

// emptySoon is the first slot from today to two days out with nothing
// planned.
func (r *run) emptySoon() *slot {
	var first *slot
	for _, c := range r.chans {
		for _, s := range r.week(c) {
			away := s.Date.Sub(r.today)
			if len(s.Pieces) == 0 && away >= 0 && away <= 48*time.Hour && (first == nil || s.Date.Before(first.Date)) {
				x := s
				first = &x
			}
		}
	}
	return first
}

// late is the first piece posting today or tomorrow that isn't ready.
func (r *run) late() *plugin.Work {
	for _, w := range r.upcoming() {
		d, _ := r.due(w)
		if d.Sub(r.today) > 24*time.Hour {
			break
		}
		if !ready(w) {
			return &w
		}
	}
	return nil
}

// event gives a new piece its Post to label: every channel, until someone
// picks.
func (r *run) event() {
	ev := r.req.Event
	if ev == nil || ev.Type != "work.created" || r.p == nil || len(r.chans) == 0 {
		return
	}
	if l := label(r.postsTo(ev.Work)); l != "" && ev.Work.Fields[r.field("post_to")] != l {
		r.setLabel(ev.Work.ID, l)
	}
}

func (r *run) setLabel(workID, l string) {
	r.res.Ops = append(r.res.Ops, plugin.Op{Kind: "work.fields", ProjectID: r.p.ID,
		Params: map[string]any{"id": workID, "fields": map[string]string{r.field("post_to"): l}}})
}

func (r *run) find(id string) *plugin.Work {
	for i := range r.work {
		if r.work[i].ID == id {
			return &r.work[i]
		}
	}
	return nil
}

func (r *run) save() {
	r.res.SetProject(r.p.ID, keyPieces, r.pieces)
}

// action runs what a person pressed.
func (r *run) action(a *plugin.Action) error {
	args := map[string]string{}
	_ = json.Unmarshal(a.Args, &args)
	switch a.Name {
	case "toggle":
		w := r.find(args["work"])
		c := r.channel(args["channel"])
		if w == nil || c == nil {
			return errorf("That piece or channel isn't here any more")
		}
		pc := r.piece(w.ID)
		keys := []string{}
		for _, x := range r.postsTo(*w) {
			keys = append(keys, x.Key)
		}
		if i := slices.Index(keys, c.Key); i >= 0 {
			keys = slices.Delete(keys, i, i+1)
		} else {
			keys = append(keys, c.Key)
		}
		pc.To, pc.Picked = keys, true
		r.save()
		r.setLabel(w.ID, label(r.postsTo(*w)))
	case "links":
		w := r.find(args["work"])
		if w == nil {
			return errorf("That piece isn't here any more")
		}
		pc := r.piece(w.ID)
		if pc.Links == nil {
			pc.Links = map[string]string{}
		}
		for _, c := range r.postsTo(*w) {
			v := strings.TrimSpace(args["link:"+c.Key])
			if v == "" {
				delete(pc.Links, c.Key)
				continue
			}
			if u, err := url.Parse(v); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return errorf("%s's link needs to start with https://", c.Name)
			}
			pc.Links[c.Key] = v
		}
		r.save()
		to := r.postsTo(*w)
		all := len(to) > 0
		for _, c := range to {
			if pc.Links[c.Key] == "" {
				all = false
			}
		}
		if all && w.StageKind != "done" {
			r.res.Ops = append(r.res.Ops, plugin.Op{Kind: "work.move", ProjectID: r.p.ID,
				Params: map[string]any{"id": w.ID, "stage": "done", "reason": "Posted to " + label(to)}})
			r.res.Toast = "Posted everywhere: moved to " + r.doneName()
		} else {
			r.res.Toast = "Links saved"
		}
	case "rhythm":
		rhythm := map[string][]time.Weekday{}
		for _, c := range r.chans {
			if days := parseDays(args["days:"+c.Key]); len(days) > 0 {
				rhythm[c.Key] = days
			}
		}
		r.res.SetProject(r.p.ID, keyRhythm, rhythm)
		r.res.Toast = "Rhythm saved"
	}
	return nil
}

func (r *run) doneName() string {
	for _, s := range r.p.Stages {
		if s.Kind == "done" {
			return s.Name
		}
	}
	return "done"
}

func (r *run) piece(id string) *piece {
	if r.pieces[id] == nil {
		r.pieces[id] = &piece{}
	}
	return r.pieces[id]
}

var dayNames = map[string]time.Weekday{
	"su": time.Sunday, "mo": time.Monday, "tu": time.Tuesday, "we": time.Wednesday,
	"th": time.Thursday, "fr": time.Friday, "sa": time.Saturday,
}

// parseDays reads days like "Mon Wed Fri", "mon, wed" or "Every day".
func parseDays(s string) []time.Weekday {
	s = strings.ToLower(strings.TrimSpace(s))
	if strings.Contains(s, "every") || strings.Contains(s, "daily") {
		return []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday, time.Sunday}
	}
	out := []time.Weekday{}
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r < 'a' || r > 'z' }) {
		if len(f) < 2 {
			continue
		}
		if d, ok := dayNames[f[:2]]; ok && !slices.Contains(out, d) {
			out = append(out, d)
		}
	}
	slices.SortFunc(out, func(a, b time.Weekday) int { return int((a+6)%7) - int((b+6)%7) })
	return out
}

// daysText writes days back as "Mon Wed Fri".
func daysText(days []time.Weekday) string {
	if len(days) == 7 {
		return "Every day"
	}
	out := make([]string, len(days))
	for i, d := range days {
		out[i] = d.String()[:3]
	}
	return strings.Join(out, " ")
}

type userError string

func (e userError) Error() string { return string(e) }

func errorf(format string, a ...any) error {
	return userError(fmt.Sprintf(format, a...))
}
