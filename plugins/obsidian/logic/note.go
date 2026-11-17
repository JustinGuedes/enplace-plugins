package main

// A work note is Markdown with YAML front matter:
//
//	---
//	enplace_id: ENP-42
//	stage: doing
//	priority: 3
//	assignee: Byron
//	tags: [plugins]
//	due: 2026-11-20
//	---
//	# Title
//	Body in Markdown…
//
// Only the keys Enplace syncs are Enplace's. Every other line of the front
// matter is the person's and is written back exactly as it was, in its
// place. "key" (what Markdown export writes) is read as enplace_id, so
// exported files can be linked as they are. Keys may be quoted
// ("status": "Done"), as some vaults write them; a quoted key is written
// back quoted, with its value as JSON, so the note keeps its style.

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// Enplace's keys, in the order they're added to a note that lacks them.
var ownKeys = []string{"enplace_id", "id", "stage", "status", "priority", "assignee", "tags", "due", "updated"}

// entry is one top-level key of the front matter with its lines (the key's
// own line and any indented or list lines under it), kept verbatim.
type entry struct {
	key    string // "" for a comment or blank line
	quoted bool   // the key was written "in quotes"
	lines  []string
}

// note is a parsed work note.
type note struct {
	hasFront bool
	front    []entry
	// heading is the "# " line's text; hasHeading says there was one.
	heading    string
	hasHeading bool
	body       string
	// The blank lines around the heading and at the end, as the note had
	// them, so a note Enplace changes keeps its layout.
	laid             bool
	lead, gap, trail string
}

var keyLine = regexp.MustCompile(`^(?:"([^"]+)"|'([^']+)'|([A-Za-z0-9_][A-Za-z0-9_ -]*)):(\s.*)?$`)

// parseNote reads a note. It never fails: text it can't read as front
// matter is the body.
func parseNote(text string) *note {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	n := &note{laid: true}
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
	trimmed := strings.TrimLeft(rest, "\n")
	n.lead, rest = rest[:len(rest)-len(trimmed)], trimmed
	if strings.HasPrefix(rest, "# ") {
		line, after, _ := strings.Cut(rest, "\n")
		n.heading, n.hasHeading = strings.TrimSpace(line[2:]), true
		trimmed = strings.TrimLeft(after, "\n")
		n.gap, rest = after[:len(after)-len(trimmed)], trimmed
	}
	trimmed = strings.TrimRight(rest, "\n")
	n.body, n.trail = trimmed, rest[len(trimmed):]
	if n.body == "" {
		n.gap, n.trail = "\n", "\n" // for a body added later
	}
	return n
}

