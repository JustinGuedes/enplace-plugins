// Command logic is the Claude in the cloud runner: Send starts the
// person's own Claude Code task on claude.ai (a routine with an API
// trigger) on a work item, with the task's own token, and answers with the
// session it opened. Only Enplace calls "fire" (StartRun), after it has
// claimed the item.
package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

func main() { plugin.Run(handle) }

// fireRe is the only link a token is ever sent to.
var fireRe = regexp.MustCompile(`^https://api\.anthropic\.com/v1/claude_code/routines/trig_[A-Za-z0-9]{6,64}/fire$`)

type fireArgs struct {
	Work struct {
		Key   string `json:"key"`
		Title string `json:"title"`
	} `json:"work"`
	Prompt string `json:"prompt"`
}

func handle(req *plugin.Request) (*plugin.Response, error) {
	res := &plugin.Response{}
	if req.Hook != plugin.HookAction || req.Action == nil || req.Action.Name != "fire" {
		return res, nil
	}
	fail := func(msg string) (*plugin.Response, error) {
		res.Data = map[string]string{"error": msg}
		return res, nil
	}
	link, _ := req.Settings["claude_cloud_url"].(string)
	link = strings.TrimSpace(link)
	if !fireRe.MatchString(link) {
		return fail("paste the task's API trigger link, like https://api.anthropic.com/v1/claude_code/routines/trig_…/fire, on Claude in the cloud's settings")
	}
	token := req.Secrets["claude_cloud_token"]
	if token == "" {
		return fail("add the task's token on Claude in the cloud's settings")
	}
	var in fireArgs
	_ = json.Unmarshal(req.Action.Args, &in)
	text := in.Prompt
	if text == "" {
		text = "Enplace work item " + in.Work.Key + ": " + in.Work.Title
	}
	body, _ := json.Marshal(map[string]string{"text": text})
	out, err := plugin.Fetch(plugin.FetchRequest{
		Method: "POST",
		URL:    link,
		Headers: map[string]string{
			"Authorization":     "Bearer " + token,
			"anthropic-version": "2023-06-01",
			"anthropic-beta":    "experimental-cc-routine-2026-04-01",
			"Content-Type":      "application/json",
		},
		Body: body,
	})
	if err != nil {
		return fail("couldn't reach claude.ai, try again")
	}
	if out.Status != 200 {
		return fail(fireError(out.Status, out.Headers["retry-after"]))
	}
	var got struct {
		URL string `json:"claude_code_session_url"`
	}
	_ = json.Unmarshal(out.Body, &got)
	session := ""
	if strings.HasPrefix(got.URL, "https://claude.ai/") {
		session = got.URL
	}
	res.Data = map[string]string{"link": session}
	return res, nil
}

func fireError(status int, retry string) string {
	switch status {
	case 401:
		return "claude.ai didn't accept the task's token. Make a new token for the task and paste it on Claude in the cloud's settings"
	case 404:
		return "claude.ai can't find that task. Check its link on Claude in the cloud's settings"
	case 429:
		if retry != "" {
			return "the task has started too many runs this hour. Try again in " + retry + " seconds"
		}
		return "the task has started too many runs this hour. Try again later"
	case 400:
		return "claude.ai refused the run. The task may be paused"
	}
	return fmt.Sprintf("claude.ai couldn't start the task (%d). Try again in a moment", status)
}
