package cloud

import (
	"net/url"
	"strconv"
)

// Problem is one thing Xcode Cloud reported about a run: an error or
// warning from the build, or a test that failed.
type Problem struct {
	Action  string `json:"action,omitempty"` // the action it came from, like "Archive - iOS"
	Kind    string `json:"kind"`             // error, warning, test or analyzer
	File    string `json:"file,omitempty"`
	Line    int    `json:"line,omitempty"`
	Test    string `json:"test,omitempty"` // Class.testName, for a failed test
	Message string `json:"message"`
}

// Step is one action of a run and how it ended.
type Step struct {
	Name   string `json:"name"`
	Type   string `json:"type"`   // BUILD, ANALYZE, TEST or ARCHIVE
	Result string `json:"result"` // passed, failed or cancelled
}

// Output is what a run said: its actions and what went wrong in them.
type Output struct {
	Steps    []Step    `json:"steps"`
	Problems []Problem `json:"problems,omitempty"`
}

// maxProblems keeps a bug readable: the first ones are what to fix.
const maxProblems = 30

type fileSource struct {
	Path string `json:"path"`
	Line int    `json:"lineNumber"`
}

// Output reads a run's actions and, for each that didn't pass, its
// errors (warnings only when there are no errors) and failed tests.
func (c *Client) Output(runID string) (Output, error) {
	var acts struct {
		Data []struct {
			ID         string `json:"id"`
			Attributes struct {
				Name     string `json:"name"`
				Type     string `json:"actionType"`
				Progress string `json:"executionProgress"`
				Result   string `json:"completionStatus"`
			} `json:"attributes"`
		} `json:"data"`
	}
	out := Output{Steps: []Step{}}
	if err := c.Do("GET", "/v1/ciBuildRuns/"+url.PathEscape(runID)+"/actions?limit=20", nil, &acts); err != nil {
		return out, err
	}
	var warnings []Problem
	for _, a := range acts.Data {
		res := status(a.Attributes.Progress, a.Attributes.Result)
		out.Steps = append(out.Steps, Step{Name: a.Attributes.Name, Type: a.Attributes.Type, Result: res})
		if res == "passed" || res == "cancelled" {
			continue
		}
		errs, warns, err := c.issues(a.ID, a.Attributes.Name)
		if err != nil {
			return out, err
		}
		out.Problems = append(out.Problems, errs...)
		warnings = append(warnings, warns...)
		if a.Attributes.Type == "TEST" {
			tests, err := c.failedTests(a.ID, a.Attributes.Name)
			if err != nil {
				return out, err
			}
			out.Problems = append(out.Problems, tests...)
		}
	}
	if len(out.Problems) == 0 {
		out.Problems = warnings
	}
	if len(out.Problems) > maxProblems {
		out.Problems = out.Problems[:maxProblems]
	}
	return out, nil
}

func (c *Client) issues(actionID, action string) (errs, warns []Problem, err error) {
	var res struct {
		Data []struct {
			Attributes struct {
				Type    string      `json:"issueType"`
				Message string      `json:"message"`
				File    *fileSource `json:"fileSource"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := c.Do("GET", "/v1/ciBuildActions/"+url.PathEscape(actionID)+"/issues?limit=50", nil, &res); err != nil {
		return nil, nil, err
	}
	for _, d := range res.Data {
		a := d.Attributes
		p := Problem{Action: action, Message: firstLines(a.Message, 6)}
		if a.File != nil {
			p.File, p.Line = a.File.Path, a.File.Line
		}
		switch a.Type {
		case "ERROR":
			p.Kind = "error"
			errs = append(errs, p)
		case "TEST_FAILURE":
			p.Kind = "test"
			errs = append(errs, p)
		case "ANALYZER_WARNING":
			p.Kind = "analyzer"
			warns = append(warns, p)
		default:
			p.Kind = "warning"
			warns = append(warns, p)
		}
	}
	return errs, warns, nil
}

func (c *Client) failedTests(actionID, action string) ([]Problem, error) {
	var res struct {
		Data []struct {
			Attributes struct {
				Class   string      `json:"className"`
				Name    string      `json:"name"`
				Status  string      `json:"status"`
				Message string      `json:"message"`
				File    *fileSource `json:"fileSource"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := c.Do("GET", "/v1/ciBuildActions/"+url.PathEscape(actionID)+"/testResults?limit=50", nil, &res); err != nil {
		return nil, err
	}
	var out []Problem
	for _, d := range res.Data {
		a := d.Attributes
		if a.Status != "FAILURE" && a.Status != "MIXED" {
			continue
		}
		p := Problem{Action: action, Kind: "test", Test: a.Class + "." + a.Name, Message: firstLines(a.Message, 6)}
		if a.File != nil {
			p.File, p.Line = a.File.Path, a.File.Line
		}
		out = append(out, p)
	}
	return out, nil
}

// Where is a problem's place, like "PlanIt/Trip.swift:12".
func (p Problem) Where() string {
	if p.File == "" {
		return ""
	}
	if p.Line > 0 {
		return p.File + ":" + strconv.Itoa(p.Line)
	}
	return p.File
}

// firstLines keeps a message to its first n lines.
func firstLines(s string, n int) string {
	lines := 0
	for i, r := range s {
		if r == '\n' {
			lines++
			if lines == n {
				return s[:i] + " …"
			}
		}
	}
	return s
}
