package beta

import (
	"net/url"
	"sort"
	"time"
)

// Detail is what Apple says about one build beyond the list: how testers
// use it and how big it is. It's read less often than the list.
type Detail struct {
	Installs int `json:"installs"`
	Sessions int `json:"sessions"`
	Crashes  int `json:"crashes"`
	Feedback int `json:"feedback"`
	Invites  int `json:"invites"`
	// DownloadBytes and InstallBytes are for the universal app; zero
	// until Apple has worked them out.
	DownloadBytes int64     `json:"download_bytes,omitempty"`
	InstallBytes  int64     `json:"install_bytes,omitempty"`
	At            time.Time `json:"at"`
}

type usage struct {
	Data []struct {
		DataPoints []struct {
			Values map[string]float64 `json:"values"`
		} `json:"dataPoints"`
		Dimensions map[string]struct {
			Data *ref `json:"data"`
		} `json:"dimensions"`
	} `json:"data"`
}

// sum adds up a metric across every data point.
func (u usage) sum(i int, key string) int {
	n := 0.0
	for _, p := range u.Data[i].DataPoints {
		n += p.Values[key]
	}
	return int(n)
}

// Usage is a build's installs, sessions, crashes and feedback so far.
func (c *Client) Usage(buildID string) (Detail, error) {
	var u usage
	if err := c.Do("GET", "/v1/builds/"+url.PathEscape(buildID)+"/metrics/betaBuildUsages", nil, &u); err != nil {
		return Detail{}, err
	}
	d := Detail{}
	for i := range u.Data {
		d.Installs += u.sum(i, "installCount")
		d.Sessions += u.sum(i, "sessionCount")
		d.Crashes += u.sum(i, "crashCount")
		d.Feedback += u.sum(i, "feedbackCount")
		d.Invites += u.sum(i, "inviteCount")
	}
	return d, nil
}

// Sizes is how big a build bundle is to download and once installed, for
// the universal app (or the biggest device when Apple gives no universal).
func (c *Client) Sizes(bundleID string) (download, install int64, err error) {
	var res struct {
		Data []struct {
			Attributes struct {
				Device   string `json:"deviceModel"`
				Download int64  `json:"downloadBytes"`
				Install  int64  `json:"installBytes"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := c.Do("GET", "/v1/buildBundles/"+url.PathEscape(bundleID)+"/buildBundleFileSizes?limit=50", nil, &res); err != nil {
		return 0, 0, err
	}
	for _, d := range res.Data {
		a := d.Attributes
		if a.Device == "Universal" {
			return a.Download, a.Install, nil
		}
		if a.Download > download {
			download, install = a.Download, a.Install
		}
	}
	return download, install, nil
}

// TesterUse is how much one tester used the app over the last 30 days.
type TesterUse struct {
	Sessions int `json:"sessions"`
	Crashes  int `json:"crashes"`
	Feedback int `json:"feedback"`
}

// TesterUsage is each tester's use of the app over the last 30 days, by
// tester ID.
func (c *Client) TesterUsage(appID string) (map[string]TesterUse, error) {
	var u usage
	q := url.Values{"groupBy": {"betaTesters"}, "period": {"P30D"}, "limit": {"50"}}
	if err := c.Do("GET", "/v1/apps/"+url.PathEscape(appID)+"/metrics/betaTesterUsages?"+q.Encode(), nil, &u); err != nil {
		return nil, err
	}
	out := map[string]TesterUse{}
	for i, d := range u.Data {
		t, ok := d.Dimensions["betaTesters"]
		if !ok || t.Data == nil {
			continue
		}
		out[t.Data.ID] = TesterUse{Sessions: u.sum(i, "sessionCount"), Crashes: u.sum(i, "crashCount"), Feedback: u.sum(i, "feedbackCount")}
	}
	return out, nil
}

// Note is one piece of feedback a tester sent from the TestFlight app: a
// screenshot with a comment, or a crash with what they were doing.
type Note struct {
	ID      string    `json:"id"`
	Kind    string    `json:"kind"` // "screenshot" or "crash"
	At      time.Time `json:"at"`
	Comment string    `json:"comment,omitempty"`
	Email   string    `json:"email,omitempty"`
	Device  string    `json:"device,omitempty"`
	OS      string    `json:"os,omitempty"`
	Build   string    `json:"build,omitempty"` // the build's ID
	// Image is the first screenshot's link; Apple's links expire.
	Image string `json:"image,omitempty"`
}

// Feedback is the app's latest feedback, screenshots and crashes together,
// newest first.
func (c *Client) Feedback(appID string, limit int) ([]Note, error) {
	out := []Note{}
	for _, kind := range []string{"screenshot", "crash"} {
		var res struct {
			Data []struct {
				ID         string `json:"id"`
				Attributes struct {
					At          time.Time `json:"createdDate"`
					Comment     string    `json:"comment"`
					Email       string    `json:"email"`
					Device      string    `json:"deviceModel"`
					OS          string    `json:"osVersion"`
					Screenshots []struct {
						URL string `json:"url"`
					} `json:"screenshots"`
				} `json:"attributes"`
				Relationships struct {
					Build one `json:"build"`
				} `json:"relationships"`
			} `json:"data"`
		}
		path := "/v1/apps/" + url.PathEscape(appID) + "/betaFeedback" + map[string]string{"screenshot": "Screenshot", "crash": "Crash"}[kind] + "Submissions?" +
			url.Values{"limit": {"20"}, "include": {"build"}}.Encode()
		if err := c.Do("GET", path, nil, &res); err != nil {
			return out, err
		}
		for _, d := range res.Data {
			a := d.Attributes
			n := Note{ID: d.ID, Kind: kind, At: a.At, Comment: a.Comment, Email: a.Email, Device: a.Device, OS: a.OS}
			if d.Relationships.Build.Data != nil {
				n.Build = d.Relationships.Build.Data.ID
			}
			if len(a.Screenshots) > 0 {
				n.Image = a.Screenshots[0].URL
			}
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Expire stops a build being testable, so testers can't install it any more.
func (c *Client) Expire(buildID string) error {
	return c.Do("PATCH", "/v1/builds/"+url.PathEscape(buildID), map[string]any{"data": map[string]any{
		"type": "builds", "id": buildID, "attributes": map[string]bool{"expired": true}}}, nil)
}
