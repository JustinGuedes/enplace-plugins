// Command logic is the App Store plugin (id "app"): link an app
// product to its App Store listing and keep its icon, live version and
// rating up to date. With App
// Store Connect keys it also follows each version through review, and
// gathers the app's statistics (stats.go).
//
// It builds on Release cycle without sharing code: it publishes what's live
// ("public.version") and each version's state ("public.store_versions") for
// the product, and the cycle numbers from there and follows the review.
// Google Play is a plugin of its own.
package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/JustinGuedes/enplace-plugins/plugins/app/store"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

func main() { plugin.Run(handle) }

// Info is what's known about the project's app.
type Info struct {
	store.App
	// Ref is what the app was looked up by.
	Ref       string    `json:"ref"`
	CheckedAt time.Time `json:"checked_at"`
	ASC       bool      `json:"asc"`
	ASCError  string    `json:"asc_error,omitempty"`
	// AppleID and SKU are the app's own in App Store Connect.
	AppleID  string          `json:"apple_id,omitempty"`
	SKU      string          `json:"sku,omitempty"`
	Versions []store.Version `json:"versions,omitempty"`
}

// Link is an app picked from a search. From is the "App Store app" setting
// at the time: when the setting changes later, it wins.
type Link struct {
	BundleID   string `json:"bundle_id"`
	AppStoreID string `json:"app_store_id"`
	From       string `json:"from"`
}

// Search is the last App Store search in the settings panel.
type Search struct {
	Q       string      `json:"q"`
	Results []store.App `json:"results"`
}

type run struct {
	req *plugin.Request
	res *plugin.Response
	pid string
}

func handle(req *plugin.Request) (*plugin.Response, error) {
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
			case "project_card:store":
				r.card(slot, r.overview())
			case "project_card:downloads", "project_card:rating", "project_card:reviews", "project_card:sales":
				n := r.numbers()
				// Each number, so Today can put several apps together.
				r.sayNumber(slot, n)
				// The numbers one at a time, where the project asked for that.
				if r.show() != "each" {
					break
				}
				switch slot {
				case "project_card:downloads":
					r.card(slot, r.downloadsCard(n))
				case "project_card:rating":
					r.card(slot, r.ratingCard(n, r.info()))
				case "project_card:reviews":
					r.card(slot, r.reviewsCard(n))
				case "project_card:sales":
					r.card(slot, r.salesCard(n))
				}
			case plugin.SlotSettingsPanel:
				r.card(slot, r.panel())
			}
		}
	case plugin.HookPage:
		if req.Page == "stores" {
			r.res.Page = r.stores()
		}
	}
	return r.res, nil
}

func (r *run) setting(k string) string {
	v, _ := r.req.ProjectSettings[r.pid][k].(string)
	return strings.TrimSpace(v)
}

// sayNumber gives a statistic as one number, for Today's cards that put
// the same number from several apps together.
func (r *run) sayNumber(slot string, n *numbers) {
	name := r.appName(r.info())
	if name == "" {
		name = "The app"
	}
	switch slot {
	case "project_card:downloads":
		if n.has {
			r.res.Number(slot, float64(n.week.New))
			r.res.Say(slot, name+" had "+plural(n.week.New, "new download", "new downloads")+" in the last 7 days.", commas(n.week.New))
		}
	case "project_card:sales":
		if n.has {
			r.res.Number(slot, float64(n.month30.Bought))
			r.res.Say(slot, name+" had "+plural(n.month30.Bought, "purchase", "purchases")+" in the last 30 days.", commas(n.month30.Bought))
		}
	case "project_card:rating":
		if in := r.info(); in != nil && in.Rating > 0 {
			r.res.Number(slot, in.Rating)
			r.res.Say(slot, fmt.Sprintf("%s is rated %.1f from %s.", name, in.Rating, plural(in.RatingCount, "rating", "ratings")), fmt.Sprintf("★ %.1f", in.Rating))
		}
	}
}

