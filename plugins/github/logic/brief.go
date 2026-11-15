package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

// The daily stuck check (ENP-106): one sentence in Today's brief naming
// the work whose code is stuck, so nothing sits unnoticed. Its fix is
// one tap away on each item's row and card.

// stuck is the open work whose code can't move on by itself, by what's
// wrong, across every project and product the plugin is on.
type stuck struct {
	conflicts, failing, behind, nopr []string
}

func (r *run) stuckWork() stuck {
	open := map[string]bool{}
	for _, w := range r.req.Work {
		if w.StageKind != "done" {
			open[strings.ToUpper(w.Key)] = true
		}
	}
	var out stuck
	seen := map[string]bool{}
	scopes := make([]string, 0, len(r.req.State.Projects))
	for pid := range r.req.State.Projects {
		scopes = append(scopes, pid)
	}
	sort.Strings(scopes)
	for _, pid := range scopes {
		ls := Links{}
		if !r.req.State.GetProject(pid, keyLinks, &ls) {
			continue
		}
		for _, k := range sortedKeys(ls) {
			if !open[k] || seen[k] {
				continue
			}
			var why string
			for _, l := range ls[k] {
				switch {
				case l.Kind == "branch" && why == "":
					why = "nopr"
				case l.Kind == "pr":
					switch l.mergeStatus() {
					case "conflicts", "failing", "behind":
						why = l.mergeStatus()
					}
				}
			}
			if why == "" {
				continue
			}
			seen[k] = true
			switch why {
			case "conflicts":
				out.conflicts = append(out.conflicts, k)
			case "failing":
				out.failing = append(out.failing, k)
			case "behind":
				out.behind = append(out.behind, k)
			default:
				out.nopr = append(out.nopr, k)
			}
		}
	}
	return out
}

// brief says what's stuck, or nothing when nothing is.
func (r *run) brief() {
	s := r.stuckWork()
	var parts []string
	say := func(keys []string, one, many string) {
		if len(keys) == 0 {
			return
		}
		verb := one
		if len(keys) > 1 {
			verb = many
		}
		parts = append(parts, keyList(keys)+" "+verb)
	}
	say(s.conflicts, "has conflicts", "have conflicts")
	say(s.failing, "has failing checks", "have failing checks")
	say(s.behind, "is behind its base branch", "are behind their base branch")
	say(s.nopr, "has code with no pull request", "have code with no pull request")
	if len(parts) == 0 {
		return
	}
	text := "Code waiting on a fix: " + joinWords(parts) + "."
	first := append(append(append(append([]string{}, s.conflicts...), s.failing...), s.behind...), s.nopr...)[0]
	r.res.Brief = &plugin.Brief{Text: text, Button: &plugin.Button{Label: "Open " + first, Href: "/?item=" + first}}
	r.res.Signal(plugin.SlotBrief, plugin.LevelLoud, "Code waiting on a fix")
}

// keyList is up to three keys, then how many more: "ENP-1, ENP-2 and 3 more".
func keyList(keys []string) string {
	if len(keys) > 3 {
		return strings.Join(keys[:3], ", ") + fmt.Sprintf(" and %d more", len(keys)-3)
	}
	return joinWords(keys)
}

func joinWords(list []string) string {
	switch len(list) {
	case 0:
		return ""
	case 1:
		return list[0]
	}
	return strings.Join(list[:len(list)-1], ", ") + " and " + list[len(list)-1]
}
