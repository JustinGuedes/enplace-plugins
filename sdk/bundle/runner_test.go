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

func TestAnAgentMayHaveSeveralWays(t *testing.T) {
	agent := func(settings, ways string) string {
		return `"kind":"agent","settings":[` + settings + `],"contributes":{"ways":[` + ways + `]}}`
	}
	const mac = `{"key":"mac","name":"On your Mac","agent":"Claude","where":"computer","pays":"plan","kind":"prompt","prompt":"Do {key}"}`
	const key = `{"key":"anthropic_api_key","name":"Key","type":"secret","scope":"agent"}`
	const api = `{"key":"api","name":"On an API key","agent":"Claude","where":"enplace","pays":"api","kind":"loop","api":"anthropic","settings":["anthropic_api_key"],"key_secret":"anthropic_api_key","models":[{"id":"m","name":"M","in":1,"out":2}]}`
	m, err := parseDemo(t, agent(key, mac+","+api))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Contributes.Ways) != 2 || m.Contributes.Ways[1].KeySecret != "anthropic_api_key" || m.Contributes.Ways[0].Kind != RunnerPrompt {
		t.Errorf("ways read as %+v", m.Contributes.Ways)
	}
	bad := map[string]string{
		"no ways or runner":     `"kind":"agent"`,
		"runner and ways":       `"kind":"agent","contributes":{"runner":{"agent":"Claude","where":"computer","pays":"plan","kind":"prompt","prompt":"x"},"ways":[` + mac + `]}`,
		"repeated way":          agent("", mac+","+mac),
		"two agents":            agent("", mac+`,{"key":"b","name":"B","agent":"Codex","where":"computer","pays":"plan","kind":"prompt","prompt":"x"}`),
		"unnamed way":           agent("", `{"key":"mac","agent":"Claude","where":"computer","pays":"plan","kind":"prompt","prompt":"x"}`),
		"key not its own":       agent(key+`,{"key":"other","name":"O","type":"secret","scope":"agent"}`, mac+`,`+api[:len(api)-1]+`},{"key":"o","name":"O","agent":"Claude","where":"enplace","pays":"api","kind":"loop","api":"anthropic","settings":["other"],"key_secret":"anthropic_api_key","models":[{"id":"m","name":"M","in":1,"out":2}]}`),
		"agent setting unused":  agent(key, mac),
		"agent setting, no way": `"kind":"agent","settings":[` + key + `],"contributes":{"runner":{"agent":"Claude","where":"computer","pays":"plan","kind":"prompt","prompt":"x"}}`,
	}
	for name, body := range bad {
		t.Run(name, func(t *testing.T) {
			if _, err := parseDemo(t, body); err == nil {
				t.Error("accepted")
			}
		})
	}
}

func TestReplacedByNamesAnotherPlugin(t *testing.T) {
	const runner = `"contributes":{"runner":{"agent":"Claude","where":"computer","pays":"plan","kind":"prompt","prompt":"x"}}`
	if m, err := parseDemo(t, `"kind":"agent","replaced_by":"claude",`+runner); err != nil || m.ReplacedBy != "claude" {
		t.Fatalf("%v %+v", err, m)
	}
	for _, to := range []string{"demo", "Not An Id"} {
		if _, err := parseDemo(t, `"kind":"agent","replaced_by":"`+to+`",`+runner); err == nil {
			t.Errorf("replaced_by %q accepted", to)
		}
	}
}
