package store

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Sales reads App Store Connect's Sales and Trends reports: how often an
// app was downloaded, updated and bought. A report covers one day, month or
// year for everything the vendor sells, so each is fetched once, cut down to
// the one app, and kept in a Ledger.

// Agg is one app's numbers for a period.
type Agg struct {
	New     int `json:"n,omitempty"` // first-time downloads
	Again   int `json:"r,omitempty"` // redownloads
	Updates int `json:"u,omitempty"`
	// Bought counts paid things: in-app purchases and paid downloads.
	Bought int `json:"b,omitempty"`
	// Proceeds are what the developer earns, by currency. Apple reports
	// each sale in its own currency and gives no exchange rate.
	Proceeds map[string]float64 `json:"p,omitempty"`
	// Countries are first-time downloads by country code (the biggest few).
	Countries map[string]int `json:"c,omitempty"`
}

// Add sums b into a.
func (a *Agg) Add(b *Agg) {
	if b == nil {
		return
	}
	a.New += b.New
	a.Again += b.Again
	a.Updates += b.Updates
	a.Bought += b.Bought
	for k, v := range b.Proceeds {
		if a.Proceeds == nil {
			a.Proceeds = map[string]float64{}
		}
		a.Proceeds[k] += v
	}
	for k, v := range b.Countries {
		if a.Countries == nil {
			a.Countries = map[string]int{}
		}
		a.Countries[k] += v
	}
}

const maxCountries = 8

func (a *Agg) trim() {
	if len(a.Countries) <= maxCountries {
		return
	}
	top := Top(a.Countries)
	a.Countries = map[string]int{}
	for _, c := range top[:maxCountries] {
		a.Countries[c.Key] = c.N
	}
}

// Count is a key and how many.
type Count struct {
	Key string
	N   int
}

