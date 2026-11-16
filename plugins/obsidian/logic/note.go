package main

// A work note is Markdown with YAML front matter:
//
//	---
//	enplace_id: ENP-42
//	stage: doing
//	priority: 3
//	tags: [plugins]
//	due: 2026-11-20
//	---
//	# Title
//	Body in Markdown…
//
// Only the keys below are Enplace's. Every other line of the front matter
// is the person's and is written back exactly as it was, in its place.
// "key" (what Markdown export writes) is read as enplace_id, so exported
// files can be linked as they are.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Enplace's keys, in the order they're added to a note that lacks them.
var ownKeys = []string{"enplace_id", "stage", "priority", "tags", "due"}

// entry is one top-level key of the front matter with its lines (the key's
// own line and any indented or list lines under it), kept verbatim.
type entry struct {
	key   string // "" for a comment or blank line
	lines []string
}

// note is a parsed work note.
type note struct {
	hasFront bool
	front    []entry
	// heading is the "# " line's text; hasHeading says there was one.
	heading    string
	hasHeading bool
	body       string
}

var keyLine = regexp.MustCompile(`^([A-Za-z0-9_][A-Za-z0-9_ -]*):(\s.*)?$`)

// parseNote reads a note. It never fails: text it can't read as front
// matter is the body.
func parseNote(text string) *note {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	n := &note{}
	rest := text
	if strings.HasPrefix(text, "---\n") {
		lines := strings.Split(text[4:], "\n")
		for i, l := range lines {
			if l == "---" || l == "..." {
				n.hasFront = true
				n.front = parseFront(lines[:i])
				rest = strings.Join(lines[i+1:], "\n")
				break
			}
		}
	}
	rest = strings.TrimLeft(rest, "\n")
	if strings.HasPrefix(rest, "# ") {
		line, after, _ := strings.Cut(rest, "\n")
		n.heading, n.hasHeading = strings.TrimSpace(line[2:]), true
		rest = after
	}
	n.body = strings.Trim(rest, "\n")
	return n
}

func parseFront(lines []string) []entry {
	var out []entry
	for _, l := range lines {
		if m := keyLine.FindStringSubmatch(l); m != nil {
			out = append(out, entry{key: strings.ToLower(strings.TrimSpace(m[1])), lines: []string{l}})
			continue
		}
		cont := strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t") || strings.HasPrefix(l, "- ") || l == "-"
		if cont && len(out) > 0 && out[len(out)-1].key != "" {
			out[len(out)-1].lines = append(out[len(out)-1].lines, l)
			continue
		}
		out = append(out, entry{lines: []string{l}})
	}
	return out
}

// find is the entry for a key (enplace_id also answers to "key").
func (n *note) find(key string) *entry {
	for i := range n.front {
		if n.front[i].key == key {
			return &n.front[i]
		}
	}
	if key == "enplace_id" {
		return n.find("key")
	}
	return nil
}

// value is a key's value as text, with quotes taken off.
func (n *note) value(key string) string {
	e := n.find(key)
	if e == nil {
		return ""
	}
	_, v, _ := strings.Cut(e.lines[0], ":")
	return unquote(strings.TrimSpace(stripComment(v)))
}

// list is a key's value as a list: [a, b], a block of "- a" lines, or
// one value split by commas.
func (n *note) list(key string) []string {
	e := n.find(key)
	if e == nil {
		return nil
	}
	var out []string
	add := func(v string) {
		if v = strings.TrimPrefix(unquote(strings.TrimSpace(v)), "#"); v != "" {
			out = append(out, v)
		}
	}
	_, v, _ := strings.Cut(e.lines[0], ":")
	v = strings.TrimSpace(stripComment(v))
	switch {
	case strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]"):
		for _, x := range splitFlow(v[1 : len(v)-1]) {
			add(x)
		}
	case v != "":
		for _, x := range strings.Split(v, ",") {
			add(x)
		}
	}
	for _, l := range e.lines[1:] {
		if t := strings.TrimSpace(l); strings.HasPrefix(t, "- ") {
			add(stripComment(t[2:]))
		}
	}
	return out
}

