package main

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/JustinGuedes/enplace-plugins/plugins/app/store"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

// The app's statistics: downloads, purchases and reviews from App Store
// Connect, and how its rating moves. The schedule gathers them into the
// project's state; everything drawn reads from there.

// Stats is how the gathering went.
type Stats struct {
	// Sales: "" before it's tried, then "ok", "no_vendor", "no_keys" or
	// "error" (with SalesError).
	Sales        string    `json:"sales,omitempty"`
	SalesError   string    `json:"sales_error,omitempty"`
	ReviewsError string    `json:"reviews_error,omitempty"`
	CheckedAt    time.Time `json:"checked_at"`
}

// Rated is the rating on one day.
type Rated struct {
	Rating float64 `json:"r"`
	Count  int     `json:"n"`
}

const keepRatings = 62

func (r *run) vendor() string {
	v, _ := r.req.Settings["asc_vendor"].(string)
	return strings.TrimSpace(v)
}

// gather runs in the schedule, after the stores were read. c is the app in
// App Store Connect, nil without keys. budget is how many fetches are left.
func (r *run) gather(in *Info, c *store.Connected, budget int) {
	now := r.req.Now
	st := Stats{CheckedAt: now}

	// The rating, once a day, so its movement can be shown later.
	if in.RatingCount > 0 {
		rated := map[string]Rated{}
		r.req.State.GetProject(r.pid, "ratings", &rated)
		rated[r.req.Date] = Rated{in.Rating, in.RatingCount}
		cut := now.AddDate(0, 0, -keepRatings).Format("2006-01-02")
		for d := range rated {
			if d < cut {
				delete(rated, d)
			}
		}
		r.res.SetProject(r.pid, "ratings", rated)
	}

	if c == nil {
		st.Sales = "no_keys"
		r.res.SetProject(r.pid, "stats", st)
		return
	}
	if budget > 0 {
		budget--
		if revs, err := store.Reviews(get, c.Token, c.AppID); err != nil {
			st.ReviewsError = err.Error()
		} else {
			r.res.SetProject(r.pid, "reviews", revs)
		}
	}

	vendor := r.vendor()
	if vendor == "" {
		st.Sales = "no_vendor"
		r.res.SetProject(r.pid, "stats", st)
		return
	}
	led := &store.Ledger{}
	r.req.State.GetProject(r.pid, "sales", &led)
	if led == nil || led.App != c.AppID+"/"+vendor {
		led = &store.Ledger{App: c.AppID + "/" + vendor}
	}
	st.Sales = "ok"
	for _, p := range led.Wanted(now, budget) {
		agg, found, err := store.Sales(get, c.Token, vendor, p, c.AppID, c.SKU)
		if err != nil {
			st.Sales, st.SalesError = "error", err.Error()
			break
		}
		led.Put(p, agg, found, now)
	}
	led.Trim(now)
	r.res.SetProject(r.pid, "sales", led)
	r.res.SetProject(r.pid, "stats", st)
}

// ----- Reading it back -----

// numbers is everything the drawing needs, worked out once.
type numbers struct {
	st      Stats
	led     *store.Ledger
	has     bool // there are download numbers to show
	asOf    string
	week    store.Agg
	prev    store.Agg
	prevOK  bool
	month   []store.Point
	month30 store.Agg
	total   store.Agg
	totalOK bool
	reviews []store.Review
	rated   map[string]Rated
}

func (r *run) numbers() *numbers {
	n := &numbers{}
	r.req.State.GetProject(r.pid, "stats", &n.st)
	r.req.State.GetProject(r.pid, "reviews", &n.reviews)
	r.req.State.GetProject(r.pid, "ratings", &n.rated)
	r.req.State.GetProject(r.pid, "sales", &n.led)
	if n.led == nil || len(n.led.Days) == 0 {
		return n
	}
	now := r.req.Now
	// Reports come out a day or two late: count back from the newest day
	// there is a report for, so a week is always seven full days.
	skip := -1
	for i := 0; i < 4; i++ {
		if _, ok := n.led.Days[now.UTC().AddDate(0, 0, -(i+1)).Format("2006-01-02")]; ok {
			skip = i
			break
		}
	}
	if skip < 0 {
		return n
	}
	n.has = true
	week := n.led.Series(now, 7, skip)
	n.asOf = week[len(week)-1].Date
	n.week, _ = store.Sum(week)
	var known int
	n.prev, known = store.Sum(n.led.Series(now, 7, skip+7))
	n.prevOK = known >= 5
	n.month = n.led.Series(now, 30, skip)
	n.month30, _ = store.Sum(n.month)
	n.total, n.totalOK = n.led.Total(now)
	return n
}