func (r *run) card(slot string, blocks []plugin.Block) {
	if r.res.Cards == nil {
		r.res.Cards = map[string][]plugin.Block{}
	}
	r.res.Cards[slot] = blocks
}

func (r *run) info() *Info {
	var in *Info
	r.req.State.GetProject(r.pid, "info", &in)
	if in != nil && in.Ref != r.ref() {
		return nil
	}
	return in
}

// identity is which app the project has: the one picked from a search,
// unless the setting was changed since.
func (r *run) identity() (bundleID, appStoreID string, err error) {
	var l Link
	if r.req.State.GetProject(r.pid, "link", &l) && l.From == r.setting("app_store") && (l.BundleID != "" || l.AppStoreID != "") {
		return l.BundleID, l.AppStoreID, nil
	}
	return store.Identity(r.setting("app_store"))
}

func (r *run) ref() string {
	b, id, _ := r.identity()
	return b + "/" + id
}

// fetched counts the fetches of this call: Enplace allows 20.
var fetched int

const maxFetched = 20

func get(u string, headers map[string]string) (int, []byte, error) {
	if headers == nil {
		headers = map[string]string{}
	}
	if headers["Accept"] == "" {
		headers["Accept"] = "application/json"
	}
	fetched++
	res, err := plugin.Fetch(plugin.FetchRequest{URL: u, Headers: headers})
	if err != nil {
		return 0, nil, err
	}
	return res.Status, res.Body, nil
}

// refresh reads the stores and publishes what it found.
func (r *run) refresh() {
	r.res.SetProject(r.pid, "public.kind", "app")
	bundleID, storeID, err := r.identity()
	var was *Info
	r.req.State.GetProject(r.pid, "info", &was)
	in := &Info{Ref: r.ref(), CheckedAt: r.req.Now}
	in.BundleID, in.AppStoreID = bundleID, storeID
	if err == nil && (bundleID != "" || storeID != "") {
		q := bundleID
		if storeID != "" {
			q = storeID
		}
		if apps, err := store.Lookup(get, q); err == nil && len(apps) > 0 {
			in.App = apps[0]
			if bundleID != "" {
				in.BundleID = bundleID
			}
		} else if was != nil && was.Ref == in.Ref {
			in.App = was.App // keep what was known through a hiccup
		}
		sec := r.req.Secrets
		var connected *store.Connected
		if sec["asc_issuer_id"] != "" && sec["asc_key_id"] != "" && sec["asc_private_key"] != "" {
			in.ASC = true
			c, err := store.ConnectApp(get, sec["asc_issuer_id"], sec["asc_key_id"], sec["asc_private_key"], in.BundleID, r.req.Now)
			if err != nil {
				in.ASCError = err.Error()
				if was != nil && was.Ref == in.Ref {
					in.Versions, in.AppleID, in.SKU = was.Versions, was.AppleID, was.SKU
				}
			} else {
				connected = c
				in.Versions, in.AppleID, in.SKU = c.Versions, c.AppID, c.SKU
				// Before the app is live the store has no icon; use the one
				// from its latest build.
				if in.IconURL == "" {
					in.IconURL = c.Icon
				}
			}
		}
		// Leave a fetch spare, whatever happens.
		r.gather(in, connected, maxFetched-1-fetched)
	}
	r.res.SetProject(r.pid, "info", in)
	if live := in.Version; live != "" {
		r.res.SetProject(r.pid, "public.version", map[string]string{"version": live, "store": "App Store"})
	} else {
		r.res.SetProject(r.pid, "public.version", nil)
	}
	type pub struct {
		Version    string `json:"version"`
		State      string `json:"state"`
		StoreState string `json:"store_state"`
	}
	vs := []pub{}
	for _, v := range in.Versions {
		vs = append(vs, pub{v.Version, store.ReleaseState(v.State), store.StateName(v.State)})
	}
	r.res.SetProject(r.pid, "public.store_versions", vs)
	if was == nil || was.IconURL != in.IconURL {
		if in.IconURL != "" || (was != nil && was.IconURL != "") {
			r.res.Ops = append(r.res.Ops, plugin.Op{Kind: "project.icon", ProjectID: r.pid, Params: map[string]string{"icon_url": in.IconURL}})
		}
	}
}

