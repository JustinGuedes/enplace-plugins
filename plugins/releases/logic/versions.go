package main

import "strings"

// The Versions plugin owns how releases are numbered and reads the
// version from the code. It publishes, on a project or product:
//
//	"public.versions": {"version":"1.5","build":"42","file":"Version.xcconfig",
//	                    "style":"short","bump":"minor","next_version":"1.0"}
//
// Without it, the numbering this plugin had before (its old Version style,
// Each release bumps and Next version settings, if they were set) carries
// on, and is published as "public.numbering" so Versions starts from it.

type versionsInfo struct {
	Version     string `json:"version"`
	Build       string `json:"build"`
	File        string `json:"file"`
	Style       string `json:"style"`
	Bump        string `json:"bump"`
	NextVersion string `json:"next_version"`
}

// numbering is the numbering this plugin's old settings set.
type numbering struct {
	Style       string `json:"style"`
	Bump        string `json:"bump"`
	NextVersion string `json:"next_version"`
}

// versionsPlugin is what Versions published for the cycle: on its own project or
// product, a product sharing it, or the project a product is in.
func (c *cycleRun) versionsPlugin(pid string) *versionsInfo {
	scopes := c.riding(pid)
	if parent, _, ok := strings.Cut(pid, "/"); ok {
		scopes = append(scopes, parent)
	}
	for _, scope := range scopes {
		for _, id := range sortedKeys(c.req.Peers) {
			var v versionsInfo
			if c.req.Peers[id].GetProject(scope, "public.versions", &v) && v.Style != "" {
				return &v
			}
		}
	}
	return nil
}

// numberBy takes the numbering from Versions when it's on.
func (c *cycleRun) numberBy(p *project) {
	v := c.versionsPlugin(p.ID)
	if v == nil {
		return
	}
	p.set.Scheme, p.set.Bump, p.set.NextVersion = v.Style, v.Bump, v.NextVersion
	p.set = p.set.Tidy()
}

// oldNumbering is what this plugin's old numbering settings say, or nil
// when none of them was set.
func oldNumbering(m map[string]any) *numbering {
	if m["scheme"] == nil && m["bump"] == nil && m["next_version"] == nil {
		return nil
	}
	s := settings(m)
	return &numbering{Style: s.Scheme, Bump: s.Bump, NextVersion: s.NextVersion}
}
