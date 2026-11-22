package cloud

import (
	"errors"
	"net/url"
	"strings"

	"github.com/JustinGuedes/enplace-plugins/plugins/app/store"
)

// OwnName is the workflow Enplace makes and looks after in each app.
const OwnName = "Enplace store build"

const ownDescription = "Made by Enplace. It archives the app when Enplace asks for a build. Enplace puts it back if it's changed here: change its settings in Enplace instead."

// Audiences say what an archive is made for, in Apple's words.
const (
	ForStore    = "APP_STORE_ELIGIBLE" // TestFlight (internal and external) and the App Store
	ForInternal = "INTERNAL_ONLY"      // internal TestFlight testers only
	ForNobody   = ""                   // not uploaded
)

// Action is one action of a workflow, as Apple describes it.
type Action struct {
	Name        string  `json:"name"`
	ActionType  string  `json:"actionType"`
	Scheme      string  `json:"scheme,omitempty"`
	Platform    string  `json:"platform,omitempty"`
	Destination *string `json:"destination,omitempty"`
	Audience    *string `json:"buildDistributionAudience"`
	Required    bool    `json:"isRequiredToPass"`
}

// Detail is a workflow with what Enplace needs to copy or check.
type Detail struct {
	ID        string
	Name      string
	Enabled   bool
	Container string
	Actions   []Action
	Manual    bool
	// Branch is the first branch its "branch changes" start condition
	// names exactly, like main.
	Branch string
	// Repo, Xcode and MacOS are the IDs it builds with.
	Repo, Xcode, MacOS string
}

type pattern struct {
	Pattern  string `json:"pattern"`
	IsPrefix bool   `json:"isPrefix"`
}

type patterns struct {
	IsAllMatch bool      `json:"isAllMatch"`
	Patterns   []pattern `json:"patterns,omitempty"`
}

type workflowAttrs struct {
	Name      string   `json:"name"`
	Enabled   bool     `json:"isEnabled"`
	Container string   `json:"containerFilePath"`
	Actions   []Action `json:"actions"`
	Branch    *struct {
		Source patterns `json:"source"`
	} `json:"branchStartCondition"`
	Manual *struct {
		Source patterns `json:"source"`
	} `json:"manualBranchStartCondition"`
}

// Workflow reads one workflow in full.
func (c *Client) Workflow(id string) (Detail, error) {
	var res struct {
		Data struct {
			ID            string                  `json:"id"`
			Attributes    workflowAttrs           `json:"attributes"`
			Relationships map[string]relationship `json:"relationships"`
		} `json:"data"`
	}
	if err := c.Do("GET", "/v1/ciWorkflows/"+url.PathEscape(id)+"?include=repository,xcodeVersion,macOsVersion", nil, &res); err != nil {
		return Detail{}, err
	}
	a := res.Data.Attributes
	d := Detail{ID: res.Data.ID, Name: a.Name, Enabled: a.Enabled, Container: a.Container, Actions: a.Actions, Manual: a.Manual != nil}
	if a.Branch != nil {
		for _, p := range a.Branch.Source.Patterns {
			if !p.IsPrefix && p.Pattern != "" {
				d.Branch = p.Pattern
				break
			}
		}
	}
	d.Repo = res.Data.Relationships["repository"].id()
	d.Xcode = res.Data.Relationships["xcodeVersion"].id()
	d.MacOS = res.Data.Relationships["macOsVersion"].id()
	return d, nil
}

type relationship struct {
	Data *store.Ref `json:"data"`
}

func (r relationship) id() string {
	if r.Data == nil {
		return ""
	}
	return r.Data.ID
}

// Want is the workflow Enplace keeps, copied from one Xcode made (its
// project file, scheme and platform), archiving for the audience.
func Want(from Detail, audience string) (Detail, error) {
	if from.Container == "" {
		return Detail{}, errors.New("the " + from.Name + " workflow doesn't say which project to build")
	}
	var src *Action
	for i, a := range from.Actions {
		if a.ActionType == "ARCHIVE" && a.Scheme != "" {
			src = &from.Actions[i]
			break
		}
	}
	if src == nil {
		for i, a := range from.Actions {
			if a.Scheme != "" {
				src = &from.Actions[i]
				break
			}
		}
	}
	if src == nil {
		return Detail{}, errors.New("the " + from.Name + " workflow has no scheme to copy: give it an action in Xcode, or make the Default workflow again")
	}
	platform := src.Platform
	if platform == "" {
		platform = "IOS"
	}
	act := Action{Name: "Archive - " + platformName(platform), ActionType: "ARCHIVE", Scheme: src.Scheme, Platform: platform, Required: true}
	if audience != ForNobody {
		a := audience
		act.Audience = &a
	}
	return Detail{
		Name: OwnName, Enabled: true, Container: from.Container, Actions: []Action{act}, Manual: true,
		Branch: from.Branch, Repo: from.Repo, Xcode: from.Xcode, MacOS: from.MacOS,
	}, nil
}

func platformName(p string) string {
	switch p {
	case "IOS":
		return "iOS"
	case "MACOS":
		return "macOS"
	case "TVOS":
		return "tvOS"
	case "WATCHOS":
		return "watchOS"
	case "VISIONOS":
		return "visionOS"
	}
	return p
}

