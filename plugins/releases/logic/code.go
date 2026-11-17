package main

import (
	"strings"

	"github.com/JustinGuedes/enplace-plugins/plugins/releases/cycle"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

// codeInfo is what's known about the project's code: the version in it
// (from Versions, or from an older code plugin that read it itself) and
// the repo, branch and tags a code plugin published as "public.code".
type codeInfo struct {
	Provider   string `json:"provider"`
	Repo       string `json:"repo"`
	URL        string `json:"url"`
	Branch     string `json:"branch"`
	File       string `json:"file"`
	Version    string `json:"version"`
	Tag        string `json:"tag"`
	TagVersion string `json:"tag_version"`
	Ahead      int    `json:"ahead"`
}

// code is what the linked plugins know about the project's code, or nil:
// the version Versions read from it, and the repo, branch and tags a code
// plugin (like GitHub) published as "public.code". For one of a project's
// products ("<project id>/<key>"), the project's code counts when the
// product has no code of its own; for a project's shared cycle, the code
// of the products sharing it does.
func (c *cycleRun) code(pid string) *codeInfo {
	scopes := c.riding(pid)
	if parent, _, ok := strings.Cut(pid, "/"); ok {
		scopes = append(scopes, parent)
	}
	var found *codeInfo
find:
	for _, scope := range scopes {
		for _, id := range sortedKeys(c.req.Peers) {
			var ci codeInfo
			if c.req.Peers[id].GetProject(scope, "public.code", &ci) && ci.Repo != "" {
				found = &ci
				break find
			}
		}
	}
	if v := c.versionsPlugin(pid); v != nil && v.Version != "" {
		if found == nil {
			found = &codeInfo{}
		}
		found.Version, found.File = v.Version, v.File
	}
	if found == nil || found.Version == "" {
		return nil
	}
	return found
}

// codeLine compares the code's version with release r, like "The code is
// at 1.5, ready for this release." It's a text block, or nil without code.
func (c *cycleRun) codeLine(p *project, r *cycle.Release) *plugin.Block {
	ci := p.code
	if ci == nil {
		return nil
	}
	file := ci.File
	if file == "" {
		file = "the code"
	}
	b := &plugin.Block{Type: plugin.BlockText, Tone: "muted"}
	switch {
	case r == nil:
		b.Text = "The code says " + ci.Version + "."
	case sameVersion(ci.Version, r.Version):
		b.Text, b.Tone = "The code is at "+ci.Version+", ready for this release.", "good"
	case p.live != "" && sameVersion(ci.Version, p.live):
		b.Text = "The code still says " + ci.Version + ". Bump it to " + r.Version + " in " + file + " before it ships."
		// Fine early in the cycle; after cut-off it's worth a look.
		if r.Cutoff != "" && r.Cutoff <= c.today.Format(day) {
			b.Tone = "warn"
		}
	default:
		b.Text, b.Tone = "The code says "+ci.Version+", but this release is "+r.Version+".", "warn"
	}
	return b
}

// sameVersion says whether "1.4", "v1.4" and "1.4.0" name one release.
func sameVersion(a, b string) bool {
	na, nb := versionParts(a), versionParts(b)
	if na == nil || nb == nil {
		return strings.TrimPrefix(a, "v") == strings.TrimPrefix(b, "v")
	}
	for len(na) < len(nb) {
		na = append(na, "0")
	}
	for len(nb) < len(na) {
		nb = append(nb, "0")
	}
	for i := range na {
		if strings.TrimLeft(na[i], "0") != strings.TrimLeft(nb[i], "0") {
			return false
		}
	}
	return true
}

func versionParts(v string) []string {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+ "); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	for _, p := range parts {
		if p == "" || strings.Trim(p, "0123456789") != "" {
			return nil
		}
	}
	return parts
}
