package play

import (
	"net/url"
	"strconv"
	"time"
)

// Review is one person's review, with the developer's reply if there is
// one. Play only hands out reviews from the last week through its API.
type Review struct {
	ID      string    `json:"id"`
	Author  string    `json:"author,omitempty"`
	Stars   int       `json:"stars"`
	Text    string    `json:"text"`
	Version string    `json:"version,omitempty"`
	At      time.Time `json:"at"`
	Reply   string    `json:"reply,omitempty"`
}

// Reviews lists the latest reviews, newest first.
func (c *Client) Reviews(n int) ([]Review, error) {
	type stamp struct {
		Seconds string `json:"seconds"`
	}
	var out struct {
		Reviews []struct {
			ID       string `json:"reviewId"`
			Author   string `json:"authorName"`
			Comments []struct {
				User *struct {
					Text    string `json:"text"`
					Stars   int    `json:"starRating"`
					Version string `json:"appVersionName"`
					At      stamp  `json:"lastModified"`
				} `json:"userComment"`
				Dev *struct {
					Text string `json:"text"`
				} `json:"developerComment"`
			} `json:"comments"`
		} `json:"reviews"`
	}
	if err := c.Do("GET", c.app()+"/reviews?maxResults="+strconv.Itoa(n), nil, &out); err != nil {
		return nil, err
	}
	rs := []Review{}
	for _, r := range out.Reviews {
		v := Review{ID: r.ID, Author: r.Author}
		for _, cm := range r.Comments {
			if cm.User != nil {
				sec, _ := strconv.ParseInt(cm.User.At.Seconds, 10, 64)
				v.Text, v.Stars, v.Version, v.At = cm.User.Text, cm.User.Stars, cm.User.Version, time.Unix(sec, 0).UTC()
			}
			if cm.Dev != nil {
				v.Reply = cm.Dev.Text
			}
		}
		rs = append(rs, v)
	}
	return rs, nil
}

// Reply answers a review, or replaces the answer it has.
func (c *Client) Reply(reviewID, text string) error {
	return c.Do("POST", c.app()+"/reviews/"+url.PathEscape(reviewID)+":reply", map[string]string{"replyText": text}, nil)
}

// Vitals are the share of daily users who saw the app crash or freeze
// (an ANR) on the latest day Play has numbers for, as fractions.
type Vitals struct {
	Crash float64 `json:"crash"`
	ANR   float64 `json:"anr"`
	Day   string  `json:"day,omitempty"`
}

// Play's bad behaviour lines: above them it shows the app less.
const (
	CrashLine = 0.0109
	ANRLine   = 0.0047
)

// Vitals reads the user-perceived crash and ANR rates. Play's daily
// numbers run a couple of days behind.
func (c *Client) Vitals(now time.Time) (Vitals, error) {
	var v Vitals
	crash, day, err := c.rate("crashRateMetricSet", "userPerceivedCrashRate", now)
	if err != nil {
		return v, err
	}
	anr, _, err := c.rate("anrRateMetricSet", "userPerceivedAnrRate", now)
	if err != nil {
		return v, err
	}
	return Vitals{Crash: crash, ANR: anr, Day: day}, nil
}

type date struct {
	Year  int `json:"year"`
	Month int `json:"month"`
	Day   int `json:"day"`
	TZ    *struct {
		ID string `json:"id"`
	} `json:"timeZone,omitempty"`
}

func dayOf(t time.Time) date {
	d := date{Year: t.Year(), Month: int(t.Month()), Day: t.Day()}
	d.TZ = &struct {
		ID string `json:"id"`
	}{"America/Los_Angeles"}
	return d
}

// rate is one metric's value on the latest day in the last week.
func (c *Client) rate(set, metric string, now time.Time) (float64, string, error) {
	end := now.AddDate(0, 0, -2)
	body := map[string]any{
		"timelineSpec": map[string]any{"aggregationPeriod": "DAILY", "startTime": dayOf(end.AddDate(0, 0, -7)), "endTime": dayOf(end)},
		"metrics":      []string{metric},
	}
	var out struct {
		Rows []struct {
			Start   date `json:"startTime"`
			Metrics []struct {
				Metric string `json:"metric"`
				Value  struct {
					Value string `json:"value"`
				} `json:"decimalValue"`
			} `json:"metrics"`
		} `json:"rows"`
	}
	if err := c.Do("POST", ReportingBase+url.PathEscape(c.Package)+"/"+set+":query", body, &out); err != nil {
		return 0, "", err
	}
	if len(out.Rows) == 0 {
		return 0, "", nil
	}
	last := out.Rows[len(out.Rows)-1]
	for _, m := range last.Metrics {
		if m.Metric == metric {
			f, _ := strconv.ParseFloat(m.Value.Value, 64)
			d := time.Date(last.Start.Year, time.Month(last.Start.Month), last.Start.Day, 0, 0, 0, 0, time.UTC)
			return f, d.Format("2006-01-02"), nil
		}
	}
	return 0, "", nil
}
