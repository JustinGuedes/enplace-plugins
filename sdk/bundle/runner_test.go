package bundle

import "testing"

func TestARunnerMayOpenALinkAfterCopying(t *testing.T) {
	runner := func(kind, extra string) string {
		return `"kind":"agent","contributes":{"runner":{"agent":"Codex","where":"cloud","pays":"plan","kind":"` + kind + `","prompt":"Do {key}"` + extra + `}}`
	}
	if _, err := parseDemo(t, runner("prompt", `,"open":"https://chatgpt.com/codex"`)); err != nil {
		t.Fatal(err)
	}
	bad := map[string]string{
		"not https":       runner("prompt", `,"open":"http://chatgpt.com/codex"`),
		"with a query":    runner("prompt", `,"open":"https://chatgpt.com/codex?x=1"`),
		"on a start kind": runner("start", `,"start":"fire","open":"https://chatgpt.com/codex"`),
	}
	for name, body := range bad {
		t.Run(name, func(t *testing.T) {
			if _, err := parseDemo(t, body); err == nil {
				t.Error("accepted")
			}
		})
	}
}

func TestFillPromptPutsInTheItem(t *testing.T) {
	r := &Runner{Prompt: "{key}: {title}\n{details}"}
	if got := r.FillPrompt("ENP-1", "Fix it", "Steps"); got != "ENP-1: Fix it\nSteps" {
		t.Errorf("got %q", got)
	}
	if got := r.FillPrompt("ENP-1", "Fix it", " "); got != "ENP-1: Fix it\n(no description)" {
		t.Errorf("empty details: got %q", got)
	}
}