func commas(n int) string {
	s := strconv.Itoa(int(math.Abs(float64(n))))
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if n < 0 {
		return "-" + s
	}
	return s
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return commas(n) + " " + many
}

// change is "18% more" / "6% fewer" than before, or "" when it can't say.
func change(now, before int) (text string, up bool) {
	if before <= 0 || now == before {
		return "", false
	}
	pct := int(math.Round(math.Abs(float64(now-before)) / float64(before) * 100))
	if pct == 0 {
		return "", false
	}
	if now > before {
		return fmt.Sprintf("%d%% more", pct), true
	}
	return fmt.Sprintf("%d%% fewer", pct), false
}

func shortDate(d string) string {
	t, err := time.Parse("2006-01-02", d)
	if err != nil {
		return d
	}
	return t.Format("2 Jan")
}

// money writes proceeds, biggest currency first. Apple reports each sale in
// its own currency, so several are listed, not added up.
func money(p map[string]float64) string {
	type cur struct {
		k string
		v float64
	}
	cs := []cur{}
	for k, v := range p {
		if math.Abs(v) >= 0.005 {
			cs = append(cs, cur{k, v})
		}
	}
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].v != cs[j].v {
			return cs[i].v > cs[j].v
		}
		return cs[i].k < cs[j].k
	})
	parts := []string{}
	for i, c := range cs {
		if i == 2 {
			parts = append(parts, fmt.Sprintf("%d more", len(cs)-2))
			break
		}
		if c.v >= 100 {
			parts = append(parts, c.k+" "+commas(int(math.Round(c.v))))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s %.2f", c.k, c.v))
	}
	return strings.Join(parts, " + ")
}

var countryNames = map[string]string{"ZA": "South Africa", "US": "United States", "GB": "United Kingdom", "AU": "Australia", "CA": "Canada", "DE": "Germany", "FR": "France", "NL": "Netherlands", "IE": "Ireland", "NZ": "New Zealand",
	"IN": "India", "BR": "Brazil", "MX": "Mexico", "ES": "Spain", "IT": "Italy", "PT": "Portugal", "SE": "Sweden", "NO": "Norway", "DK": "Denmark", "FI": "Finland", "CH": "Switzerland", "AT": "Austria", "BE": "Belgium", "PL": "Poland",
	"JP": "Japan", "KR": "South Korea", "CN": "China", "HK": "Hong Kong", "SG": "Singapore", "TW": "Taiwan", "AE": "United Arab Emirates", "SA": "Saudi Arabia", "IL": "Israel", "TR": "Türkiye", "NG": "Nigeria", "KE": "Kenya",
	"EG": "Egypt", "AR": "Argentina", "CL": "Chile", "CO": "Colombia", "PH": "Philippines", "ID": "Indonesia", "MY": "Malaysia", "TH": "Thailand", "VN": "Vietnam", "RU": "Russia", "UA": "Ukraine", "GR": "Greece", "CZ": "Czechia", "RO": "Romania", "HU": "Hungary"}

func country(code string) string {
	if n, ok := countryNames[code]; ok {
		return n
	}
	return code
}

// story is the week in plain sentences.
func (n *numbers) story(name string) string {
	if name == "" {
		name = "the app"
	}
	var s string
	switch {
	case n.week.New == 0:
		s = "Nobody new downloaded " + name + " in the last 7 days."
	case n.week.New == 1:
		s = "1 person downloaded " + name + " in the last 7 days."
	default:
		s = commas(n.week.New) + " people downloaded " + name + " in the last 7 days"
		if c, _ := change(n.week.New, n.prev.New); c != "" && n.prevOK {
			s += ", " + c + " than the week before"
		}
		s += "."
	}
	if n.totalOK && n.total.New > n.week.New {
		s += " That makes " + commas(n.total.New) + " since launch."
	}
	return s
}

// aside is the smaller sentence under the story.
func (n *numbers) aside() string {
	parts := []string{}
	if top := store.Top(n.month30.Countries); len(top) > 0 && n.month30.New > 0 {
		where := country(top[0].Key)
		if len(top) > 1 {
			where += " and " + country(top[1].Key)
		}
		parts = append(parts, "Most new downloads this month came from "+where+".")
	}
	if fresh := n.fresh(7); fresh > 0 {
		parts = append(parts, plural(fresh, "new review", "new reviews")+" this week.")
	}
	return strings.Join(parts, " ")
}

