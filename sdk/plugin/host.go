package plugin

import (
	"encoding/json"
	"errors"
)

// Host calls: the few things logic can ask Enplace to do while it runs.
// Enplace checks each one against the plugin's manifest.

// FetchRequest is an HTTP request to make from Enplace's server. Only hosts
// the manifest lists (permissions.hosts), or that a person typed into a
// setting marked "fetch", can be reached, and only on public addresses.
// Fetching works in the schedule, action and event hooks; drawing (render
// and page) has to be quick, so it can't fetch.
type FetchRequest struct {
	Method  string            `json:"method,omitempty"` // GET by default
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    []byte            `json:"body,omitempty"`
}

// FetchResponse is what came back.
type FetchResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    []byte            `json:"body,omitempty"`
	// MS is how long it took.
	MS int `json:"ms"`
}

type hostReq struct {
	Op    string        `json:"op"`
	Fetch *FetchRequest `json:"fetch,omitempty"`
	Name  string        `json:"name,omitempty"`
	Value string        `json:"value,omitempty"`
	AI    *AIRequest    `json:"ai,omitempty"`
}

type hostRes struct {
	Error string         `json:"error,omitempty"`
	Fetch *FetchResponse `json:"fetch,omitempty"`
	AI    *AIJob         `json:"ai,omitempty"`
}

// ErrNoHost is returned outside Enplace's sandbox, like in plain go test.
var ErrNoHost = errors.New("host calls only work inside Enplace")

// Fetch makes an HTTP request through Enplace. A network failure or a host
// the plugin may not reach is an error; any HTTP status is a response.
func Fetch(r FetchRequest) (*FetchResponse, error) {
	var res hostRes
	if err := host(hostReq{Op: "fetch", Fetch: &r}, &res); err != nil {
		return nil, err
	}
	if res.Error != "" {
		return nil, errors.New(res.Error)
	}
	if res.Fetch == nil {
		return nil, errors.New("no answer")
	}
	return res.Fetch, nil
}

func host(req any, res any) error {
	b, err := json.Marshal(req)
	if err != nil {
		return err
	}
	out, err := hostCall(b)
	if err != nil {
		return err
	}
	return json.Unmarshal(out, res)
}

// VaultPut stores a secret the plugin keeps for itself, like a calendar's
// private link a person gave it. It's encrypted, never shown again, and
// comes back in Request.Vault on the schedule, action and event hooks.
// Names are 1-40 lowercase letters, digits, dashes or underscores.
func VaultPut(name, value string) error {
	var res hostRes
	if err := host(hostReq{Op: "vault.put", Name: name, Value: value}, &res); err != nil {
		return err
	}
	if res.Error != "" {
		return errors.New(res.Error)
	}
	return nil
}

// VaultDelete forgets a stored secret.
func VaultDelete(name string) error {
	var res hostRes
	if err := host(hostReq{Op: "vault.delete", Name: name}, &res); err != nil {
		return err
	}
	if res.Error != "" {
		return errors.New(res.Error)
	}
	return nil
}

// AIRequest asks for a small AI job through Enplace's queue. The manifest
// has to say permissions.ai. Who does it, which model and what it may cost
// are the person's choice, never the plugin's.
type AIRequest struct {
	// Type is "summarize", "tidy" or "custom".
	Type string `json:"type"`
	// Instructions say what to do, for a custom job (up to 4 KB).
	Instructions string `json:"instructions,omitempty"`
	// Input is the text to work on (up to 100 KB).
	Input string `json:"input"`
	// Tier asks for a bigger model than the type needs: 0 small, 1 medium,
	// 2 large.
	Tier int `json:"tier,omitempty"`
	// ProjectID is the project the job is about, if any.
	ProjectID string `json:"project_id,omitempty"`
}

// AIJob is where a job stands. Ask again with the same input, or AIGet
// with its ID, from a later hook call (a schedule, say) to get the output.
type AIJob struct {
	ID string `json:"id,omitempty"`
	// Status is AIOff, "held" (waits for the person's OK on its cost),
	// "queued", "taken", "done", "failed" or "capped" (the person's
	// spending cap stopped it).
	Status string `json:"status"`
	Output string `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
	// EstimateMicros is the most it could cost, in millionths of a dollar.
	EstimateMicros int64 `json:"estimate_micros,omitempty"`
}

// AIOff is the status when the person hasn't switched AI on. Carry on
// without the answer.
const AIOff = "off"

// AIAsk queues a job, or returns the one already there for the same input
// (with its output once it's done), so asking again is cheap.
func AIAsk(r AIRequest) (*AIJob, error) {
	return aiCall(hostReq{Op: "ai.ask", AI: &r})
}

// AIGet reads a job this plugin asked for.
func AIGet(id string) (*AIJob, error) {
	return aiCall(hostReq{Op: "ai.get", Name: id})
}

func aiCall(req hostReq) (*AIJob, error) {
	var res hostRes
	if err := host(req, &res); err != nil {
		return nil, err
	}
	if res.Error != "" {
		return nil, errors.New(res.Error)
	}
	if res.AI == nil {
		return nil, errors.New("no answer")
	}
	return res.AI, nil
}
