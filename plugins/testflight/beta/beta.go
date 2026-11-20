// Package beta reads and changes an app's TestFlight through the App Store
// Connect API: its builds, a tester group and its testers. Like the App
// Store plugin's store package it's free of Enplace's plugin calls (the
// caller hands it a way to call), so it can be tested plainly.
package beta

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/JustinGuedes/enplace-plugins/plugins/app/store"
)

// Client is App Store Connect, signed in with the App Store plugin's key.
type Client struct{ *store.Client }

// New signs in with the App Store Connect key, the same key the App Store
// plugin uses.
func New(call store.Call, issuer, keyID, pemKey string, now time.Time) (*Client, error) {
	c, err := store.NewClient(call, issuer, keyID, pemKey, now)
	if err != nil {
		return nil, err
	}
	return &Client{c}, nil
}

type ref = store.Ref

type one struct {
	Data *ref `json:"data"`
}

type many struct {
	Data []ref `json:"data"`
}

// Build is one uploaded build, in TestFlight's terms.
type Build struct {
	ID       string    `json:"id"`
	Version  string    `json:"version"` // the app's version, like 1.0
	Number   string    `json:"number"`  // the build number, like 12
	Uploaded time.Time `json:"uploaded"`
	Expires  time.Time `json:"expires"`
	// State is one of the States below.
	State string `json:"state"`
	// WhatToTest is the build's "What to test" in English, and NotesID
	// the text's own ID once there is one.
	WhatToTest string `json:"what_to_test,omitempty"`
	NotesID    string `json:"notes_id,omitempty"`
}

// A build's state, in plain words for the person.
const (
	Processing  = "processing"   // Apple is still processing it
	NeedsAnswer = "needs_answer" // it waits on the encryption question
	Ready       = "ready"        // testers can install it
	Expired     = "expired"      // after 90 days, or expired by hand
	Failed      = "failed"       // Apple turned it down
)

// StateName says a state in a few words.
func StateName(s string) string {
	switch s {
	case Processing:
		return "Apple is processing it"
	case NeedsAnswer:
		return "Needs the encryption answer"
	case Ready:
		return "Ready to test"
	case Expired:
		return "Expired"
	case Failed:
		return "Apple couldn't process it"
	}
	return "Unknown"
}

func buildState(processing, internal string, expired bool) string {
	switch {
	case expired || internal == "EXPIRED":
		return Expired
	case processing == "FAILED" || processing == "INVALID" || internal == "PROCESSING_EXCEPTION":
		return Failed
	case processing == "PROCESSING" || internal == "PROCESSING":
		return Processing
	case internal == "MISSING_EXPORT_COMPLIANCE":
		return NeedsAnswer
	}
	return Ready
}

// Builds lists the app's latest builds, newest first.
func (c *Client) Builds(appID string, limit int) ([]Build, error) {
	var res struct {
		Data []struct {
			ID         string `json:"id"`
			Attributes struct {
				Version    string    `json:"version"`
				Uploaded   time.Time `json:"uploadedDate"`
				Expires    time.Time `json:"expirationDate"`
				Expired    bool      `json:"expired"`
				Processing string    `json:"processingState"`
			} `json:"attributes"`
			Relationships struct {
				Pre     one  `json:"preReleaseVersion"`
				Detail  one  `json:"buildBetaDetail"`
				Locales many `json:"betaBuildLocalizations"`
			} `json:"relationships"`
		} `json:"data"`
		Included []struct {
			Type       string `json:"type"`
			ID         string `json:"id"`
			Attributes struct {
				Version  string `json:"version"`
				Internal string `json:"internalBuildState"`
				WhatsNew string `json:"whatsNew"`
				Locale   string `json:"locale"`
			} `json:"attributes"`
		} `json:"included"`
	}
	q := url.Values{
		"filter[app]": {appID},
		"sort":        {"-uploadedDate"},
		"limit":       {strconv.Itoa(limit)},
		"include":     {"preReleaseVersion,buildBetaDetail,betaBuildLocalizations"},
	}
	if err := c.Do("GET", "/v1/builds?"+q.Encode(), nil, &res); err != nil {
		return nil, err
	}
	inc := map[string]int{}
	for i, x := range res.Included {
		inc[x.Type+"/"+x.ID] = i
	}
	get := func(typ string, r *ref) (int, bool) {
		if r == nil {
			return 0, false
		}
		i, ok := inc[typ+"/"+r.ID]
		return i, ok
	}
	out := []Build{}
	for _, d := range res.Data {
		b := Build{ID: d.ID, Number: d.Attributes.Version, Uploaded: d.Attributes.Uploaded, Expires: d.Attributes.Expires}
		if i, ok := get("preReleaseVersions", d.Relationships.Pre.Data); ok {
			b.Version = res.Included[i].Attributes.Version
		}
		internal := ""
		if i, ok := get("buildBetaDetails", d.Relationships.Detail.Data); ok {
			internal = res.Included[i].Attributes.Internal
		}
		b.State = buildState(d.Attributes.Processing, internal, d.Attributes.Expired)
		for _, l := range d.Relationships.Locales.Data {
			if i, ok := get("betaBuildLocalizations", &l); ok && strings.HasPrefix(res.Included[i].Attributes.Locale, "en") {
				b.NotesID, b.WhatToTest = l.ID, res.Included[i].Attributes.WhatsNew
				break
			}
		}
		out = append(out, b)
	}
	return out, nil
}

