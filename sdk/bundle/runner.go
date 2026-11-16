package bundle

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Runner is a way to run work: the agent Send hands a work item to, and how
// Enplace reaches it. Enplace owns the run itself (who started it, which
// item, Approvals, the claim on the item, spend caps and history); a runner
// only says how to reach the agent. There are three kinds:
//
//   - "prompt": the person runs the agent (Claude Code, Codex CLI, Gemini
//     CLI…). Send copies Prompt; the agent works over MCP with its own key.
//     Manifest only, no logic.
//   - "start": Enplace starts the agent somewhere else. The logic's Start
//     action calls the outside service (only on the plugin's hosts, with its
//     own secrets) and answers Data {"link": "https://…"}, the session to
//     follow. The agent then reports back over MCP like any other.
//   - "loop": Enplace drives a model's API itself, speaking API (a wire
//     format Enplace knows, "anthropic"), with the person's key in KeySecret
//     and the prices in Models. Enplace makes every call, so it holds the
//     caps and a plugin can't misreport what it spent. No logic.
type Runner struct {
	// Agent is who does the work, like "Claude" or "Codex": "Send to Claude".
	Agent string `json:"agent"`
	// Name is the runner's name where several are listed, like "Claude in
	// the cloud". Left out, the plugin's name.
	Name string `json:"name,omitempty"`
	// Where it runs: "computer" (the person's own), "cloud" (somewhere
	// else) or "enplace" (Enplace itself runs it).
	Where string `json:"where"`
	// Pays: "plan" (the person's own plan with the agent's maker) or "api"
	// (pay as you go, on their key).
	Pays string `json:"pays"`
	Kind string `json:"kind"`
	// Hint is a line on what it does, shown where Send offers it.
	Hint string `json:"hint,omitempty"`
	// Prompt is what Send hands the agent, with {key} and {title} filled in.
	// Every kind may set it; prompt runners must.
	Prompt string `json:"prompt,omitempty"`
	// Connect names how the agent connects to Enplace's MCP, from the
	// connect screen's list (claude_code, codex, gemini, cursor…), so
	// Enplace can say whether one is connected and show the steps.
	Connect string `json:"connect,omitempty"`
	// Start, Stop and Message are the logic's actions for a start runner.
	// Only Enplace calls them; they can't be pressed as buttons. Without
	// Message, messages ride along on the agent's next Enplace tool call.
	Start   string `json:"start,omitempty"`
	Stop    string `json:"stop,omitempty"`
	Message string `json:"message,omitempty"`
	// Ready are the settings (or secret settings) that must be set before
	// it can run, like the link and token of a claude.ai task.
	Ready []string `json:"ready,omitempty"`
	// MaxPerHour spaces starts out, for services that limit them (a
	// claude.ai task starts up to 30 runs an hour). 0 is no limit.
	MaxPerHour int `json:"max_per_hour,omitempty"`
	// API, KeySecret and Models are for a loop runner.
	API       string        `json:"api,omitempty"`
	KeySecret string        `json:"key_secret,omitempty"`
	Models    []RunnerModel `json:"models,omitempty"`
}

// RunnerModel is a model a loop runner can use, with its price in US
// dollars per million tokens.
type RunnerModel struct {
	ID   string  `json:"id"`
	Name string  `json:"name"`
	Hint string  `json:"hint,omitempty"`
	In   float64 `json:"in"`
	Out  float64 `json:"out"`
	// CacheRead is a cached input token's price; CacheWrite one written to
	// the cache (left out: 1.25 times In).
	CacheRead  float64 `json:"cache_read,omitempty"`
	CacheWrite float64 `json:"cache_write,omitempty"`
	// Fallback says the API may hand a declined request to another model,
	// which Enplace then prices at the highest listed price.
	Fallback bool `json:"fallback,omitempty"`
}

// Runner kinds and the APIs Enplace speaks for loop runners.
const (
	RunnerPrompt = "prompt"
	RunnerStart  = "start"
	RunnerLoop   = "loop"
)

// RunnerAPIs are the wire formats Enplace can drive for a loop runner.
var RunnerAPIs = []string{"anthropic"}

var modelRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._:-]{0,79}$`)

func (m *Manifest) checkRunner() error {
	r := m.Contributes.Runner
	if r == nil {
		return nil
	}
	bad := func(f string, a ...any) error { return fmt.Errorf("runner: "+f, a...) }
	if r.Agent == "" || len(r.Agent) > 30 || len(r.Name) > 40 || len(r.Hint) > 160 {
		return bad("agent (1-30 characters), name (up to 40) and hint (up to 160)")
	}
	if !slices.Contains([]string{"computer", "cloud", "enplace"}, r.Where) {
		return bad(`where is "computer", "cloud" or "enplace"`)
	}
	if r.Pays != "plan" && r.Pays != "api" {
		return bad(`pays is "plan" or "api"`)
	}
	if m.Scope != "workspace" && m.Scope != "both" {
		return bad("a runner is installed for the workspace (scope workspace or both)")
	}
	if len(r.Prompt) > 4000 || r.Connect != "" && !keyRe.MatchString(r.Connect) {
		return bad("prompt is up to 4000 characters, connect a key like codex")
	}
	if r.MaxPerHour < 0 || r.MaxPerHour > 10000 {
		return bad("max_per_hour is 0 to 10000")
	}
	for _, k := range r.Ready {
		if !slices.ContainsFunc(m.Settings, func(s Setting) bool { return s.Key == k && s.Scope == "workspace" }) {
			return bad("ready: no workspace setting %q", k)
		}
	}
	for _, a := range []string{r.Start, r.Stop, r.Message} {
		if a != "" && !keyRe.MatchString(a) {
			return bad("start, stop and message are action names")
		}
	}
	switch r.Kind {
	case RunnerPrompt:
		if strings.TrimSpace(r.Prompt) == "" {
			return bad("a prompt runner needs a prompt")
		}
		if r.Start != "" || r.Stop != "" || r.Message != "" || r.API != "" || len(r.Models) > 0 {
			return bad("a prompt runner has no actions and no API")
		}
	case RunnerStart:
		if r.Start == "" {
			return bad("a start runner names its start action")
		}
		if r.API != "" || len(r.Models) > 0 {
			return bad("a start runner has no API")
		}
		if r.Where == "enplace" {
			return bad(`a start runner runs somewhere else, not "enplace"`)
		}
	case RunnerLoop:
		if !slices.Contains(RunnerAPIs, r.API) {
			return bad("api must be one of %s", strings.Join(RunnerAPIs, ", "))
		}
		if r.Where != "enplace" || r.Pays != "api" {
			return bad(`a loop runner runs in Enplace and pays by "api"`)
		}
		if r.Start != "" || r.Stop != "" || r.Message != "" {
			return bad("a loop runner has no actions: Enplace runs it")
		}
		if !keyRe.MatchString(r.KeySecret) || !slices.ContainsFunc(m.Settings, func(s Setting) bool { return s.Key == r.KeySecret && s.Type == "secret" && s.Scope == "workspace" }) {
			return bad("key_secret names one of its workspace secret settings")
		}
		if len(r.Models) == 0 || len(r.Models) > 12 {
			return bad("a loop runner lists 1-12 models")
		}
		seen := map[string]bool{}
		for _, md := range r.Models {
			if !modelRe.MatchString(md.ID) || seen[md.ID] || md.Name == "" || len(md.Name) > 40 || len(md.Hint) > 120 {
				return bad("bad or repeated model %q", md.ID)
			}
			seen[md.ID] = true
			for _, p := range []float64{md.In, md.Out, md.CacheRead, md.CacheWrite} {
				if p < 0 || p > 10000 {
					return bad("model %s: prices are dollars per million tokens", md.ID)
				}
			}
			if md.In <= 0 || md.Out <= 0 {
				return bad("model %s: give its in and out prices", md.ID)
			}
		}
	default:
		return errors.New(`runner: kind is "prompt", "start" or "loop"`)
	}
	return nil
}

// Actions are the logic actions only Enplace may call.
func (r *Runner) Actions() []string {
	if r == nil {
		return nil
	}
	var out []string
	for _, a := range []string{r.Start, r.Stop, r.Message} {
		if a != "" {
			out = append(out, a)
		}
	}
	return out
}

// Title is the runner's name: its own, else the plugin's.
func (r *Runner) Title(pluginName string) string {
	if r.Name != "" {
		return r.Name
	}
	return pluginName
}

// FillPrompt is the prompt for one work item.
func (r *Runner) FillPrompt(key, title string) string {
	p := r.Prompt
	if strings.TrimSpace(p) == "" {
		p = "Enplace work item {key}: {title}\n\nUse the Enplace MCP: call start_work for {key}, do the work, post progress with add_note, then submit_for_review with a short report and any pull request link. When you change code, open a pull request titled \"{key}: what it does\" so Enplace links it. Pass your session link as session_url to start_work and submit_for_review."
	}
	return strings.NewReplacer("{key}", key, "{title}", title).Replace(p)
}
