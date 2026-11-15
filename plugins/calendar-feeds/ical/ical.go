// Package ical reads iCalendar feeds: enough of VEVENT, time zones and common
// recurrence rules to know when someone is busy.
package ical

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

// A small iCalendar (RFC 5545) reader: enough of VEVENT, time zones and
// common recurrence rules to know when someone is busy.

type prop struct {
	name   string
	params map[string]string
	value  string
}

type vevent struct {
	uid, summary, status, transp, rrule, block string
	start, end, recurID                        time.Time
	allDay                                     bool
	duration                                   time.Duration
	exdates                                    []time.Time
	rdates                                     []time.Time
	hasEnd                                     bool
}

func unfold(data string) []string {
	data = strings.ReplaceAll(data, "\r\n", "\n")
	var out []string
	for _, l := range strings.Split(data, "\n") {
		if (strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t")) && len(out) > 0 {
			out[len(out)-1] += l[1:]
			continue
		}
		out = append(out, l)
	}
	return out
}

func parseProp(line string) (prop, bool) {
	// NAME;PARAM=V;PARAM="V:x":VALUE — the first colon outside quotes ends the head.
	inQ := false
	cut := -1
	for i, r := range line {
		if r == '"' {
			inQ = !inQ
		}
		if r == ':' && !inQ {
			cut = i
			break
		}
	}
	if cut < 0 {
		return prop{}, false
	}
	head, value := line[:cut], line[cut+1:]
	parts := strings.Split(head, ";")
	p := prop{name: strings.ToUpper(parts[0]), params: map[string]string{}, value: value}
	for _, kv := range parts[1:] {
		if k, v, ok := strings.Cut(kv, "="); ok {
			p.params[strings.ToUpper(k)] = strings.Trim(v, `"`)
		}
	}
	return p, true
}