func parseFront(lines []string) []entry {
	var out []entry
	for _, l := range lines {
		if m := keyLine.FindStringSubmatch(l); m != nil {
			k := m[1] + m[2] + m[3]
			out = append(out, entry{key: strings.ToLower(strings.TrimSpace(k)), quoted: m[3] == "", lines: []string{l}})
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

// value is a key's value as text, with quotes taken off. YAML's null is
// no value.
func (n *note) value(key string) string {
	e := n.find(key)
	if e == nil {
		return ""
	}
	_, v, _ := strings.Cut(e.lines[0][len(rawKey(e.lines[0])):], ":")
	v = strings.TrimSpace(stripComment(v))
	if v == "null" || v == "~" {
		return ""
	}
	return unquote(v)
}

// rawKey is the key part of a key line, quotes and all.
func rawKey(line string) string {
	if line != "" && (line[0] == '"' || line[0] == '\'') {
		if i := strings.IndexByte(line[1:], line[0]); i >= 0 {
			return line[:i+2]
		}
	}
	k, _, _ := strings.Cut(line, ":")
	return k
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
	v := strings.TrimSpace(stripComment(n.rawValue(e)))
	switch {
	case v == "null" || v == "~":
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

func (n *note) rawValue(e *entry) string {
	_, v, _ := strings.Cut(e.lines[0][len(rawKey(e.lines[0])):], ":")
	return v
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

// set puts one of Enplace's keys, in the note's own style: in its place
// when the note has it, else after the other Enplace keys (or first).
// The value is a string, a number or a list of strings. nil takes the key
// out, unless keep is set: then it stays, empty.
func (n *note) set(key string, v any, keep bool) {
	n.hasFront = true
	e := n.find(key)
	quoted := n.quotes()
	if e != nil {
		quoted = e.quoted
	}
	if v == nil && !keep {
		if e != nil {
			n.remove(e)
		}
		return
	}
	line := render(key, v, quoted)
	if e != nil {
		if k := rawKey(e.lines[0]); k != "" {
			line = k + line[len(rawKey(line)):] // the key as the note spells it
		}
		e.lines = []string{line}
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
	n.front = append(n.front[:at], append([]entry{{key: key, quoted: quoted, lines: []string{line}}}, n.front[at:]...)...)
}

// quotes says the note writes its keys in quotes, mostly.
func (n *note) quotes() bool {
	q, all := 0, 0
	for _, e := range n.front {
		if e.key != "" {
			all++
			if e.quoted {
				q++
			}
		}
	}
	return all > 0 && q*2 > all
}

// render is one key line: plain YAML, or quoted key and JSON value.
func render(key string, v any, quoted bool) string {
	if quoted {
		k, _ := json.Marshal(key)
		var val []byte
		switch x := v.(type) {
		case nil:
			val = []byte("null")
		case []string:
			if x == nil {
				x = []string{}
			}
			val, _ = json.Marshal(x)
		case int:
			val = []byte(strconv.Itoa(x))
		default:
			val, _ = json.Marshal(v)
		}
		return string(k) + ": " + string(val)
	}
	switch x := v.(type) {
	case nil:
		return key + ":"
	case []string:
		parts := make([]string, len(x))
		for i, t := range x {
			parts[i] = yamlValue(t)
		}
		return key + ": [" + strings.Join(parts, ", ") + "]"
	case int:
		return key + ": " + strconv.Itoa(x)
	case string:
		return key + ": " + yamlValue(x)
	}
	return key + ":"
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

// String writes the note back, in the layout it was read with.
func (n *note) String() string {
	lead, gap, trail := n.lead, n.gap, n.trail
	if !n.laid {
		lead, gap, trail = "", "\n", "\n"
	}
	var b strings.Builder
	if n.hasFront && len(n.front) > 0 {
		b.WriteString("---\n")
		for _, e := range n.front {
			for _, l := range e.lines {
				b.WriteString(l + "\n")
			}
		}
		b.WriteString("---\n")
	} else {
		lead = ""
	}
	b.WriteString(lead)
	if n.hasHeading {
		b.WriteString("# " + n.heading + "\n")
		if n.body == "" {
			return b.String()
		}
		b.WriteString(gap)
	}
	if n.body != "" {
		b.WriteString(n.body + trail)
	}
	return b.String()
}

var priorityWords = map[string]int{"none": 0, "low": 1, "medium": 2, "med": 2, "normal": 2, "high": 3, "urgent": 4}

// pScale is a "P1" style priority: P0 urgent, P1 high, P2 medium, P3 low.
var pScale = regexp.MustCompile(`^[Pp]([0-4])$`)

// priority reads 0 to 4, a word like high, or P0 to P4.
func (n *note) priority() int {
	v := strings.ToLower(n.value("priority"))
	if p, err := strconv.Atoi(v); err == nil && p >= 0 && p <= 4 {
		return p
	}
	if m := pScale.FindStringSubmatch(v); m != nil {
		return max(4-int(m[1][0]-'0'), 1)
	}
	return priorityWords[v]
}

// priorityValue is a priority to write, in the scale the note uses.
func (n *note) priorityValue(p int) any {
	if p <= 0 {
		return nil
	}
	if pScale.MatchString(n.value("priority")) {
		return "P" + strconv.Itoa(4-p)
	}
	return p
}

var dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}`)

func (n *note) due() string {
	return dateRe.FindString(n.value("due"))
}