func (r *run) action(a *plugin.Action) {
	args := map[string]string{}
	_ = json.Unmarshal(a.Args, &args)
	switch a.Name {
	case "tool_app":
		in := r.info()
		if in == nil {
			r.res.Data = map[string]any{"linked": false, "how_to": "Set the App Store plugin's app_store setting on the app product to its bundle ID, App Store link or App Store ID (update_project with change_products: [{key, plugin_settings: {app: {app_store: ...}}}])."}
			return
		}
		data := map[string]any{"linked": true, "name": in.Name, "price": in.Price, "category": in.Genre, "first_released": in.FirstReleased, "bundle_id": in.BundleID, "app_store_id": in.AppStoreID, "live_version": in.Version, "rating": in.Rating, "rating_count": in.RatingCount,
			"store_url": in.StoreURL, "versions": in.Versions, "checked_at": in.CheckedAt}
		for k, v := range r.toolStats() {
			data[k] = v
		}
		r.res.Data = data
	case "search":
		q := strings.TrimSpace(args["q"])
		if q == "" {
			r.res.SetProject(r.pid, "search", nil)
			return
		}
		apps, err := store.Lookup(get, q)
		if err != nil {
			r.res.Toast = err.Error()
			return
		}
		if len(apps) == 0 {
			r.res.Toast = "No apps found. Try its App Store link, or type its bundle ID below."
		}
		r.res.SetProject(r.pid, "search", Search{Q: q, Results: apps})
	case "link":
		if args["bundle_id"] == "" && args["app_store_id"] == "" {
			return
		}
		l := Link{BundleID: args["bundle_id"], AppStoreID: args["app_store_id"], From: r.setting("app_store")}
		r.res.SetProject(r.pid, "link", l)
		r.res.SetProject(r.pid, "search", nil)
		// Refresh as the linked app.
		r.req.State.PutProject(r.pid, "link", l)
		r.refresh()
		r.res.Toast = "Linked " + args["name"]
		r.res.Log = "Linked the app to " + strings.TrimSpace(args["name"]+" "+args["bundle_id"])
	case "clear":
		r.res.SetProject(r.pid, "search", nil)
	case "refresh":
		r.refresh()
		r.res.Toast = "Checked the App Store"
	}
}

// ----- What's drawn -----

func (r *run) project() plugin.Project {
	for _, p := range r.req.Projects {
		if p.ID == r.pid {
			return p
		}
	}
	return plugin.Project{}
}

func (r *run) settingsHref() string {
	return "/p/" + strings.ToLower(r.project().Key) + "/settings/app"
}

func rating(a store.App) string {
	if a.Rating == 0 {
		return ""
	}
	return fmt.Sprintf("★ %.1f (%d)", a.Rating, a.RatingCount)
}

func join(parts ...string) string {
	out := []string{}
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " · ")
}

