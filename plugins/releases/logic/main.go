// Command logic is the Release cycle plugin: releases on a cadence, with
// ship rules at cut-off. It is built for WASI and runs in Enplace's sandbox.
//
// It keeps each project's releases in its own storage, published as
// "public.releases" so plugins that build on it (App, Website, Release
// lanes) can read them. Work joins a release through the plugin's Release
// field. Plugins that know what's live in a store can publish
// "public.version" ({"version":"1.4"}) and "public.store_versions"
// ([{"version":"1.5","state":"in_review"}]) for their project, and the
// cycle numbers from there and follows the review.
//
// Its "Work ships" setting (or a plugin saying "public.ships":
// "continuous") can say work goes out as soon as it's done, like a website
// deployed on each fix. Then work in the Release field's "deploy" choice
// goes out when it's done, and each day's finished work is recorded as one
// deploy. Without an app shipping on the cadence beside it, no releases are
// planned.
//
// On a product it plans that product's releases; on the project it is one
// cycle shared by the products that have none of their own.
package main

import (
	"strconv"
	"time"

	"github.com/JustinGuedes/enplace-plugins/plugins/releases/cycle"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

func main() { plugin.Run(handle) }

const (
	keyReleases = "public.releases"
	keyStyle    = "style"
	keyCadence  = "cadence"
	keyAnchor   = "anchor"
	keyMode     = "public.mode"
	day         = "2006-01-02"
	// Values of the Release field besides a release's id.
	general   = ""          // never in a release
	unplanned = "unplanned" // ships, but isn't in a release yet
	deploy    = "deploy"    // ships as soon as it's done
	// shipsAsDone is the "Work ships" setting for work that goes out as
	// soon as it's done.
	shipsAsDone = "As each piece is done"
)

// project is one project's releases and the work that can be in them.
type project struct {
	plugin.Project
	set   cycle.Settings
	rels  []cycle.Release
	work  []plugin.Work
	items []cycle.Item
	live  string
	// continuous: a plugin says work here ships as soon as it's done.
	// cadence: releases are planned on the cadence (it's on, and nothing
	// but continuous work ships here, unless an app is on too).
	continuous bool
	cadence    bool
	// code is the version in the project's code, from a code host plugin.
	code *codeInfo
	// follows: a product taking its dates from its project's cycle.
	follows bool
}

// onlyContinuous is a project where everything ships as soon as it's done.
func (p *project) onlyContinuous() bool { return p.continuous && !p.cadence }

type cycleRun struct {
	req   *plugin.Request
	res   *plugin.Response
	loc   *time.Location
	now   time.Time
	today time.Time
	n     int
	// fresh is the releases worked out in this call, by project, and
	// anchors the day each one's cadence counts from.
	fresh   map[string][]cycle.Release
	anchors map[string]string
}

func handle(req *plugin.Request) (*plugin.Response, error) {
	c := &cycleRun{req: req, res: &plugin.Response{}}
	c.loc, _ = time.LoadLocation(req.Timezone)
	if c.loc == nil {
		c.loc = time.UTC
	}
	c.now = req.Now.In(c.loc)
	c.today = time.Date(c.now.Year(), c.now.Month(), c.now.Day(), 0, 0, 0, 0, time.UTC)
	switch req.Hook {
	case plugin.HookSchedule:
		for _, p := range req.Projects {
			if p.On {
				c.refresh(c.load(p.ID), true)
			}
		}
		c.calendar()
	case plugin.HookEvent:
		if p := c.load(req.ProjectID); p != nil {
			c.refresh(p, false)
			c.calendar()
		}
	case plugin.HookAction:
		if req.Action == nil {
			break
		}
		if req.Action.Name == "tool_releases" {
			c.res.Data = c.toolReleases()
			break
		}
		if p := c.load(req.ProjectID); p != nil && !req.ReadOnly {
			if err := c.action(p, req.Action); err != nil {
				c.res.Toast = err.Error()
				break
			}
			c.refresh(p, false)
			c.calendar()
		}
	case plugin.HookRender:
		c.render()
	case plugin.HookPage:
		switch req.Page {
		case "all":
			c.res.Page = c.allPage()
		case "releases":
			if p := c.load(req.ProjectID); p != nil {
				c.res.Page = c.projectPage(p)
			}
		}
	}
	return c.res, nil
}

func (c *cycleRun) field(key string) string { return c.req.Plugin.ID + "." + key }

func (c *cycleRun) newID() string {
	c.n++
	return "r" + strconv.FormatInt(c.req.Now.UnixNano(), 36) + strconv.Itoa(c.n)
}
