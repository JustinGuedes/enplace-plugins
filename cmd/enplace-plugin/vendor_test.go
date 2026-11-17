package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// upstreamRepo makes a git repo of skills the way people share them.
func upstreamRepo(t *testing.T) (string, func(msg string) string) {
	t.Helper()
	repo := t.TempDir()
	files := map[string]string{
		"LICENSE":                         "MIT License\n\nCopyright (c) Someone\n",
		"README.md":                       "# My skills\n",
		"skills/eng/tdd/SKILL.md":         "---\nname: tdd\ndescription: Test first.\n---\n# TDD\n",
		"skills/eng/tdd/tests.md":         "# Good tests\n",
		"skills/eng/tdd/agents/x.yaml":    "for another tool\n",
		"skills/eng/bugs/SKILL.md":        "---\nname: bugs\ndescription: Find bugs.\n---\n# Bugs\n",
		"skills/eng/bugs/scripts/loop.sh": "#!/bin/sh\n",
		"skills/eng/mine/SKILL.md":        "---\nname: mine\ndescription: Only for me.\n---\n# Mine\n",
		"skills/eng/notes.md":             "not a skill\n",
	}
	for p, body := range files {
		write(t, mkdirs(t, filepath.Join(repo, p)), body)
	}
	commit := func(msg string) string {
		for _, args := range [][]string{{"add", "-A"}, {"-c", "user.name=T", "-c", "user.email=t@example.com", "commit", "-q", "-m", msg}} {
			if _, err := git(repo, args...); err != nil {
				t.Fatal(err)
			}
		}
		sha, err := git(repo, "rev-parse", "HEAD")
		if err != nil {
			t.Fatal(err)
		}
		return sha
	}
	if _, err := git(repo, "init", "-q"); err != nil {
		t.Fatal(err)
	}
	commit("first")
	return repo, commit
}

func mkdirs(t *testing.T, p string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestVendorCopiesWordsWithTheLicence(t *testing.T) {
	repo, commit := upstreamRepo(t)
	pack := filepath.Join(t.TempDir(), "eng")
	write(t, mkdirs(t, filepath.Join(pack, "manifest.json")), `{
  "id": "eng",
  "name": "Engineering",
  "version": "1.0.0",
  "kind": "skills",
  "license": "MIT",
  "upstream": {
    "repo": "https://github.com/someone/skills",
    "commit": "0000000000000000000000000000000000000000"
  }
}`)
	write(t, filepath.Join(pack, UpstreamFile), `{"skills": ["skills/eng/*"], "except": ["mine"]}`)

	res, err := vendor(pack, vendorOptions{From: repo})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Added, ",") != "bugs,tdd" || strings.Join(res.Dropped, ",") != "skills/eng/bugs/scripts/loop.sh" {
		t.Errorf("added %v, dropped %v", res.Added, res.Dropped)
	}
	for _, p := range []string{"LICENSE", "skills/tdd/SKILL.md", "skills/tdd/tests.md", "skills/bugs/SKILL.md"} {
		if _, err := os.Stat(filepath.Join(pack, p)); err != nil {
			t.Errorf("%s wasn't copied in", p)
		}
	}
	for _, p := range []string{"skills/tdd/agents", "skills/bugs/scripts", "skills/mine", "README.md"} {
		if _, err := os.Stat(filepath.Join(pack, p)); err == nil {
			t.Errorf("%s shouldn't be copied in", p)
		}
	}
	files, err := collect(pack)
	if err != nil {
		t.Fatal(err)
	}
	if files["LICENSE"] == nil || files["upstream.json"] != nil {
		t.Error("the bundle carries the licence and leaves upstream.json out")
	}

	// Nothing new upstream: nothing to do.
	res, err = vendor(pack, vendorOptions{From: repo, Bump: true})
	if err != nil || res.Changed() {
		t.Fatalf("a second run changed %+v (%v)", res, err)
	}

	// The author changes a skill: the pack follows, with a new version and a
	// summary to read first.
	write(t, filepath.Join(repo, "skills/eng/tdd/SKILL.md"), "---\nname: tdd\ndescription: Test first.\n---\n# TDD, one test at a time\n")
	sha := commit("change")
	res, err = vendor(pack, vendorOptions{From: repo, Bump: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Edited, ",") != "tdd" || res.Version != "1.0.1" || res.To != sha {
		t.Errorf("after a change: %+v", res)
	}
	raw, _ := os.ReadFile(filepath.Join(pack, "manifest.json"))
	if !strings.Contains(string(raw), `"version": "1.0.1"`) || !strings.Contains(string(raw), sha) {
		t.Errorf("manifest now:\n%s", raw)
	}
	if md := res.Markdown(); !strings.Contains(md, "Changed: tdd") || !strings.Contains(md, "/compare/") {
		t.Errorf("summary:\n%s", md)
	}
}

func TestVendorOnlySharesWhatItMay(t *testing.T) {
	repo, _ := upstreamRepo(t)
	pack := filepath.Join(t.TempDir(), "eng")
	write(t, mkdirs(t, filepath.Join(pack, "manifest.json")), `{"id":"eng","name":"Eng","version":"1.0.0","kind":"skills","license":"CC-BY-NC-4.0",
		"upstream":{"repo":"https://github.com/someone/skills","commit":"0000000000000000000000000000000000000000"}}`)
	write(t, filepath.Join(pack, UpstreamFile), `{"skills": ["skills/eng/*"]}`)
	if _, err := vendor(pack, vendorOptions{From: repo}); err == nil || !strings.Contains(err.Error(), "share") {
		t.Errorf("a non-commercial licence: %v", err)
	}
	if err := os.Remove(filepath.Join(repo, "LICENSE")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(pack, "manifest.json"), `{"id":"eng","name":"Eng","version":"1.0.0","kind":"skills","license":"MIT",
		"upstream":{"repo":"https://github.com/someone/skills","commit":"0000000000000000000000000000000000000000"}}`)
	if _, err := vendor(pack, vendorOptions{From: repo}); err == nil || !strings.Contains(err.Error(), "LICENSE") {
		t.Errorf("no licence file: %v", err)
	}
}
