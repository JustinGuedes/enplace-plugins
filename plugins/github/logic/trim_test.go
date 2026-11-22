package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Links stay small enough for Enplace to keep: a busy project's links
// outgrew its per-key limit and every check was turned away (ENP-320).
func TestTrimKeepsLinksSmall(t *testing.T) {
	ls := Links{}
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for i := 1; i <= 280; i++ {
		at := start.Add(time.Duration(i) * time.Hour)
		title := fmt.Sprintf("ENP-%d: %s", i, strings.Repeat("a fairly long pull request title ", 3))
		ls.add([]string{fmt.Sprintf("ENP-%d", i)}, Link{Kind: "pr", Number: i, Title: title, URL: fmt.Sprintf("https://github.com/o/r/pull/%d", i), State: "merged", Branch: "claude/project-thread-abcdef", SHA: strings.Repeat("c", 40), At: at, Opened: at})
		ls.add([]string{fmt.Sprintf("ENP-%d", i)}, Link{Kind: "commit", Title: title, URL: fmt.Sprintf("https://github.com/o/r/commit/%d", i), SHA: strings.Repeat("d", 40), At: at})
	}
	ls.trim()
	raw, _ := json.Marshal(ls)
	if len(raw) > maxBytes {
		t.Fatalf("links take %d bytes, over %d", len(raw), maxBytes)
	}
	if ls["ENP-280"] == nil {
		t.Fatal("the newest item lost its links")
	}
	if ls["ENP-1"] != nil {
		t.Fatal("the oldest item kept its links")
	}
	if strings.Contains(string(raw), "0001-01-01") {
		t.Fatal("empty times are still stored")
	}
}

// A merged pull request drops what GitHub said about merging it.
func TestMergedDropsMergeState(t *testing.T) {
	ls := Links{}
	at := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	ls.add([]string{"ENP-1"}, Link{Kind: "pr", Number: 1, URL: "u", State: "open", SHA: "s", At: at, Merge: "clean", Base: "main", Read: at})
	ls.add([]string{"ENP-1"}, Link{Kind: "pr", Number: 1, URL: "u", State: "merged", SHA: "s", At: at.Add(time.Hour)})
	if l := ls["ENP-1"][0]; l.Merge != "" || !l.Read.IsZero() {
		t.Fatalf("merged link kept its merge state: %+v", l)
	}
}
