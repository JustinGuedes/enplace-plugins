package cloud

import "testing"

func TestPick(t *testing.T) {
	ws := []Workflow{{"1", "Archive", false}, {"2", "TestFlight", true}}
	if w, ok := Pick(ws, ""); !ok || w.ID != "2" {
		t.Fatalf("first on: %+v", w)
	}
	if w, ok := Pick(ws, "archive"); !ok || w.ID != "1" {
		t.Fatalf("by name: %+v", w)
	}
	if _, ok := Pick(ws, "Nope"); ok {
		t.Fatal("picked a workflow that isn't there")
	}
}

func TestStatus(t *testing.T) {
	for _, c := range []struct{ progress, result, want string }{
		{"PENDING", "", "queued"}, {"RUNNING", "", "running"}, {"COMPLETE", "SUCCEEDED", "passed"},
		{"COMPLETE", "FAILED", "failed"}, {"COMPLETE", "ERRORED", "failed"}, {"COMPLETE", "CANCELED", "cancelled"},
	} {
		if got := status(c.progress, c.result); got != c.want {
			t.Errorf("%s/%s = %s", c.progress, c.result, got)
		}
	}
}