// fresh counts reviews written in the last days.
func (n *numbers) fresh(days int) int {
	c := 0
	for _, rv := range n.reviews {
		if t, err := time.Parse("2006-01-02", rv.Date); err == nil && n.st.CheckedAt.Sub(t) < time.Duration(days)*24*time.Hour {
			c++
		}
	}
	return c
}

// chart is new downloads a day: the last week stands out, days with no
// report yet are hollow.
func chart(ps []store.Point, label string, spark bool) plugin.Block {
	max := 1
	for _, p := range ps {
		if p.Agg.New > max {
			max = p.Agg.New
		}
	}
	b := plugin.Block{Type: plugin.BlockChart, Label: label}
	if spark {
		b.Tone = "spark"
	}
	for i, p := range ps {
		h := float64(p.Agg.New) / float64(max)
		it := plugin.Item{Title: shortDate(p.Date), Progress: &h, Done: i >= len(ps)-7}
		if p.Known {
			it.Sub = plural(p.Agg.New, "download", "downloads") + " on " + shortDate(p.Date)
		} else {
			it.Locked = true
			it.Sub = "No report yet for " + shortDate(p.Date)
		}
		b.Items = append(b.Items, it)
	}
	return b
}

// ratingMove is how the number of ratings moved over the last days.
func (n *numbers) ratingMove(today string, days int) string {
	t, err := time.Parse("2006-01-02", today)
	now, ok := n.rated[today]
	if err != nil || !ok {
		return ""
	}
	// The oldest snapshot within reach, at least a day old.
	for i := days; i >= 1; i-- {
		if was, ok := n.rated[t.AddDate(0, 0, -i).Format("2006-01-02")]; ok {
			if d := now.Count - was.Count; d > 0 {
				return plural(d, "new rating", "new ratings") + " in " + plural(i, "day", "days")
			}
			return ""
		}
	}
	return ""
}

func stars(n int) string {
	if n < 0 {
		n = 0
	}
	if n > 5 {
		n = 5
	}
	return strings.Repeat("★", n) + strings.Repeat("☆", 5-n)
}

// setup says what's missing before downloads can be shown, "" when nothing.
func (r *run) setup(n *numbers) (text string, btn *plugin.Button) {
	ws := &plugin.Button{Label: "Open App Store settings", Href: "/settings/plugins/" + r.req.Plugin.ID}
	switch n.st.Sales {
	case "no_keys":
		return "Add App Store Connect keys to see downloads, purchases and reviews here.", ws
	case "no_vendor":
		return "Add your vendor number to see downloads and purchases. It's in App Store Connect under Payments and Financial Reports.", ws
	case "error":
		return "Downloads: " + n.st.SalesError + ".", ws
	case "ok":
		if !n.has {
			return "Reading the sales reports. The first numbers appear within the hour.", nil
		}
	}
	return "", nil
}

// ----- The pieces: each is a card of its own that a screen can place -----

func (r *run) appName(in *Info) string {
	if in != nil && in.Name != "" {
		// "Open Wide!: Brush Timer" reads better as "Open Wide!".
		if i := strings.IndexAny(in.Name, ":-–—|"); i > 2 {
			return strings.TrimSpace(in.Name[:i])
		}
		return in.Name
	}
	return r.project().Name
}

func (r *run) pageHref() string {
	return "/p/" + strings.ToLower(r.project().Key) + "/x/" + r.req.Plugin.ID + "/stores"
}

// show is the project's choice of what its overview carries.
func (r *run) show() string {
	switch r.setting("overview") {
	case "A card for each number":
		return "each"
	case "No statistics":
		return "none"
	}
	return "summary"
}

// summary is the statistics part of the overview's App card.
func (r *run) summary(n *numbers, in *Info) []plugin.Block {
	if !n.has {
		return nil
	}
	return []plugin.Block{
		{Type: plugin.BlockText, Tone: "big", Text: n.story(r.appName(in))},
		chart(n.month[len(n.month)-14:], "New downloads a day, last 14 days", true),
	}
}

