// Command logic is the Uptime plugin (its id is still "website"): it checks
// that a website product is up. Every 15 minutes (and on "Check now") it
// fetches the site through Enplace and keeps what it found; the overview
// card and the settings panel draw that. It also gives the product (and so
// the project) the site's own icon, unless App Store has one for it.
package main

import (
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

func main() { plugin.Run(handle) }

// Check is what the last check found.
type Check struct {
	URL        string    `json:"url"`
	Status     string    `json:"status"` // up or down
	Code       int       `json:"code"`
	Title      string    `json:"title,omitempty"`
	ResponseMS int       `json:"response_ms"`
	Error      string    `json:"error,omitempty"`
	CheckedAt  time.Time `json:"checked_at"`
}

func handle(req *plugin.Request) (*plugin.Response, error) {
	res := &plugin.Response{}
	pid := req.ProjectID
	link := siteURL(req, pid)
	switch req.Hook {
	case plugin.HookSchedule:
		if link == "" {
			res.SetProject(pid, "check", nil)
			updateIcon(req, res, link, nil, false)
			break
		}
		c, page := check(link, req.Now)
		res.SetProject(pid, "check", c)
		updateIcon(req, res, link, iconPage(c, page), false)
	case plugin.HookAction:
		if req.Action != nil && req.Action.Name == "check" && link != "" {
			c, page := check(link, req.Now)
			res.SetProject(pid, "check", c)
			res.Toast = map[string]string{"up": "The site is up", "down": "The site looks down"}[c.Status]
			updateIcon(req, res, link, iconPage(c, page), true)
		}
	case plugin.HookRender:
		var c *Check
		if req.State.GetProject(pid, "check", &c) && c != nil && c.URL != link {
			c = nil // the address changed since
		}
		for _, slot := range req.Slots {
			switch slot {
			case "project_card:site":
				setCard(res, slot, card(req, link, c))
				// Up is nothing new; down won't wait.
				switch {
				case link == "" || c == nil:
				case c.Status == "down":
					res.Signal(slot, plugin.LevelLoud, domain(link)+" is down")
					res.Say(slot, domain(link)+" is down, checked "+ago(c.CheckedAt, req.Now)+".", "Down")
				default:
					res.Signal(slot, plugin.LevelQuiet, "")
					res.Say(slot, domain(link)+" is up, checked "+ago(c.CheckedAt, req.Now)+".", "Up")
				}
			case plugin.SlotSettingsPanel:
				setCard(res, slot, panel(req, link, c))
			}
		}
	}
	return res, nil
}

func setCard(res *plugin.Response, slot string, blocks []plugin.Block) {
	if res.Cards == nil {
		res.Cards = map[string][]plugin.Block{}
	}
	res.Cards[slot] = blocks
}

func siteURL(req *plugin.Request, pid string) string {
	v, _ := req.ProjectSettings[pid]["url"].(string)
	return v
}

func domain(link string) string {
	u, err := url.Parse(link)
	if err != nil {
		return link
	}
	return strings.TrimPrefix(u.Hostname(), "www.")
}

var titleTag = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

var pageHeaders = map[string]string{"User-Agent": "Enplace website check", "Accept": "text/html,*/*"}

// check fetches the site and says how it went, with the page it answered.
func check(link string, now time.Time) (Check, []byte) {
	c := Check{URL: link, CheckedAt: now}
	r, err := plugin.Fetch(plugin.FetchRequest{URL: link, Headers: pageHeaders})
	if err != nil {
		c.Status, c.Error = "down", "Couldn't reach the site."
		return c, nil
	}
	c.Code, c.ResponseMS = r.Status, r.MS
	// A server error or a missing page is down. Other answers, like a bot
	// check's 403, still mean the site is there.
	if r.Status >= 500 || r.Status == 404 || r.Status == 410 {
		c.Status, c.Error = "down", fmt.Sprintf("The site answered %d.", r.Status)
		return c, nil
	}
	c.Status = "up"
	if m := titleTag.FindSubmatch(r.Body); m != nil {
		t := strings.Join(strings.Fields(html.UnescapeString(string(m[1]))), " ")
		if len(t) > 120 {
			t = t[:120]
		}
		c.Title = t
	}
	return c, r.Body
}

// iconPage is the page to read icons from, or nil when the site is down.
func iconPage(c Check, page []byte) []byte {
	if c.Status != "up" {
		return nil
	}
	if page == nil {
		page = []byte{}
	}
	return page
}

// updateIcon looks for the site's icon once a day, when the address
// changes, or on "Check now" (force), and gives it to the project. It's a
// fallback: an app's icon from its store wins.
func updateIcon(req *plugin.Request, res *plugin.Response, link string, page []byte, force bool) {
	pid := req.ProjectID
	var was *Icon
	req.State.GetProject(pid, "icon", &was)
	var now *Icon
	switch {
	case link == "":
	case page == nil:
		// Down: keep what was found while it was up.
		if was != nil && was.Site == link {
			return
		}
	case force || was == nil || was.Site != link || req.Now.Sub(was.FoundAt) >= iconEvery:
		now = &Icon{Site: link, URL: findIcon(plugin.Fetch, link, page), FoundAt: req.Now}
	default:
		return
	}
	res.SetProject(pid, "icon", now)
	before, after := "", ""
	if was != nil {
		before = was.URL
	}
	if now != nil {
		after = now.URL
	}
	if before != after {
		res.Ops = append(res.Ops, plugin.Op{Kind: "project.icon", ProjectID: pid, Params: map[string]any{"icon_url": after, "fallback": true}})
	}
}

// line says how the last check went, like "answered in 230 ms · checked 4 min ago".
func line(c *Check, now time.Time) string {
	if c == nil {
		return "Not checked yet"
	}
	when := "checked " + ago(c.CheckedAt, now)
	if c.Status == "down" {
		return c.Error + " · " + when
	}
	speed := fmt.Sprintf("%d ms", c.ResponseMS)
	if c.ResponseMS >= 1000 {
		speed = fmt.Sprintf("%.1f s", float64(c.ResponseMS)/1000)
	}
	return "answered in " + speed + " · " + when
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

func settingsHref(req *plugin.Request) string {
	for _, p := range req.Projects {
		if p.ID == req.ProjectID {
			return "/p/" + strings.ToLower(p.Key) + "/settings/website"
		}
	}
	return ""
}

func card(req *plugin.Request, link string, c *Check) []plugin.Block {
	if link == "" {
		return []plugin.Block{
			{Type: plugin.BlockText, Text: "No address yet. Add it to see whether the site is up.", Tone: "muted"},
			{Type: plugin.BlockButtons, Buttons: []plugin.Button{{Label: "Add address", Href: settingsHref(req)}}},
		}
	}
	name := domain(link)
	sub := line(c, req.Now)
	if c != nil && c.Title != "" {
		name, sub = c.Title, domain(link)+" · "+sub
	}
	label, tone := "Not checked yet", "muted"
	if c != nil {
		label, tone = map[string]string{"up": "Up", "down": "Down"}[c.Status], map[string]string{"up": "good", "down": "bad"}[c.Status]
	}
	return []plugin.Block{
		{Type: plugin.BlockStat, Label: label, Value: name, Sub: sub, Tone: tone},
		{Type: plugin.BlockButtons, Buttons: []plugin.Button{{Label: "Open site", URL: link}, {Label: "Check now", Action: "check"}}},
	}
}

func panel(req *plugin.Request, link string, c *Check) []plugin.Block {
	if link == "" {
		return []plugin.Block{{Type: plugin.BlockText, Text: "Enplace checks the site every 15 minutes once it has an address.", Tone: "muted"}}
	}
	text := line(c, req.Now)
	if c != nil {
		text = map[string]string{"up": "Up", "down": "Down"}[c.Status] + " · " + text
	}
	blocks := []plugin.Block{{Type: plugin.BlockText, Text: text}}
	var ic *Icon
	if req.State.GetProject(req.ProjectID, "icon", &ic) && ic != nil && ic.Site == link {
		if ic.URL != "" {
			blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: "The product and the project use the site's icon, unless an app gives one.", Tone: "muted"})
		} else {
			blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: "The site has no icon Enplace can use (it needs to be on https).", Tone: "muted"})
		}
	}
	return append(blocks, plugin.Block{Type: plugin.BlockButtons, Buttons: []plugin.Button{{Label: "Check now", Action: "check"}, {Label: "Open", URL: link}}})
}