// Top sorts counts, biggest first.
func Top(m map[string]int) []Count {
	out := make([]Count, 0, len(m))
	for k, v := range m {
		out = append(out, Count{k, v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].N != out[j].N {
			return out[i].N > out[j].N
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// ParseSales cuts a sales summary report (tab-separated, as Apple sends it)
// down to one app: the rows for its Apple ID, and the in-app purchases
// whose parent is its SKU.
func ParseSales(tsv []byte, appleID, sku string) *Agg {
	a := &Agg{}
	lines := strings.Split(strings.ReplaceAll(string(tsv), "\r\n", "\n"), "\n")
	if len(lines) < 2 {
		return a
	}
	col := map[string]int{}
	for i, h := range strings.Split(lines[0], "\t") {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}
	for _, need := range []string{"product type identifier", "units", "apple identifier"} {
		if _, ok := col[need]; !ok {
			return a
		}
	}
	for _, line := range lines[1:] {
		f := strings.Split(line, "\t")
		cell := func(name string) string {
			i, ok := col[name]
			if !ok || i >= len(f) {
				return ""
			}
			return strings.TrimSpace(f[i])
		}
		kind := strings.ToUpper(cell("product type identifier"))
		units, err := strconv.ParseFloat(cell("units"), 64)
		if err != nil || kind == "" {
			continue
		}
		n := int(units)
		inApp := strings.HasPrefix(kind, "IA") || strings.HasPrefix(kind, "FI")
		if inApp {
			if sku == "" || cell("parent identifier") != sku {
				continue
			}
		} else if appleID == "" || cell("apple identifier") != appleID {
			continue
		}
		each, _ := strconv.ParseFloat(cell("developer proceeds"), 64)
		earn := func() {
			if each == 0 {
				return
			}
			a.Bought += n
			cur := strings.ToUpper(cell("currency of proceeds"))
			if cur == "" {
				cur = "?"
			}
			if a.Proceeds == nil {
				a.Proceeds = map[string]float64{}
			}
			a.Proceeds[cur] += each * units
		}
		switch {
		case inApp:
			earn()
		case strings.HasPrefix(kind, "1") || kind == "F1":
			a.New += n
			if c := strings.ToUpper(cell("country code")); c != "" && n != 0 {
				if a.Countries == nil {
					a.Countries = map[string]int{}
				}
				a.Countries[c] += n
			}
			earn()
		case strings.HasPrefix(kind, "3") || kind == "F3":
			a.Again += n
		case strings.HasPrefix(kind, "7") || kind == "F7":
			a.Updates += n
		}
	}
	a.trim()
	return a
}

// Period is one report: a day (2026-11-11), a month (2026-11) or a year.
type Period string

// Frequency is what Apple calls the period's length.
func (p Period) Frequency() string {
	switch len(p) {
	case 4:
		return "YEARLY"
	case 7:
		return "MONTHLY"
	}
	return "DAILY"
}

// ErrSalesAccess means the key works but may not read sales reports.
var ErrSalesAccess = errors.New("this App Store Connect key can't read sales reports: it needs the Sales, Finance or Admin role")

// Sales fetches one period's report for an app. found is false when Apple
// has no report for the period: nothing sold, or it isn't out yet.
func Sales(get Get, token, vendor string, p Period, appleID, sku string) (agg *Agg, found bool, err error) {
	q := url.Values{}
	q.Set("filter[frequency]", p.Frequency())
	q.Set("filter[reportType]", "SALES")
	q.Set("filter[reportSubType]", "SUMMARY")
	q.Set("filter[vendorNumber]", strings.TrimSpace(vendor))
	q.Set("filter[reportDate]", string(p))
	q.Set("filter[version]", "1_0")
	status, raw, err := get(ConnectBase+"/v1/salesReports?"+q.Encode(), map[string]string{"Authorization": "Bearer " + token, "Accept": "application/a-gzip, application/json"})
	if err != nil {
		return nil, false, errors.New("App Store Connect didn't answer")
	}
	switch {
	case status == 404:
		return nil, false, nil
	case status == 401:
		return nil, false, errors.New("App Store Connect refused the key (401)")
	case status == 403:
		return nil, false, ErrSalesAccess
	case status == 400:
		// Apple answers 400 for a vendor number it doesn't know, and for a
		// date outside what it keeps.
		if strings.Contains(strings.ToLower(string(raw)), "vendor") {
			return nil, false, errors.New("App Store Connect doesn't know that vendor number")
		}
		return nil, false, nil
	case status != 200:
		return nil, false, fmt.Errorf("App Store Connect answered %d", status)
	}
	if len(raw) > 2 && raw[0] == 0x1f && raw[1] == 0x8b {
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, false, errors.New("App Store Connect sent a report that can't be read")
		}
		raw, err = io.ReadAll(io.LimitReader(zr, 32<<20))
		if err != nil {
			return nil, false, errors.New("App Store Connect sent a report that can't be read")
		}
	}
	return ParseSales(raw, appleID, sku), true, nil
}

// Ledger is every report kept for one app. An empty entry is a period that
// was checked and had nothing.
type Ledger struct {
	// App is whose numbers these are; a ledger for another app is dropped.
	App    string          `json:"app"`
	Days   map[string]*Agg `json:"days,omitempty"`
	Months map[string]*Agg `json:"months,omitempty"`
	Years  map[string]*Agg `json:"years,omitempty"`
	// Since is set once the years have been read back to before the first
	// sale: the oldest year looked at.
	Since string `json:"since,omitempty"`
}

const (
	keepDays   = 62
	keepMonths = 13
	dayFmt     = "2006-01-02"
	monthFmt   = "2006-01"
	firstYear  = 2008 // the App Store opened
)

func day(t time.Time) string { return t.UTC().Format(dayFmt) }

// Wanted lists the reports still missing, most useful first: the last week,
// then the years (for the total), the months, and the older days.
func (l *Ledger) Wanted(now time.Time, max int) []Period {
	now = now.UTC()
	out := []Period{}
	days := func(from, to int) {
		for i := from; i <= to; i++ {
			d := day(now.AddDate(0, 0, -i))
			if _, ok := l.Days[d]; !ok {
				out = append(out, Period(d))
			}
		}
	}
	days(1, 8)
	if l.Since == "" {
		for y := now.Year() - 1; y >= firstYear; y-- {
			if _, ok := l.Years[strconv.Itoa(y)]; !ok {
				out = append(out, Period(strconv.Itoa(y)))
				break // one at a time: the first empty year ends the search
			}
		}
	}
	first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	for i := 1; i < keepMonths; i++ {
		m := first.AddDate(0, -i, 0).Format(monthFmt)
		if _, ok := l.Months[m]; !ok {
			out = append(out, Period(m))
		}
	}
	days(9, keepDays-1)
	if len(out) > max {
		out = out[:max]
	}
	return out
}

// Put records a report. A period Apple had nothing for is only recorded as
// empty once it's old enough that the report can't simply be late.
func (l *Ledger) Put(p Period, a *Agg, found bool, now time.Time) {
	now = now.UTC()
	if !found {
		a = &Agg{}
	}
	switch len(p) {
	case 4:
		y, _ := strconv.Atoi(string(p))
		if !found {
			if y == now.Year()-1 && now.YearDay() <= 20 {
				return // last year's report comes out in January
			}
			l.Since = string(p)
		}
		if y <= firstYear {
			l.Since = string(p)
		}
		if l.Years == nil {
			l.Years = map[string]*Agg{}
		}
		l.Years[string(p)] = a
	case 7:
		if !found {
			t, err := time.Parse(monthFmt, string(p))
			if err != nil || now.Before(t.AddDate(0, 1, 7)) {
				return
			}
		}
		if l.Months == nil {
			l.Months = map[string]*Agg{}
		}
		l.Months[string(p)] = a
	default:
		if !found {
			t, err := time.Parse(dayFmt, string(p))
			if err != nil || now.Before(t.AddDate(0, 0, 3)) {
				return
			}
		}
		if l.Days == nil {
			l.Days = map[string]*Agg{}
		}
		l.Days[string(p)] = a
	}
}

// Trim forgets days and months too old to be shown.
func (l *Ledger) Trim(now time.Time) {
	cut := day(now.AddDate(0, 0, -keepDays))
	for d := range l.Days {
		if d < cut {
			delete(l.Days, d)
		}
	}
	cutM := now.UTC().AddDate(0, -keepMonths-1, 0).Format(monthFmt)
	for m := range l.Months {
		if m < cutM {
			delete(l.Months, m)
		}
	}
}

// Point is one day of a series. Known is false for a day with no report yet.
type Point struct {
	Date  string
	Agg   Agg
	Known bool
}

// Series is the n days ending yesterday-minus-skip, oldest first.
func (l *Ledger) Series(now time.Time, n, skip int) []Point {
	out := make([]Point, 0, n)
	for i := n + skip; i > skip; i-- {
		d := day(now.AddDate(0, 0, -i))
		p := Point{Date: d}
		if a, ok := l.Days[d]; ok && a != nil {
			p.Agg, p.Known = *a, true
		}
		out = append(out, p)
	}
	return out
}

// Sum adds up a series, with how many of its days are known.
func Sum(ps []Point) (Agg, int) {
	var a Agg
	known := 0
	for i := range ps {
		if ps[i].Known {
			known++
			a.Add(&ps[i].Agg)
		}
	}
	return a, known
}

// Total is everything since launch: whole years, then whole months of the
// years with no yearly report, then the days of the months with no monthly
// one. Complete is false while reports it needs are still missing.
func (l *Ledger) Total(now time.Time) (total Agg, complete bool) {
	now = now.UTC()
	complete = l.Since != ""
	first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	for y, a := range l.Years {
		if n, _ := strconv.Atoi(y); n < now.Year() {
			total.Add(a)
		}
	}
	// Months not covered by a yearly report, back to the oldest kept.
	for m := first.AddDate(0, -1, 0); !m.Before(first.AddDate(0, -(keepMonths - 1), 0)); m = m.AddDate(0, -1, 0) {
		if _, ok := l.Years[strconv.Itoa(m.Year())]; ok && m.Year() < now.Year() {
			break
		}
		if a, ok := l.Months[m.Format(monthFmt)]; ok {
			total.Add(a)
			continue
		}
		// No monthly report yet (it comes out a few days into the next
		// month): count its days.
		got := false
		for d := m; d.Before(m.AddDate(0, 1, 0)); d = d.AddDate(0, 0, 1) {
			if a, ok := l.Days[d.Format(dayFmt)]; ok {
				total.Add(a)
				got = true
			}
		}
		if !got {
			complete = false
		}
	}
	for d := first; d.Before(now.Truncate(24 * time.Hour)); d = d.AddDate(0, 0, 1) {
		if a, ok := l.Days[d.Format(dayFmt)]; ok {
			total.Add(a)
		} else if now.Sub(d) > 72*time.Hour {
			complete = false
		}
	}
	return total, complete
}

// Review is a written App Store review.
type Review struct {
	Rating    int    `json:"rating"`
	Title     string `json:"title"`
	Body      string `json:"body,omitempty"`
	Name      string `json:"name,omitempty"`
	Date      string `json:"date"`
	Territory string `json:"territory,omitempty"`
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

// Reviews lists an app's newest written reviews.
func Reviews(get Get, token, appID string) ([]Review, error) {
	status, raw, err := get(ConnectBase+"/v1/apps/"+url.PathEscape(appID)+"/customerReviews?sort=-createdDate&limit=10", map[string]string{"Authorization": "Bearer " + token})
	if err != nil {
		return nil, errors.New("App Store Connect didn't answer")
	}
	if status == 401 || status == 403 {
		return nil, errors.New("this App Store Connect key can't read reviews")
	}
	if status != 200 {
		return nil, fmt.Errorf("App Store Connect answered %d", status)
	}
	var body struct {
		Data []struct {
			Attributes struct {
				Rating    int    `json:"rating"`
				Title     string `json:"title"`
				Body      string `json:"body"`
				Name      string `json:"reviewerNickname"`
				Created   string `json:"createdDate"`
				Territory string `json:"territory"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, errors.New("App Store Connect sent something unexpected")
	}
	out := []Review{}
	for _, d := range body.Data {
		at := d.Attributes
		date := at.Created
		if len(date) >= 10 {
			date = date[:10]
		}
		out = append(out, Review{Rating: at.Rating, Title: clip(at.Title, 120), Body: clip(at.Body, 400), Name: clip(at.Name, 40), Date: date, Territory: at.Territory})
	}
	return out, nil
}
