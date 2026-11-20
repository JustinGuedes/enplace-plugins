package beta

import "testing"

func TestBuildState(t *testing.T) {
	for _, c := range []struct {
		processing, internal string
		expired              bool
		want                 string
	}{
		{"PROCESSING", "PROCESSING", false, Processing},
		{"VALID", "MISSING_EXPORT_COMPLIANCE", false, NeedsAnswer},
		{"VALID", "READY_FOR_BETA_TESTING", false, Ready},
		{"VALID", "IN_BETA_TESTING", false, Ready},
		{"VALID", "IN_BETA_TESTING", true, Expired},
		{"INVALID", "", false, Failed},
	} {
		if got := buildState(c.processing, c.internal, c.expired); got != c.want {
			t.Errorf("%s/%s/%v = %s, want %s", c.processing, c.internal, c.expired, got, c.want)
		}
	}
}

func TestNextNumber(t *testing.T) {
	bs := []Build{{Number: "12"}, {Number: "9"}, {Number: "1.0.3"}}
	if n := NextNumber(bs, "5"); n != "13" {
		t.Fatalf("after uploads: %s", n)
	}
	if n := NextNumber(bs, "40"); n != "41" {
		t.Fatalf("after the code's: %s", n)
	}
	if n := NextNumber(nil, ""); n != "1" {
		t.Fatalf("first: %s", n)
	}
}