func (r *run) overview() []plugin.Block {
	in := r.info()
	blocks := []plugin.Block{}
	switch {
	case in == nil || (in.BundleID == "" && in.AppStoreID == ""):
		if b, id, _ := r.identity(); b == "" && id == "" {
			return []plugin.Block{
				{Type: plugin.BlockText, Text: "No bundle ID yet. Link the app to see its icon, live version and rating.", Tone: "warn"},
				{Type: plugin.BlockButtons, Buttons: []plugin.Button{{Label: "Link the app", Href: r.settingsHref()}}},
			}
		}
		blocks = append(blocks, plugin.Block{Type: plugin.BlockStat, Label: "App Store", Value: "Checking…", Tone: "muted"})
	case in.Version == "":
		blocks = append(blocks, plugin.Block{Type: plugin.BlockStat, Label: "App Store", Value: "Not live yet", Sub: in.BundleID})
	default:
		blocks = append(blocks, plugin.Block{Type: plugin.BlockStat, Label: "App Store", Value: in.Version + " live", Sub: join(rating(in.App), in.Seller)})
	}
	if r.show() == "summary" {
		// The week's numbers lead the card, where there are any.
		blocks = append(r.summary(r.numbers(), in), blocks...)
	}
	btns := []plugin.Button{{Label: "App Store page", Href: r.pageHref()}}
	if in != nil && in.StoreURL != "" {
		btns = append(btns, plugin.Button{Label: "App Store", URL: in.StoreURL})
	}
	return append(blocks, plugin.Block{Type: plugin.BlockButtons, Buttons: btns})
}

func raw(kv ...string) json.RawMessage {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	b, _ := json.Marshal(m)
	return b
}

// panel is the top of the plugin's settings in a project: the linked app
// and a search to link one.
func (r *run) panel() []plugin.Block {
	blocks := []plugin.Block{}
	in := r.info()
	bundleID, storeID, err := r.identity()
	linked := bundleID != "" || storeID != ""
	switch {
	case err != nil:
		blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: "That doesn't look like a bundle ID, an App Store link or an App Store ID.", Tone: "warn"})
	case !linked:
		blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: "No bundle ID yet. Without it Enplace can't show the app's icon, live version or rating. Find the app below, or type its bundle ID if it isn't live yet.", Tone: "warn"})
	case in == nil:
		blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: "Linked to " + join(bundleID, storeID) + ". Checking the App Store…", Tone: "muted"})
	default:
		name := in.Name
		if name == "" {
			name = join(in.BundleID, in.AppStoreID)
		}
		sub := "Not live on the App Store yet"
		if in.Version != "" {
			sub = in.Version + " live on the App Store"
		}
		blocks = append(blocks, plugin.Block{Type: plugin.BlockList, Items: []plugin.Item{{Title: name, Sub: join(sub, in.BundleID), Icon: "app", Value: rating(in.App)}}})
		switch {
		case in.ASCError != "":
			blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: "App Store Connect: " + in.ASCError, Tone: "warn"})
		case in.ASC:
			blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: "App Store Connect is connected, so releases follow each version through review.", Tone: "muted"})
		default:
			blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: "Add App Store Connect keys in the workspace's App Store settings to follow versions through review.", Tone: "muted"})
		}
	}
	var s Search
	r.req.State.GetProject(r.pid, "search", &s)
	label := "Find it on the App Store"
	if linked {
		label = "Change the app"
	}
	blocks = append(blocks, plugin.Block{Type: plugin.BlockForm,
		Fields:  []plugin.Field{{Key: "q", Label: label, Placeholder: "Its name, App Store link or bundle ID", Value: s.Q, Required: true}},
		Buttons: []plugin.Button{{Label: "Search", Action: "search"}}})
	if len(s.Results) > 0 {
		items := []plugin.Item{}
		for _, a := range s.Results {
			items = append(items, plugin.Item{Title: a.Name, Sub: join(a.Seller, a.Version, rating(a), a.BundleID), Icon: "app",
				Button: &plugin.Button{Label: "Use this app", Action: "link", Args: raw("bundle_id", a.BundleID, "app_store_id", a.AppStoreID, "name", a.Name)}})
		}
		blocks = append(blocks, plugin.Block{Type: plugin.BlockList, Items: items})
	}
	btns := []plugin.Button{}
	if s.Q != "" {
		btns = append(btns, plugin.Button{Label: "Clear search", Action: "clear"})
	}
	if linked {
		btns = append(btns, plugin.Button{Label: "Check now", Action: "refresh"})
	}
	if len(btns) > 0 {
		blocks = append(blocks, plugin.Block{Type: plugin.BlockButtons, Buttons: btns})
	}
	return blocks
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

