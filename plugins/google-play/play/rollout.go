package play

import (
	"errors"
	"slices"
)

// ErrNoRollout is a rollout change with no staged rollout on production.
var ErrNoRollout = errors.New("nothing is rolling out on production")

// Rollout changes production's staged rollout: a fraction under 1 raises
// it, 1 or more completes it (everyone gets it), and halt or resume stops
// or restarts it where it is. It returns the track to save.
func Rollout(ts []Track, fraction float64, halt, resume bool) (Track, error) {
	t, _ := Find(ts, Production)
	i := slices.IndexFunc(t.Releases, func(r Release) bool { return r.Status == InProgress || r.Status == Halted })
	if i < 0 {
		return t, ErrNoRollout
	}
	r := t.Releases[i]
	switch {
	case halt:
		r.Status = Halted
	case resume:
		r.Status = InProgress
	case fraction >= 1:
		// Everyone has it now: it's the only release production keeps.
		r.Status, r.UserFraction = Completed, 0
		t.Releases = []Release{r}
		return t, nil
	case fraction > 0:
		if fraction <= r.UserFraction {
			return t, errors.New("a rollout can only go up")
		}
		r.Status, r.UserFraction = InProgress, fraction
	}
	t.Releases[i] = r
	return t, nil
}

// Promote puts a release on another track. A testing track then has only
// it; production keeps what everyone has and rolls the new one out to a
// fraction of users first (fraction 0 sends it to everyone).
func Promote(ts []Track, r Release, to string, fraction float64) Track {
	t, _ := Find(ts, to)
	next := Release{Name: r.Name, VersionCodes: r.VersionCodes, Notes: r.Notes, Status: Completed}
	if to != Production || fraction <= 0 || fraction >= 1 {
		t.Releases = []Release{next}
		return t
	}
	next.Status, next.UserFraction = InProgress, fraction
	keep := []Release{}
	for _, old := range t.Releases {
		if old.Status == Completed {
			keep = append(keep, old)
		}
	}
	t.Releases = append(keep, next)
	return t
}

// WithNotes is a release with its notes in a language set to text.
func WithNotes(r Release, lang, text string) Release {
	notes := []Note{}
	for _, n := range r.Notes {
		if n.Language != lang {
			notes = append(notes, n)
		}
	}
	r.Notes = append(notes, Note{Language: lang, Text: text})
	return r
}

// Next is the track a release on t goes to next, or "" from production.
func Next(t string) string {
	switch t {
	case Internal:
		return Closed
	case Closed:
		return Open
	case Open:
		return Production
	}
	return ""
}