func (r *run) downloadsCard(n *numbers) []plugin.Block {
	if !n.has {
		return nil
	}
	b := plugin.Block{Type: plugin.BlockStat, Label: "Last 7 days", Value: commas(n.week.New)}
	if c, up := change(n.week.New, n.prev.New); c != "" && n.prevOK {
		b.Sub = c + " than the week before"
		if up {
			b.Tone = "good"
		}
	}
	if n.totalOK {
		b.Sub = join(b.Sub, commas(n.total.New)+" all time")
	}
	return []plugin.Block{b, chart(n.month[len(n.month)-14:], "New downloads a day, last 14 days", true)}
}

func (r *run) ratingCard(n *numbers, in *Info) []plugin.Block {
	if in == nil || in.Rating == 0 {
		return nil
	}
	return []plugin.Block{{Type: plugin.BlockStat, Label: "App Store", Value: fmt.Sprintf("★ %.1f", in.Rating), Sub: join(plural(in.RatingCount, "rating", "ratings"), n.ratingMove(r.req.Date, 7))}}
}

func reviewItems(revs []store.Review, max int, body bool) []plugin.Item {
	items := []plugin.Item{}
	for i, rv := range revs {
		if i == max {
			break
		}
		it := plugin.Item{Title: stars(rv.Rating) + "  " + rv.Title, Value: shortDate(rv.Date), Sub: join(rv.Name, country3(rv.Territory))}
		if body && rv.Body != "" {
			it.Sub = join(rv.Body, it.Sub)
		}
		items = append(items, it)
	}
	return items
}

// country3 names App Store Connect's three-letter territories it knows.
func country3(code string) string {
	two := map[string]string{"ZAF": "ZA", "USA": "US", "GBR": "GB", "AUS": "AU", "CAN": "CA", "DEU": "DE", "FRA": "FR", "NLD": "NL", "IRL": "IE", "NZL": "NZ", "IND": "IN", "BRA": "BR", "MEX": "MX", "ESP": "ES", "ITA": "IT", "JPN": "JP", "KOR": "KR", "CHN": "CN", "SGP": "SG", "SWE": "SE", "NOR": "NO", "DNK": "DK", "CHE": "CH"}
	if c, ok := two[code]; ok {
		return country(c)
	}
	return code
}

func (r *run) reviewsCard(n *numbers) []plugin.Block {
	if len(n.reviews) == 0 {
		return nil
	}
	return []plugin.Block{{Type: plugin.BlockList, Items: reviewItems(n.reviews, 2, false)}}
}

func (r *run) salesCard(n *numbers) []plugin.Block {
	if !n.has || n.month30.Bought == 0 {
		return nil
	}
	return []plugin.Block{{Type: plugin.BlockStat, Label: "Last 30 days", Value: plural(n.month30.Bought, "purchase", "purchases"), Sub: money(n.month30.Proceeds)}}
}