// stores is the Stores page.
func (r *run) stores() *plugin.Page {
	pg := &plugin.Page{Title: "App Store"}
	in := r.info()
	if in != nil && (in.BundleID != "" || in.AppStoreID != "") {
		pg.Blocks = append(pg.Blocks, r.statsBlocks(in)...)
	}
	pg.Blocks = append(pg.Blocks, plugin.Block{Type: plugin.BlockHeading, Text: "On the App Store", Sub: "What's live, and each version's review."})
	sec := plugin.Block{Type: plugin.BlockSection, Text: "App Store"}
	switch {
	case in == nil || (in.BundleID == "" && in.AppStoreID == ""):
		sec.Blocks = []plugin.Block{
			{Type: plugin.BlockText, Text: "The app isn't linked to its App Store listing yet.", Tone: "warn"},
			{Type: plugin.BlockButtons, Buttons: []plugin.Button{{Label: "Link the app", Href: r.settingsHref()}}},
		}
	default:
		sec.Sub = join(in.Name, in.BundleID)
		live := "Not live yet"
		if in.Version != "" {
			live = in.Version
		}
		stats := []plugin.Item{{Title: "Live version", Value: live, Sub: in.ReleasedAt}}
		if in.Rating > 0 {
			stats = append(stats, plugin.Item{Title: "Rating", Value: fmt.Sprintf("★ %.1f", in.Rating), Sub: fmt.Sprintf("%d ratings", in.RatingCount)})
		}
		if len(in.Platforms) > 0 {
			stats = append(stats, plugin.Item{Title: "Platforms", Value: strings.Join(in.Platforms, ", ")})
		}
		if in.Price != "" {
			stats = append(stats, plugin.Item{Title: "Price", Value: in.Price, Sub: in.Genre})
		}
		if sz := size(in.Bytes); sz != "" {
			sub := ""
			if in.MinOS != "" {
				sub = "needs " + in.MinOS + " or later"
			}
			stats = append(stats, plugin.Item{Title: "Size", Value: sz, Sub: sub})
		}
		if in.FirstReleased != "" {
			stats = append(stats, plugin.Item{Title: "First released", Value: shortDate(in.FirstReleased) + " " + in.FirstReleased[:4]})
		}
		sec.Blocks = append(sec.Blocks, plugin.Block{Type: plugin.BlockStats, Items: stats})
		switch {
		case in.ASCError != "":
			sec.Blocks = append(sec.Blocks, plugin.Block{Type: plugin.BlockText, Text: "App Store Connect: " + in.ASCError, Tone: "warn"})
		case len(in.Versions) > 0:
			items := []plugin.Item{}
			for i, v := range in.Versions {
				if i == 8 {
					break
				}
				items = append(items, plugin.Item{Title: v.Version, Sub: store.StateName(v.State), Done: store.ReleaseState(v.State) == "live"})
			}
			sec.Blocks = append(sec.Blocks, plugin.Block{Type: plugin.BlockList, Items: items})
		case !in.ASC:
			sec.Blocks = append(sec.Blocks, plugin.Block{Type: plugin.BlockText, Text: "Add App Store Connect keys to see each version's review state here.", Tone: "muted"})
		}
		btns := []plugin.Button{{Label: "Check now", Action: "refresh"}}
		if in.StoreURL != "" {
			btns = append(btns, plugin.Button{Label: "Open on the App Store", URL: in.StoreURL})
		}
		sec.Blocks = append(sec.Blocks, plugin.Block{Type: plugin.BlockButtons, Buttons: btns}, plugin.Block{Type: plugin.BlockText, Text: "Checked " + ago(in.CheckedAt, r.req.Now), Tone: "muted"})
	}
	pg.Blocks = append(pg.Blocks, sec)
	return pg
}
