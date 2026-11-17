package bundle

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
)

// Role is a job on the person's team that a role plugin brings, like
// Designer or App shipper. Adding it to the team makes a role that takes
// the work it says, tells the agent playing it the Brief on each item, and
// hands that agent the plugin's skills and those of the plugins in Uses.
type Role struct {
	// Name is the role's name on the team, up to 40 characters. Left out,
	// it's the plugin's name.
	Name string `json:"name,omitempty"`
	// About says what it does in one short line, like "Mockups, icons and
	// screens".
	About string `json:"about,omitempty"`
	// Icon is one of Enplace's line icons; Tone tints it.
	Icon string `json:"icon,omitempty"`
	Tone string `json:"tone,omitempty"`
	// Takes is the work it takes: items with any of Tags, or Anything no
	// other role takes.
	Takes RoleTakes `json:"takes"`
	// Brief is what the agent playing it is told to do with each item.
	Brief string `json:"brief"`
	// Uses names skills plugins whose skills its agent gets too, so a role
	// can be built from skills that already exist.
	Uses []string `json:"uses,omitempty"`
}

// RoleTakes is the work a role takes.
type RoleTakes struct {
	Tags     []string `json:"tags,omitempty"`
	Anything bool     `json:"anything,omitempty"`
}

// RoleTones are the tints a role's icon can have.
var RoleTones = []string{"accent", "info", "ok", "warn", "danger", "muted"}

var tagRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,29}$`)

func (r *Role) check() error {
	if r == nil {
		return nil
	}
	if len(r.Name) > 40 || len(r.About) > 80 {
		return errors.New("role: name is up to 40 characters and about up to 80")
	}
	if r.Brief == "" || len(r.Brief) > 2000 {
		return errors.New("role: brief says what its agent does with each item, up to 2000 characters")
	}
	if r.Tone != "" && !slices.Contains(RoleTones, r.Tone) {
		return fmt.Errorf("role: tone is one of %v", RoleTones)
	}
	if r.Icon != "" && !keyRe.MatchString(r.Icon) {
		return errors.New("role: icon names one of Enplace's icons")
	}
	if !r.Takes.Anything && len(r.Takes.Tags) == 0 {
		return errors.New("role: takes needs tags, or anything")
	}
	if len(r.Takes.Tags) > 12 {
		return errors.New("role: takes at most 12 tags")
	}
	for _, t := range r.Takes.Tags {
		if !tagRe.MatchString(t) {
			return fmt.Errorf("role: tag %q is lowercase letters, digits or dashes", t)
		}
	}
	if len(r.Uses) > 8 {
		return errors.New("role: uses at most 8 skills plugins")
	}
	for _, id := range r.Uses {
		if !idRe.MatchString(id) {
			return fmt.Errorf("role: uses %q, which isn't a plugin id", id)
		}
	}
	return nil
}
