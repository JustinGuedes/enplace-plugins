package plugin

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestRunAnswersOneRequest(t *testing.T) {
	var out bytes.Buffer
	in := strings.NewReader(`{"hook":"render","slots":["brief"],"state":{"workspace":{"n":3}}}`)
	err := run(in, &out, func(req *Request) (*Response, error) {
		var n int
		if !req.State.Get("n", &n) || n != 3 {
			t.Errorf("state n = %d", n)
		}
		res := &Response{Brief: &Brief{Text: "Three things today"}}
		res.Set("n", n+1)
		return res, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var res Response
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("answer isn't JSON: %v\n%s", err, out.String())
	}
	var n int
	if res.Brief == nil || res.Brief.Text != "Three things today" || !res.State.Get("n", &n) || n != 4 {
		t.Errorf("answer = %s", out.String())
	}
}

func TestRunAnswersEmptyWhenTheHandlerHasNothing(t *testing.T) {
	var out bytes.Buffer
	if err := run(strings.NewReader(`{"hook":"event"}`), &out, func(*Request) (*Response, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != "{}" {
		t.Errorf("answer = %s, want {}", got)
	}
}

func TestRunReportsFailures(t *testing.T) {
	if err := run(strings.NewReader(`not json`), &bytes.Buffer{}, nil); err == nil || !strings.Contains(err.Error(), "bad request") {
		t.Errorf("bad input: %v", err)
	}
	boom := errors.New("boom")
	if err := run(strings.NewReader(`{}`), &bytes.Buffer{}, func(*Request) (*Response, error) { return nil, boom }); !errors.Is(err, boom) {
		t.Errorf("handler error: %v", err)
	}
}

func TestStateByProject(t *testing.T) {
	var s State
	s.Put("seen", true)
	s.PutProject("p1", "count", 2)

	var seen bool
	var count int
	if !s.Get("seen", &seen) || !seen {
		t.Error("workspace value lost")
	}
	if !s.GetProject("p1", "count", &count) || count != 2 {
		t.Error("project value lost")
	}
	if s.GetProject("p2", "count", &count) {
		t.Error("another project sees p1's value")
	}
	if s.Get("missing", &seen) {
		t.Error("a missing key reads as set")
	}
}

func TestResponseRecordsChanges(t *testing.T) {
	var r Response
	r.Set("a", "x")
	r.SetProject("p1", "b", []int{1, 2})
	r.SetProject("p1", "gone", nil)

	var a string
	var b []int
	if !r.State.Get("a", &a) || a != "x" || !r.State.GetProject("p1", "b", &b) || len(b) != 2 {
		t.Errorf("state = %+v", r.State)
	}
	// nil clears a key: it's stored as JSON null.
	if got := string(r.State.Projects["p1"]["gone"]); got != "null" {
		t.Errorf("cleared key = %s", got)
	}
}

func TestHostCallsNeedEnplace(t *testing.T) {
	if _, err := Fetch(FetchRequest{URL: "https://example.com"}); !errors.Is(err, ErrNoHost) {
		t.Errorf("Fetch outside Enplace: %v", err)
	}
}

func TestBoardMoveReadsMergedArgs(t *testing.T) {
	// Enplace adds the move to the button's own args.
	raw := []byte(`{"board":"b1","card":"c7","from":"todo","to":"doing","index":2}`)
	var m struct {
		Board string `json:"board"`
		BoardMove
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m.Board != "b1" || m.Card != "c7" || m.From != "todo" || m.To != "doing" || m.Index != 2 {
		t.Errorf("move = %+v", m)
	}
}

func TestBlocksReplaceByID(t *testing.T) {
	r := Response{Blocks: []Block{{Type: BlockBoard, ID: "board", Move: &Button{Action: "move"}}}}
	out, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(out); got != `{"blocks":[{"type":"board","id":"board","move":{"label":"","action":"move"}}]}` {
		t.Errorf("response = %s", got)
	}
}