// splitFlow splits a YAML flow list's inside by commas outside quotes.
func splitFlow(s string) []string {
	var out []string
	var cur strings.Builder
	var q rune
	for _, r := range s {
		switch {
		case q != 0:
			if r == q {
				q = 0
			}
		case r == '"' || r == '\'':
			q = r
		case r == ',':
			out = append(out, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteRune(r)
	}
	return append(out, cur.String())
}

func stripComment(v string) string {
	if strings.HasPrefix(strings.TrimSpace(v), "\"") || strings.HasPrefix(strings.TrimSpace(v), "'") {
		return v
	}
	if i := strings.Index(v, " #"); i >= 0 {
		return v[:i]
	}
	return v
}

func unquote(v string) string {
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		var s string
		if json.Unmarshal([]byte(v), &s) == nil {
			return s
		}
		return v[1 : len(v)-1]
	}
	if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
		return strings.ReplaceAll(v[1:len(v)-1], "''", "'")
	}
	return v
}

// set puts one of Enplace's keys: in its place when the note has it, else
// after the other Enplace keys (or first). An empty value takes it out.
func (n *note) set(key, line string) {
	n.hasFront = true
	if e := n.find(key); e != nil {
		if line == "" {
			n.remove(e)
			return
		}
		e.key, e.lines = key, []string{line}
		return
	}
	if line == "" {
		return
	}
	at := 0
	for i, k := range ownKeys {
		if k == key {
			break
		}
		for j := range n.front {
			if n.front[j].key == ownKeys[i] || (ownKeys[i] == "enplace_id" && n.front[j].key == "key") {
				at = max(at, j+1)
			}
		}
	}
	n.front = append(n.front[:at], append([]entry{{key: key, lines: []string{line}}}, n.front[at:]...)...)
}

func (n *note) remove(e *entry) {
	for i := range n.front {
		if &n.front[i] == e {
			n.front = append(n.front[:i], n.front[i+1:]...)
			return
		}
	}
}

// yamlValue quotes a value only when YAML would read it as something else.
func yamlValue(v string) string {
	if v == "" || strings.ContainsAny(v, ":#[]{},&*!|>'\"%@`\n") || strings.TrimSpace(v) != v || strings.HasPrefix(v, "-") || strings.HasPrefix(v, "?") {
		b, _ := json.Marshal(v)
		return string(b)
	}
	switch strings.ToLower(v) {
	case "true", "false", "yes", "no", "null", "~", "on", "off":
		b, _ := json.Marshal(v)
		return string(b)
	}
	return v
}

func tagsLine(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	parts := make([]string, len(tags))
	for i, t := range tags {
		parts[i] = yamlValue(t)
	}
	return "tags: [" + strings.Join(parts, ", ") + "]"
}

// String writes the note back.
func (n *note) String() string {
	var b strings.Builder
	if n.hasFront && len(n.front) > 0 {
		b.WriteString("---\n")
		for _, e := range n.front {
			for _, l := range e.lines {
				b.WriteString(l + "\n")
			}
		}
		b.WriteString("---\n")
	}
	if n.hasHeading {
		b.WriteString("# " + n.heading + "\n")
		if n.body != "" {
			b.WriteString("\n")
		}
	}
	if n.body != "" {
		b.WriteString(n.body + "\n")
	}
	return b.String()
}

var priorityWords = map[string]int{"none": 0, "low": 1, "medium": 2, "med": 2, "normal": 2, "high": 3, "urgent": 4}

// priority reads 0 to 4, or a word like high.
func (n *note) priority() int {
	v := strings.ToLower(n.value("priority"))
	if p, err := strconv.Atoi(v); err == nil && p >= 0 && p <= 4 {
		return p
	}
	return priorityWords[v]
}

var dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}`)

func (n *note) due() string {
	return dateRe.FindString(n.value("due"))
}

func idLine(key string) string { return "enplace_id: " + key }

func priorityLine(p int) string {
	if p <= 0 {
		return ""
	}
	return fmt.Sprintf("priority: %d", p)
}
