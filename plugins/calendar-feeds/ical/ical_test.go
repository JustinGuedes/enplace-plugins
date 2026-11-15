package ical

import (
	"testing"
	"time"
)

const feed = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\n" +
	"BEGIN:VTIMEZONE\r\nTZID:Europe/London\r\nEND:VTIMEZONE\r\n" +
	// Weekly standup Mon/Wed 09:30 London, with one week skipped and one moved.
	"BEGIN:VEVENT\r\nUID:standup\r\nSUMMARY:Stand\r\n  up\r\nDTSTART;TZID=Europe/London:20300107T093000\r\nDTEND;TZID=Europe/London:20300107T094500\r\n" +
	"RRULE:FREQ=WEEKLY;BYDAY=MO,WE;COUNT=10\r\nEXDATE;TZID=Europe/London:20300109T093000\r\n" +
	"BEGIN:VALARM\r\nTRIGGER:-PT5M\r\nEND:VALARM\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nUID:standup\r\nRECURRENCE-ID;TZID=Europe/London:20300114T093000\r\nSUMMARY:Stand up (late)\r\n" +
	"DTSTART;TZID=Europe/London:20300114T110000\r\nDTEND;TZID=Europe/London:20300114T111500\r\nEND:VEVENT\r\n" +
	// An all-day holiday, and a free (transparent) reminder.
	"BEGIN:VEVENT\r\nUID:hol\r\nSUMMARY:Holiday\r\nDTSTART;VALUE=DATE:20300108\r\nDTEND;VALUE=DATE:20300109\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nUID:free\r\nSUMMARY:Think\\, maybe\r\nDTSTART:20300108T130000Z\r\nDURATION:PT1H\r\nTRANSP:TRANSPARENT\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nUID:gone\r\nSUMMARY:Cancelled\r\nSTATUS:CANCELLED\r\nDTSTART:20300108T150000Z\r\nDTEND:20300108T160000Z\r\nEND:VEVENT\r\n" +
	// Monthly on the second Tuesday.
	"BEGIN:VEVENT\r\nUID:monthly\r\nSUMMARY:Board\r\nDTSTART:20291211T170000Z\r\nDTEND:20291211T180000Z\r\nRRULE:FREQ=MONTHLY;BYDAY=2TU\r\nEND:VEVENT\r\n" +
	"END:VCALENDAR\r\n"

func TestParseEvents(t *testing.T) {
	from := time.Date(2030, 1, 6, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 14)
	evs := ParseEvents(feed, "c", from, to, time.UTC)
	got := map[string][]Event{}
	for _, e := range evs {
		got[e.Title] = append(got[e.Title], e)
	}
	// Mon 7, (Wed 9 skipped), Mon 14 moved, Wed 16 → 2 plain + 1 moved.
	if n := len(got["Stand up"]); n != 2 {
		t.Fatalf("standups: %d %v", n, evs)
	}
	if s := got["Stand up"][0].Start; !s.Equal(time.Date(2030, 1, 7, 9, 30, 0, 0, time.UTC)) {
		t.Fatalf("London in January is UTC: %v", s)
	}
	if late := got["Stand up (late)"]; len(late) != 1 || late[0].Start.UTC().Hour() != 11 {
		t.Fatalf("override: %v", late)
	}
	if h := got["Holiday"]; len(h) != 1 || !h[0].AllDay || h[0].Busy {
		t.Fatalf("all-day: %v", h)
	}
	if f := got["Think, maybe"]; len(f) != 1 || f[0].Busy || f[0].End.Sub(f[0].Start) != time.Hour {
		t.Fatalf("free: %v", f)
	}
	if len(got["Cancelled"]) != 0 {
		t.Fatal("cancelled event shown")
	}
	if b := got["Board"]; len(b) != 1 || b[0].Start.Day() != 8 {
		t.Fatalf("second Tuesday: %v", b)
	}
}

func TestOldDailyEventsStillExpand(t *testing.T) {
	ics := "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nUID:d\r\nSUMMARY:Lunch\r\nDTSTART:20100101T120000Z\r\nDTEND:20100101T130000Z\r\nRRULE:FREQ=DAILY\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	from := time.Date(2030, 3, 1, 0, 0, 0, 0, time.UTC)
	if evs := ParseEvents(ics, "c", from, from.AddDate(0, 0, 7), time.UTC); len(evs) != 7 {
		t.Fatalf("want 7 lunches, got %d", len(evs))
	}
}