// SetWhatToTest writes a build's "What to test", in English.
func (c *Client) SetWhatToTest(b Build, text string) error {
	if b.NotesID != "" {
		return c.Do("PATCH", "/v1/betaBuildLocalizations/"+url.PathEscape(b.NotesID), map[string]any{"data": map[string]any{
			"type": "betaBuildLocalizations", "id": b.NotesID, "attributes": map[string]string{"whatsNew": text}}}, nil)
	}
	return c.Do("POST", "/v1/betaBuildLocalizations", map[string]any{"data": map[string]any{
		"type":          "betaBuildLocalizations",
		"attributes":    map[string]string{"locale": "en-US", "whatsNew": text},
		"relationships": map[string]any{"build": map[string]any{"data": ref{Type: "builds", ID: b.ID}}}}}, nil)
}

// NoSpecialEncryption answers the export question for a build: the app
// uses no encryption beyond what Apple's own system gives it.
func (c *Client) NoSpecialEncryption(buildID string) error {
	return c.Do("PATCH", "/v1/builds/"+url.PathEscape(buildID), map[string]any{"data": map[string]any{
		"type": "builds", "id": buildID, "attributes": map[string]bool{"usesNonExemptEncryption": false}}}, nil)
}

// Group is a TestFlight tester group.
type Group struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Internal bool   `json:"internal"`
	AllBuild bool   `json:"all_builds"`
}

// Group finds the app's group by name, making it when it isn't there: an
// internal group that gets every build, so nothing waits on Apple's review.
func (c *Client) Group(appID, name string) (g Group, made bool, err error) {
	var res struct {
		Data []struct {
			ID         string `json:"id"`
			Attributes struct {
				Name     string `json:"name"`
				Internal bool   `json:"isInternalGroup"`
				All      bool   `json:"hasAccessToAllBuilds"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := c.Do("GET", "/v1/apps/"+url.PathEscape(appID)+"/betaGroups?limit=50", nil, &res); err != nil {
		return g, false, err
	}
	for _, d := range res.Data {
		if strings.EqualFold(d.Attributes.Name, name) {
			return Group{d.ID, d.Attributes.Name, d.Attributes.Internal, d.Attributes.All}, false, nil
		}
	}
	var out struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	err = c.Do("POST", "/v1/betaGroups", map[string]any{"data": map[string]any{
		"type":          "betaGroups",
		"attributes":    map[string]any{"name": name, "isInternalGroup": true, "hasAccessToAllBuilds": true},
		"relationships": map[string]any{"app": map[string]any{"data": ref{Type: "apps", ID: appID}}}}}, &out)
	if err != nil {
		return g, false, err
	}
	return Group{out.Data.ID, name, true, true}, true, nil
}

// Tester is someone in a group.
type Tester struct {
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
	// State is Apple's: NOT_INVITED, INVITED, ACCEPTED, INSTALLED or REVOKED.
	State string `json:"state,omitempty"`
}

// Installed says the tester has the TestFlight app and a build on a device.
func (t Tester) Installed() bool { return t.State == "INSTALLED" }

// StateName says where a tester is, in a few words.
func (t Tester) StateName() string {
	switch t.State {
	case "INSTALLED":
		return "Installed"
	case "ACCEPTED":
		return "Joined, nothing installed yet"
	case "INVITED":
		return "Invited"
	case "REVOKED":
		return "Taken off"
	}
	return "Not invited yet"
}

// Testers lists a group's testers, by email.
func (c *Client) Testers(groupID string) ([]Tester, error) {
	var res struct {
		Data []struct {
			Attributes struct {
				Email string `json:"email"`
				First string `json:"firstName"`
				Last  string `json:"lastName"`
				State string `json:"state"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := c.Do("GET", "/v1/betaGroups/"+url.PathEscape(groupID)+"/betaTesters?limit=50", nil, &res); err != nil {
		return nil, err
	}
	out := []Tester{}
	for _, d := range res.Data {
		a := d.Attributes
		out = append(out, Tester{Email: a.Email, Name: strings.TrimSpace(a.First + " " + a.Last), State: a.State})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Email < out[j].Email })
	return out, nil
}

// AddTester puts someone in a group. For an internal group they must
// already be a user on the App Store Connect team.
func (c *Client) AddTester(groupID, email string) error {
	err := c.Do("POST", "/v1/betaTesters", map[string]any{"data": map[string]any{
		"type":          "betaTesters",
		"attributes":    map[string]string{"email": email},
		"relationships": map[string]any{"betaGroups": map[string]any{"data": []ref{{Type: "betaGroups", ID: groupID}}}}}}, nil)
	var e *store.Error
	if errors.As(err, &e) && e.Status == 409 {
		return fmt.Errorf("couldn't add %s (%s). Someone in this group must be a user on your App Store Connect team: add them in Users and Access first", email, e.Error())
	}
	return err
}

// NextNumber is the build number after every one given, at least after
// floor (a number the code already has).
func NextNumber(builds []Build, floor string) string {
	n, _ := strconv.Atoi(strings.TrimSpace(floor))
	for _, b := range builds {
		if m, err := strconv.Atoi(b.Number); err == nil && m > n {
			n = m
		}
	}
	return strconv.Itoa(n + 1)
}
