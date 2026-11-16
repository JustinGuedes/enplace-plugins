// Command logic is the Calendar feeds plugin: subscribe to calendars' iCal
// links and show their events as busy time on Calendar and Today.
//
// The links are private (anyone with one can read the calendar), so they
// live in the plugin's vault, encrypted; its state only keeps names and how
// each calendar's last refresh went.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
	_ "time/tzdata" // the sandbox has no time zone files

	"github.com/JustinGuedes/enplace-plugins/plugins/calendar-feeds/ical"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

func main() { plugin.Run(handle) }

// Calendar is one subscription. Its link is in the vault under its id.
type Calendar struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Colour string `json:"colour,omitempty"`
}

// Status is how a calendar's last refresh went.
type Status struct {
	Count     int       `json:"count"`
	Error     string    `json:"error,omitempty"`
	FetchedAt time.Time `json:"fetched_at"`
}

func handle(req *plugin.Request) (*plugin.Response, error) {
	res := &plugin.Response{}
	var cals []Calendar
	req.State.Get("calendars", &cals)
	status := map[string]Status{}
	req.State.Get("status", &status)
	if req.Vault == nil {
		req.Vault = map[string]string{}
	}
	switch req.Hook {
	case plugin.HookSchedule:
		refresh(req, res, cals, status)
	case plugin.HookAction:
		if req.Action == nil {
			break
		}
		var args struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			URL  string `json:"url"`
		}
		_ = json.Unmarshal(req.Action.Args, &args)
		switch req.Action.Name {
		case "add":
			link, err := cleanLink(args.URL)
			if err != nil {
				return nil, err
			}
			name := strings.TrimSpace(args.Name)
			if name == "" {
				u, _ := url.Parse(link)
				name = u.Hostname()
			}
			if len(name) > 60 {
				name = name[:60]
			}
			sum := sha256.Sum256([]byte(link))
			id := hex.EncodeToString(sum[:6])
			for _, c := range cals {
				if c.ID == id {
					res.Toast = "That calendar is already here"
					return res, nil
				}
			}
			if err := plugin.VaultPut(id, link); err != nil {
				return nil, err
			}
			req.Vault[id] = link
			cals = append(cals, Calendar{ID: id, Name: name})
			res.Set("calendars", cals)
			got := refresh(req, res, cals, status)
			if st := got[id]; st.Error != "" {
				res.Toast = name + " added, but " + strings.ToLower(st.Error[:1]) + st.Error[1:]
			} else {
				res.Toast = fmt.Sprintf("Added %s: %d events", name, st.Count)
			}
		case "remove":
			var keep []Calendar
			for _, c := range cals {
				if c.ID != args.ID {
					keep = append(keep, c)
				}
			}
			_ = plugin.VaultDelete(args.ID)
			delete(req.Vault, args.ID)
			delete(status, args.ID)
			res.Set("calendars", keep)
			refresh(req, res, keep, status)
			res.Toast = "Calendar removed"
		case "refresh":
			refresh(req, res, cals, status)
			res.Toast = "Calendars refreshed"
		}
	case plugin.HookRender:
		res.Cards = map[string][]plugin.Block{plugin.SlotSettingsPanel: panel(req, cals, status)}
	}
	return res, nil
}

// refresh fetches every calendar and replaces the busy times.
func refresh(req *plugin.Request, res *plugin.Response, cals []Calendar, status map[string]Status) map[string]Status {
	loc, err := time.LoadLocation(req.Timezone)
	if err != nil {
		loc = time.UTC
	}
	now := req.Now.In(loc)
	from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -7)
	to := from.AddDate(0, 0, 60)
	events := []plugin.CalendarEvent{}
	next := map[string]Status{}
	for _, c := range cals {
		link := req.Vault[c.ID]
		st := Status{FetchedAt: req.Now}
		if link == "" {
			st.Error = "The link is missing. Remove the calendar and add it again."
			next[c.ID] = st
			continue
		}
		r, err := plugin.Fetch(plugin.FetchRequest{URL: link, Headers: map[string]string{"Accept": "text/calendar,*/*"}})
		switch {
		case err != nil:
			st.Error = "Couldn't reach the calendar."
		case r.Status != 200:
			st.Error = fmt.Sprintf("The calendar answered %d.", r.Status)
		default:
			evs := ical.ParseEvents(string(r.Body), c.ID, from, to, loc)
			if len(evs) > 3000 {
				evs = evs[:3000]
			}
			for _, e := range evs {
				events = append(events, plugin.CalendarEvent{ID: c.ID + ":" + e.ID, Calendar: c.ID, Title: e.Title, Start: e.Start, End: e.End, AllDay: e.AllDay, Source: c.Name, Free: !e.Busy && !e.AllDay})
			}
			st.Count = len(evs)
		}
		if st.Error != "" {
			// Keep showing what it had until the calendar answers again.
			if old, ok := status[c.ID]; ok {
				st.Count = old.Count
			}
		}
		next[c.ID] = st
	}
	res.Set("status", next)
	// Only replace the events when every calendar answered, so one failing
	// fetch doesn't wipe a day's meetings.
	for _, st := range next {
		if st.Error != "" && st.Count > 0 {
			return next
		}
	}
	res.Calendar = &plugin.Calendar{Events: events}
	return next
}

func cleanLink(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(strings.ToLower(raw), "webcal://") {
		raw = "https://" + raw[len("webcal://"):]
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return "", fmt.Errorf("paste the calendar's private address (https or webcal)")
	}
	return u.String(), nil
}

func ago(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	}
	return fmt.Sprintf("%d days ago", int(d.Hours()/24))
}

func panel(req *plugin.Request, cals []Calendar, status map[string]Status) []plugin.Block {
	blocks := []plugin.Block{{Type: plugin.BlockText, Text: "Busy time from your calendars shapes Today and the Calendar, so plans fit around meetings. Links are kept encrypted and only used to read the calendar."}}
	if len(cals) > 0 {
		var items []plugin.Item
		for _, c := range cals {
			st, ok := status[c.ID]
			sub := "Not checked yet"
			if ok {
				sub = fmt.Sprintf("%d events · checked %s", st.Count, ago(st.FetchedAt, req.Now))
				if st.Error != "" {
					sub = st.Error + " · " + ago(st.FetchedAt, req.Now)
				}
			}
			items = append(items, plugin.Item{Title: c.Name, Sub: sub, Icon: "calendar", Button: &plugin.Button{Label: "Remove", Action: "remove", Args: mustJSON(map[string]string{"id": c.ID})}})
		}
		blocks = append(blocks, plugin.Block{Type: plugin.BlockList, Items: items},
			plugin.Block{Type: plugin.BlockButtons, Buttons: []plugin.Button{{Label: "Refresh now", Action: "refresh"}}})
	}
	blocks = append(blocks, plugin.Block{Type: plugin.BlockForm, Text: "Add a calendar",
		Fields: []plugin.Field{
			{Key: "name", Label: "Name", Placeholder: "Personal"},
			{Key: "url", Label: "iCal address", Type: "secret", Placeholder: "https://…/basic.ics", Required: true,
				Help: "In Google Calendar: Settings → your calendar → Secret address in iCal format. In iCloud: share the calendar publicly and copy the link."},
		},
		Buttons: []plugin.Button{{Label: "Add calendar", Action: "add"}}})
	return blocks
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