func unescape(s string) string {
	r := strings.NewReplacer(`\n`, " ", `\N`, " ", `\,`, ",", `\;`, ";", `\\`, `\`)
	return strings.TrimSpace(r.Replace(s))
}

// parseICalTime reads DATE or DATE-TIME values. Floating times and unknown
// zones use def.
func parseICalTime(p prop, def *time.Location) (time.Time, bool, bool) {
	v := strings.TrimSpace(p.value)
	if p.params["VALUE"] == "DATE" || (len(v) == 8 && !strings.Contains(v, "T")) {
		t, err := time.ParseInLocation("20060102", v[:min(8, len(v))], def)
		return t, true, err == nil
	}
	if strings.HasSuffix(v, "Z") {
		t, err := time.Parse("20060102T150405Z", v)
		return t, false, err == nil
	}
	loc := def
	if tz := p.params["TZID"]; tz != "" {
		loc = zone(tz, def)
	}
	t, err := time.ParseInLocation("20060102T150405", v, loc)
	return t, false, err == nil
}

// zone maps a TZID to a location: IANA names directly, common Windows names
// (Outlook writes those) through a short table, anything else to def.
func zone(tz string, def *time.Location) *time.Location {
	tz = strings.TrimPrefix(tz, "/")
	if l, err := time.LoadLocation(tz); err == nil {
		return l
	}
	if iana, ok := windowsZones[tz]; ok {
		if l, err := time.LoadLocation(iana); err == nil {
			return l
		}
	}
	return def
}

var windowsZones = map[string]string{
	"UTC": "UTC", "GMT Standard Time": "Europe/London", "W. Europe Standard Time": "Europe/Berlin",
	"Romance Standard Time": "Europe/Paris", "Central Europe Standard Time": "Europe/Budapest",
	"E. Europe Standard Time": "Europe/Bucharest", "South Africa Standard Time": "Africa/Johannesburg",
	"Eastern Standard Time": "America/New_York", "Central Standard Time": "America/Chicago",
	"Mountain Standard Time": "America/Denver", "Pacific Standard Time": "America/Los_Angeles",
	"AUS Eastern Standard Time": "Australia/Sydney", "India Standard Time": "Asia/Kolkata",
	"Tokyo Standard Time": "Asia/Tokyo", "China Standard Time": "Asia/Shanghai", "Singapore Standard Time": "Asia/Singapore",
}

// parseDuration reads RFC 5545 durations such as PT1H30M or P1D.
func parseICalDuration(s string) time.Duration {
	s = strings.ToUpper(strings.TrimSpace(s))
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimLeft(s, "+-")
	s = strings.TrimPrefix(s, "P")
	var d time.Duration
	num := ""
	inTime := false
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			num += string(r)
		case r == 'T':
			inTime = true
		default:
			n, _ := strconv.Atoi(num)
			num = ""
			switch r {
			case 'W':
				d += time.Duration(n) * 7 * 24 * time.Hour
			case 'D':
				d += time.Duration(n) * 24 * time.Hour
			case 'H':
				d += time.Duration(n) * time.Hour
			case 'M':
				if inTime {
					d += time.Duration(n) * time.Minute
				}
			case 'S':
				d += time.Duration(n) * time.Second
			}
		}
	}
	if neg {
		d = -d
	}
	return d
}

func parseVEvents(data string, def *time.Location) []vevent {
	var out []vevent
	var cur *vevent
	depth := 0 // nested components inside VEVENT (VALARM)
	for _, line := range unfold(data) {
		p, ok := parseProp(line)
		if !ok {
			continue
		}
		switch {
		case p.name == "BEGIN" && strings.EqualFold(p.value, "VEVENT"):
			cur = &vevent{}
			depth = 0
			continue
		case p.name == "BEGIN" && cur != nil:
			depth++
			continue
		case p.name == "END" && cur != nil && depth > 0:
			depth--
			continue
		case p.name == "END" && strings.EqualFold(p.value, "VEVENT") && cur != nil:
			if !cur.start.IsZero() {
				if !cur.hasEnd {
					switch {
					case cur.duration > 0:
						cur.end = cur.start.Add(cur.duration)
					case cur.allDay:
						cur.end = cur.start.AddDate(0, 0, 1)
					default:
						cur.end = cur.start
					}
				}
				out = append(out, *cur)
			}
			cur = nil
			continue
		}
		if cur == nil || depth > 0 {
			continue
		}
		switch p.name {
		case "UID":
			cur.uid = strings.TrimSpace(p.value)
		case "SUMMARY":
			cur.summary = unescape(p.value)
		case "STATUS":
			cur.status = strings.ToUpper(strings.TrimSpace(p.value))
		case "TRANSP":
			cur.transp = strings.ToUpper(strings.TrimSpace(p.value))
		case "RRULE":
			cur.rrule = strings.TrimSpace(p.value)
		case "X-ENPLACE-BLOCK":
			cur.block = strings.TrimSpace(p.value)
		case "DTSTART":
			if t, allDay, ok := parseICalTime(p, def); ok {
				cur.start, cur.allDay = t, allDay
			}
		case "DTEND":
			if t, _, ok := parseICalTime(p, def); ok {
				cur.end, cur.hasEnd = t, true
			}
		case "DURATION":
			cur.duration = parseICalDuration(p.value)
		case "RECURRENCE-ID":
			if t, _, ok := parseICalTime(p, def); ok {
				cur.recurID = t
			}
		case "EXDATE", "RDATE":
			for _, v := range strings.Split(p.value, ",") {
				q := p
				q.value = v
				if t, _, ok := parseICalTime(q, def); ok {
					if p.name == "EXDATE" {
						cur.exdates = append(cur.exdates, t)
					} else {
						cur.rdates = append(cur.rdates, t)
					}
				}
			}
		}
	}
	return out
}

// ParseEvents reads every occurrence overlapping [from, to) from iCalendar
// data, expanding recurring events and applying moved or cancelled instances.
func ParseEvents(data, calID string, from, to time.Time, def *time.Location) []Event {
	evs := parseVEvents(data, def)
	overrides := map[string]map[int64]vevent{}
	for _, e := range evs {
		if !e.recurID.IsZero() {
			if overrides[e.uid] == nil {
				overrides[e.uid] = map[int64]vevent{}
			}
			overrides[e.uid][e.recurID.Unix()] = e
		}
	}
	var out []Event
	add := func(e vevent, s, en time.Time, id string) {
		if e.status == "CANCELLED" || !Overlaps(s, maxTime(en, s.Add(time.Minute)), from, to) {
			return
		}
		out = append(out, Event{ID: id, CalendarID: calID, Title: e.summary, Start: s, End: en, AllDay: e.allDay,
			Busy: e.transp != "TRANSPARENT" && !e.allDay, BlockID: e.block})
	}
	for _, e := range evs {
		if !e.recurID.IsZero() {
			continue
		}
		length := e.end.Sub(e.start)
		if e.rrule == "" && len(e.rdates) == 0 {
			add(e, e.start, e.end, e.uid)
			continue
		}
		starts := expand(e.start, e.rrule, from.Add(-length), to)
		starts = append(starts, e.rdates...)
		ex := map[int64]bool{}
		for _, x := range e.exdates {
			ex[x.Unix()] = true
		}
		for _, s := range starts {
			if ex[s.Unix()] {
				continue
			}
			id := e.uid + "/" + s.UTC().Format("20060102T150405Z")
			if o, ok := overrides[e.uid][s.Unix()]; ok {
				add(o, o.start, o.end, id)
				continue
			}
			add(e, s, s.Add(length), id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// expand lists recurrence start times up to `until`. It covers FREQ
// DAILY, WEEKLY (with BYDAY), MONTHLY (by month day, or BYDAY like 1MO / -1FR)
// and YEARLY, with INTERVAL, COUNT and UNTIL. Occurrences keep the wall-clock
// time in the event's zone across daylight-saving changes.
func expand(start time.Time, rule string, from, until time.Time) []time.Time {
	r := map[string]string{}
	for _, kv := range strings.Split(rule, ";") {
		if k, v, ok := strings.Cut(kv, "="); ok {
			r[strings.ToUpper(k)] = strings.ToUpper(v)
		}
	}
	interval, _ := strconv.Atoi(r["INTERVAL"])
	if interval < 1 {
		interval = 1
	}
	count, _ := strconv.Atoi(r["COUNT"])
	end := until
	if u := r["UNTIL"]; u != "" {
		p := prop{value: u, params: map[string]string{}}
		if t, allDay, ok := parseICalTime(p, start.Location()); ok {
			if allDay {
				t = t.AddDate(0, 0, 1)
			}
			if t.Before(end) {
				end = t.Add(time.Second)
			}
		}
	}
	days := map[time.Weekday]bool{}
	type nth struct {
		n  int
		wd time.Weekday
	}
	var nths []nth
	for _, d := range strings.Split(r["BYDAY"], ",") {
		if len(d) < 2 {
			continue
		}
		wd, ok := weekdayCodes[d[len(d)-2:]]
		if !ok {
			continue
		}
		if n, err := strconv.Atoi(d[:len(d)-2]); err == nil && n != 0 {
			nths = append(nths, nth{n, wd})
		} else {
			days[wd] = true
		}
	}
	at := func(d time.Time) time.Time {
		return time.Date(d.Year(), d.Month(), d.Day(), start.Hour(), start.Minute(), start.Second(), 0, start.Location())
	}
	var out []time.Time
	n, steps := 0, 0
	// emit counts every occurrence (for COUNT) but keeps only those from `from` on.
	emit := func(t time.Time) bool {
		steps++
		if t.Before(start) {
			return steps < 200000
		}
		if !t.Before(end) || (count > 0 && n >= count) || len(out) >= 2000 || steps >= 200000 {
			return false
		}
		n++
		if !t.Before(from) {
			out = append(out, t)
		}
		return true
	}
	switch r["FREQ"] {
	case "DAILY":
		for d := start; ; d = at(d.AddDate(0, 0, interval)) {
			if len(days) > 0 && !days[d.Weekday()] {
				if d.After(end) {
					break
				}
				continue
			}
			if !emit(d) {
				break
			}
		}
	case "WEEKLY":
		if len(days) == 0 {
			days[start.Weekday()] = true
		}
		// Weeks start on Monday (WKST default).
		weekStart := at(start.AddDate(0, 0, -((int(start.Weekday()) + 6) % 7)))
		for w := weekStart; w.Before(end); w = at(w.AddDate(0, 0, 7*interval)) {
			for i := 0; i < 7; i++ {
				d := at(w.AddDate(0, 0, i))
				if days[d.Weekday()] && !emit(d) {
					return out
				}
			}
		}
	case "MONTHLY":
		first := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, start.Location())
		for m := first; m.Before(end); m = m.AddDate(0, interval, 0) {
			var cands []time.Time
			if len(nths) > 0 {
				for _, x := range nths {
					if d, ok := nthWeekday(m, x.n, x.wd); ok {
						cands = append(cands, at(d))
					}
				}
			} else {
				day := start.Day()
				if md, err := strconv.Atoi(r["BYMONTHDAY"]); err == nil && md != 0 {
					day = md
				}
				if day < 0 {
					day = daysIn(m) + day + 1
				}
				if day >= 1 && day <= daysIn(m) {
					cands = append(cands, at(time.Date(m.Year(), m.Month(), day, 0, 0, 0, 0, m.Location())))
				}
			}
			sort.Slice(cands, func(i, j int) bool { return cands[i].Before(cands[j]) })
			for _, c := range cands {
				if !emit(c) {
					return out
				}
			}
		}
	case "YEARLY":
		for y := 0; ; y += interval {
			d := time.Date(start.Year()+y, start.Month(), start.Day(), start.Hour(), start.Minute(), start.Second(), 0, start.Location())
			if d.Month() != start.Month() { // 29 February
				continue
			}
			if !emit(d) || y > 200 {
				break
			}
		}
	default:
		emit(start)
	}
	return out
}

var weekdayCodes = map[string]time.Weekday{"SU": time.Sunday, "MO": time.Monday, "TU": time.Tuesday, "WE": time.Wednesday, "TH": time.Thursday, "FR": time.Friday, "SA": time.Saturday}

func daysIn(m time.Time) int {
	return time.Date(m.Year(), m.Month()+1, 0, 0, 0, 0, 0, m.Location()).Day()
}

// nthWeekday is the n-th (or, negative, n-th last) weekday of month m.
func nthWeekday(m time.Time, n int, wd time.Weekday) (time.Time, bool) {
	if n > 0 {
		d := time.Date(m.Year(), m.Month(), 1, 0, 0, 0, 0, m.Location())
		d = d.AddDate(0, 0, (int(wd)-int(d.Weekday())+7)%7+7*(n-1))
		return d, d.Month() == m.Month()
	}
	d := time.Date(m.Year(), m.Month(), daysIn(m), 0, 0, 0, 0, m.Location())
	d = d.AddDate(0, 0, -((int(d.Weekday())-int(wd)+7)%7)+7*(n+1))
	return d, d.Month() == m.Month()
}

// Event is one occurrence from a feed.
type Event struct {
	ID         string    `json:"id"`
	CalendarID string    `json:"calendar_id"`
	Title      string    `json:"title"`
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
	AllDay     bool      `json:"all_day"`
	Busy       bool      `json:"busy"`
	BlockID    string    `json:"block_id,omitempty"`
}

// Overlaps reports whether [s1,e1) and [s2,e2) overlap.
func Overlaps(s1, e1, s2, e2 time.Time) bool { return s1.Before(e2) && s2.Before(e1) }