// Same says the workflow already is what Enplace wants.
func Same(have, want Detail) bool {
	if !have.Enabled || !have.Manual || have.Container != want.Container || len(have.Actions) != len(want.Actions) {
		return false
	}
	for i, a := range have.Actions {
		b := want.Actions[i]
		if a.ActionType != b.ActionType || a.Scheme != b.Scheme || a.Platform != b.Platform || val(a.Audience) != val(b.Audience) {
			return false
		}
	}
	return true
}

func val(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (w Detail) attributes() map[string]any {
	return map[string]any{
		"name": w.Name, "description": ownDescription, "isEnabled": true, "clean": false,
		"containerFilePath": w.Container, "actions": w.Actions,
		"manualBranchStartCondition": map[string]any{"source": patterns{IsAllMatch: true}},
	}
}

// Make creates the workflow in the product.
func (c *Client) Make(productID string, w Detail) (string, error) {
	if w.Repo == "" || w.Xcode == "" || w.MacOS == "" {
		return "", errors.New("Enplace couldn't see which repo, Xcode and macOS to use. Open the Default workflow in Xcode once and save it")
	}
	rel := map[string]any{
		"product":      map[string]any{"data": store.Ref{Type: "ciProducts", ID: productID}},
		"repository":   map[string]any{"data": store.Ref{Type: "scmRepositories", ID: w.Repo}},
		"xcodeVersion": map[string]any{"data": store.Ref{Type: "ciXcodeVersions", ID: w.Xcode}},
		"macOsVersion": map[string]any{"data": store.Ref{Type: "ciMacOsVersions", ID: w.MacOS}},
	}
	var out struct {
		Data store.Ref `json:"data"`
	}
	err := c.Do("POST", "/v1/ciWorkflows", map[string]any{"data": map[string]any{"type": "ciWorkflows", "attributes": w.attributes(), "relationships": rel}}, &out)
	return out.Data.ID, err
}

// Fix puts the workflow back the way Enplace wants it.
func (c *Client) Fix(id string, w Detail) error {
	return c.Do("PATCH", "/v1/ciWorkflows/"+url.PathEscape(id), map[string]any{"data": map[string]any{"type": "ciWorkflows", "id": id, "attributes": w.attributes()}}, nil)
}

// Source is the workflow to copy from: Xcode's Default, else the first
// other workflow that isn't Enplace's.
func Source(ws []Workflow) (Workflow, bool) {
	for _, w := range ws {
		if strings.EqualFold(w.Name, "Default") {
			return w, true
		}
	}
	for _, w := range ws {
		if w.Name != OwnName {
			return w, true
		}
	}
	return Workflow{}, false
}

// Own is Enplace's workflow, when there is one.
func Own(ws []Workflow) (Workflow, bool) {
	for _, w := range ws {
		if w.Name == OwnName {
			return w, true
		}
	}
	return Workflow{}, false
}

// Redo puts the workflow back the way Enplace wants it and moves it to the
// Xcode and macOS versions of the workflow it was copied from.
func (c *Client) Redo(id string, w Detail) error {
	data := map[string]any{"type": "ciWorkflows", "id": id, "attributes": w.attributes()}
	rel := map[string]any{}
	if w.Xcode != "" {
		rel["xcodeVersion"] = map[string]any{"data": store.Ref{Type: "ciXcodeVersions", ID: w.Xcode}}
	}
	if w.MacOS != "" {
		rel["macOsVersion"] = map[string]any{"data": store.Ref{Type: "ciMacOsVersions", ID: w.MacOS}}
	}
	if len(rel) > 0 {
		data["relationships"] = rel
	}
	return c.Do("PATCH", "/v1/ciWorkflows/"+url.PathEscape(id), map[string]any{"data": data}, nil)
}

// Changes says, in a few words each, how have differs from want.
func Changes(have, want Detail) []string {
	var out []string
	if !have.Enabled {
		out = append(out, "switched it back on")
	}
	if !have.Manual {
		out = append(out, "starts only when Enplace asks")
	}
	if have.Container != want.Container {
		out = append(out, "builds "+want.Container)
	}
	var h, w Action
	if len(have.Actions) > 0 {
		h = have.Actions[0]
	}
	if len(want.Actions) > 0 {
		w = want.Actions[0]
	}
	if len(have.Actions) != len(want.Actions) || h.ActionType != w.ActionType {
		out = append(out, "only archives")
	}
	if h.Scheme != w.Scheme {
		out = append(out, "scheme "+w.Scheme)
	}
	if h.Platform != w.Platform {
		out = append(out, "for "+platformName(w.Platform))
	}
	if val(h.Audience) != val(w.Audience) {
		out = append(out, "archives "+AudienceName(val(w.Audience)))
	}
	if want.Xcode != "" && have.Xcode != want.Xcode {
		out = append(out, "Default's Xcode version")
	}
	if want.MacOS != "" && have.MacOS != want.MacOS {
		out = append(out, "Default's macOS version")
	}
	return out
}

// AudienceName says what an audience is for, in the setting's words.
func AudienceName(a string) string {
	switch a {
	case ForStore:
		return "for the App Store and TestFlight"
	case ForInternal:
		return "for TestFlight internal testers only"
	}
	return "without uploading"
}
