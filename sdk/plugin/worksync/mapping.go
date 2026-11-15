package worksync

import (
	"strings"
)

// The "stages" setting maps outside statuses to the project's stages, one
// pair a line (or split by ";"): "In Progress = Doing". The right side is
// a stage's name or key, or a kind (todo, doing, review, done) for the
// project's first stage of that kind. Statuses it doesn't name go by kind:
// the source's own, or a guess from the status name.

type pair struct{ outside, stage string }

func (s *Sync) pairs() []pair {
	raw := strings.NewReplacer(";", "\n", ",", "\n").Replace(s.Setting("stages"))
	var out []pair
	for _, line := range strings.Split(raw, "\n") {
		a, b, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		a, b = strings.TrimSpace(a), strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(b), ">"))
		if a != "" && b != "" {
			out = append(out, pair{a, b})
		}
	}
	return out
}

// resolve turns a stage name, key or kind into the project's stage key.
func (s *Sync) resolve(v string) string {
	if s.project == nil || len(s.project.Stages) == 0 {
		return ""
	}
	for _, st := range s.project.Stages {
		if strings.EqualFold(st.Key, v) || strings.EqualFold(st.Name, v) {
			return st.Key
		}
	}
	for _, st := range s.project.Stages {
		if strings.EqualFold(st.Kind, v) {
			return st.Key
		}
	}
	return ""
}

// stageFor is the project's stage key for an outside item.
func (s *Sync) stageFor(it Item) string {
	if it.Stage != "" && s.project != nil {
		for _, st := range s.project.Stages {
			if st.Key == it.Stage {
				return st.Key
			}
		}
	}
	for _, p := range s.pairs() {
		if strings.EqualFold(p.outside, it.Status) {
			if k := s.resolve(p.stage); k != "" {
				return k
			}
		}
	}
	if k := s.resolve(s.kind(it)); k != "" {
		return k
	}
	return s.kind(it)
}

// statusFor is the outside status (from the mapping, or empty) and the
// kind for a stage key.
func (s *Sync) statusFor(stage string) (name, kind string) {
	kind = "todo"
	if s.project != nil {
		for _, st := range s.project.Stages {
			if st.Key == stage {
				kind = st.Kind
			}
		}
	}
	if stage == "todo" || stage == "doing" || stage == "review" || stage == "done" {
		if s.project == nil || len(s.project.Stages) == 0 {
			kind = stage
		}
	}
	for _, p := range s.pairs() {
		if s.resolve(p.stage) == stage {
			return p.outside, kind
		}
	}
	return "", kind
}

// StageFor is the project's stage key an outside item goes to, by the
// project's mapping or its kind, for a source's settings panel.
func (s *Sync) StageFor(it Item) string { return s.stageFor(it) }

// StageName is a stage key's name in the project.
func (s *Sync) StageName(key string) string { return s.stageName(key) }

func (s *Sync) stageName(key string) string {
	if s.project != nil {
		for _, st := range s.project.Stages {
			if st.Key == key {
				return st.Name
			}
		}
	}
	return key
}

// kind is where an outside item is: its own kind, or a guess from its
// status name.
func (s *Sync) kind(it Item) string {
	switch it.StatusKind {
	case "todo", "doing", "review", "done":
		return it.StatusKind
	}
	return GuessKind(it.Status)
}

// GuessKind guesses todo, doing, review or done from a status name.
func GuessKind(status string) string {
	st := strings.ToLower(status)
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(st, w) {
				return true
			}
		}
		return false
	}
	switch {
	case has("done", "closed", "complete", "cancel", "resolved", "merged", "shipped", "won't", "wont", "duplicate", "finished"):
		return "done"
	case has("review", "qa", "testing", "verify"):
		return "review"
	case has("progress", "doing", "started", "active", "working", "building"):
		return "doing"
	}
	return "todo"
}

// StageKind is the kind (todo, doing, review or done) of one of the
// project's stages, or "" when the project has no such stage.
func (s *Sync) StageKind(key string) string {
	if s.project != nil {
		for _, st := range s.project.Stages {
			if st.Key == key {
				return st.Kind
			}
		}
	}
	return ""
}

// Mapped reports whether the "stages" setting names this outside status,
// so a source can tell a status-like label from an ordinary one.
func (s *Sync) Mapped(status string) bool {
	for _, p := range s.pairs() {
		if strings.EqualFold(p.outside, status) {
			return true
		}
	}
	return false
}
