package main

import (
	"encoding/base64"
	"sort"
	"strings"
	"time"
)

// uploadMarks are what a workflow file holds when it uploads to a store:
// the usual actions and tools, in lower case (ENP-379).
var uploadMarks = map[string][]string{
	"google-play": {"upload-google-play", "androidpublisher", "fastlane supply", "upload_to_play_store"},
}

// scanEvery is how often it reads the workflow files again.
const scanEvery = time.Hour

// scan reads the workflow files for store uploads, at most once an hour.
func (r *run) scan(c *client, s *Status, was *Status) {
	if was != nil && was.Repo == s.Repo && r.req.Now.Sub(was.ScannedAt) < scanEvery {
		s.Uploads, s.ScannedAt, s.Scanned = was.Uploads, was.ScannedAt, was.Scanned
		return
	}
	s.ScannedAt, s.Scanned, s.Uploads = r.req.Now, true, nil
	found := map[string]bool{}
	for _, w := range s.Workflows {
		if w.State != "active" || !strings.HasPrefix(w.Path, ".github/workflows/") {
			continue
		}
		var f struct {
			Content string `json:"content"`
		}
		if c.getJSON("/repos/"+c.repo+"/contents/"+w.Path, &f) != nil {
			s.Scanned = false // the token can't read files: say nothing
			return
		}
		text, _ := base64.StdEncoding.DecodeString(strings.ReplaceAll(f.Content, "\n", ""))
		for store, marks := range uploadMarks {
			for _, m := range marks {
				if strings.Contains(strings.ToLower(string(text)), m) {
					found[store] = true
				}
			}
		}
	}
	for store := range found {
		s.Uploads = append(s.Uploads, store)
	}
	sort.Strings(s.Uploads)
}

func (s *Status) repo() string {
	if s == nil {
		return ""
	}
	return s.Repo
}

func (s *Status) uploads() []string {
	if s == nil || s.Uploads == nil {
		return []string{}
	}
	return s.Uploads
}