// statsBlocks is the statistics on the App Store page.
func (r *run) statsBlocks(in *Info) []plugin.Block {
	n := r.numbers()
	blocks := []plugin.Block{}
	if n.has {
		lead := plugin.Block{Type: plugin.BlockSection, Blocks: []plugin.Block{{Type: plugin.BlockText, Tone: "big", Text: n.story(r.appName(in))}}}
		if a := n.aside(); a != "" {
			lead.Blocks = append(lead.Blocks, plugin.Block{Type: plugin.BlockText, Text: a})
		}
		blocks = append(blocks, lead)
		blocks = append(blocks, plugin.Block{Type: plugin.BlockSection, Blocks: []plugin.Block{
			chart(n.month, "New downloads a day, last 30 days", false),
			{Type: plugin.BlockText, Tone: "muted", Text: "Up to " + shortDate(n.asOf) + ". Apple's reports arrive a day or two behind."},
		}})
	}
	if text, btn := r.setup(n); text != "" {
		tone := "muted"
		if n.st.Sales == "error" {
			tone = "warn"
		}
		sec := plugin.Block{Type: plugin.BlockSection, Text: "Downloads", Blocks: []plugin.Block{{Type: plugin.BlockText, Text: text, Tone: tone}}}
		if btn != nil {
			sec.Blocks = append(sec.Blocks, plugin.Block{Type: plugin.BlockButtons, Buttons: []plugin.Button{*btn}})
		}
		blocks = append(blocks, sec)
	}

	tiles := []plugin.Item{}
	if in != nil && in.Rating > 0 {
		tiles = append(tiles, plugin.Item{Title: plural(in.RatingCount, "rating", "ratings"), Value: fmt.Sprintf("★ %.1f", in.Rating), Sub: n.ratingMove(r.req.Date, 7)})
	}
	if n.has {
		if n.totalOK {
			tiles = append(tiles, plugin.Item{Title: "Downloads since launch", Value: commas(n.total.New)})
		}
		tiles = append(tiles, plugin.Item{Title: "Downloads, 30 days", Value: commas(n.month30.New)})
		tiles = append(tiles, plugin.Item{Title: "Updates, 7 days", Value: commas(n.week.Updates)})
		if n.week.Again > 0 {
			tiles = append(tiles, plugin.Item{Title: "Redownloads, 7 days", Value: commas(n.week.Again)})
		}
		if n.month30.Bought != 0 {
			tiles = append(tiles, plugin.Item{Title: "Purchases, 30 days", Value: commas(n.month30.Bought), Sub: money(n.month30.Proceeds)})
		}
	}
	if len(tiles) > 0 {
		blocks = append(blocks, plugin.Block{Type: plugin.BlockStats, Items: tiles})
	}

	if n.has {
		if top := store.Top(n.month30.Countries); len(top) > 1 && n.month30.New > 0 {
			items := []plugin.Item{}
			for i, c := range top {
				if i == 5 {
					break
				}
				share := float64(c.N) / float64(top[0].N)
				items = append(items, plugin.Item{Title: country(c.Key), Value: commas(c.N), Progress: &share})
			}
			blocks = append(blocks, plugin.Block{Type: plugin.BlockSection, Text: "Where downloads come from", Sub: "Last 30 days", Blocks: []plugin.Block{{Type: plugin.BlockList, Items: items}}})
		}
		// The last year, a month at a bar.
		if len(n.led.Months) > 1 {
			months := []string{}
			for m := range n.led.Months {
				months = append(months, m)
			}
			sort.Strings(months)
			max := 1
			for _, m := range months {
				if v := n.led.Months[m].New; v > max {
					max = v
				}
			}
			yr := plugin.Block{Type: plugin.BlockChart, Label: "New downloads a month"}
			for _, m := range months {
				a := n.led.Months[m]
				h := float64(a.New) / float64(max)
				name := m
				if t, err := time.Parse("2006-01", m); err == nil {
					name = t.Format("Jan 2006")
				}
				yr.Items = append(yr.Items, plugin.Item{Title: name, Sub: plural(a.New, "download", "downloads") + " in " + name, Progress: &h})
			}
			blocks = append(blocks, plugin.Block{Type: plugin.BlockSection, Text: "The last year", Collapsed: true, Blocks: []plugin.Block{yr}})
		}
	}

	if len(n.reviews) > 0 {
		blocks = append(blocks, plugin.Block{Type: plugin.BlockSection, Text: "Latest reviews", Blocks: []plugin.Block{{Type: plugin.BlockList, Items: reviewItems(n.reviews, 5, true)}}})
	} else if n.st.ReviewsError != "" {
		blocks = append(blocks, plugin.Block{Type: plugin.BlockSection, Text: "Latest reviews", Blocks: []plugin.Block{{Type: plugin.BlockText, Tone: "muted", Text: strings.ToUpper(n.st.ReviewsError[:1]) + n.st.ReviewsError[1:] + "."}}})
	}
	return blocks
}

func size(b int64) string {
	if b <= 0 {
		return ""
	}
	return fmt.Sprintf("%.0f MB", math.Max(1, float64(b)/1e6))
}

// toolStats is the statistics an agent gets from get_app.
func (r *run) toolStats() map[string]any {
	n := r.numbers()
	out := map[string]any{"downloads_available": n.has}
	if n.has {
		out["as_of"] = n.asOf
		out["last_7_days"] = map[string]any{"downloads": n.week.New, "redownloads": n.week.Again, "updates": n.week.Updates, "purchases": n.week.Bought, "proceeds": n.week.Proceeds}
		if n.prevOK {
			out["previous_7_days_downloads"] = n.prev.New
		}
		out["last_30_days"] = map[string]any{"downloads": n.month30.New, "purchases": n.month30.Bought, "proceeds": n.month30.Proceeds, "downloads_by_country": n.month30.Countries}
		if n.totalOK {
			out["downloads_since_launch"] = n.total.New
		}
	} else if text, _ := r.setup(n); text != "" {
		out["downloads_note"] = text
	}
	if len(n.reviews) > 0 {
		out["latest_reviews"] = n.reviews
	}
	return out
}
