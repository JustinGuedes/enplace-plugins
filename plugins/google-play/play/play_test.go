package play

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
	"time"
)

// testKey is a service account key file with a fresh RSA key.
func testKey(t *testing.T) string {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(k)
	p := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	raw, _ := json.Marshal(map[string]string{"type": "service_account", "client_email": "enplace@demo.iam.gserviceaccount.com", "private_key": string(p), "project_id": "demo"})
	return string(raw)
}

// fake answers Google's calls from a table of "METHOD url-suffix".
type fake struct {
	answers map[string]struct {
		status int
		body   string
	}
	calls []string
}

func (f *fake) call(method, u string, _ map[string]string, body []byte) (int, []byte, error) {
	f.calls = append(f.calls, method+" "+u)
	if u == TokenURL {
		if !strings.Contains(string(body), "assertion=") {
			return 400, []byte(`{"error_description":"no assertion"}`), nil
		}
		return 200, []byte(`{"access_token":"tok"}`), nil
	}
	for k, a := range f.answers {
		m, suffix, _ := strings.Cut(k, " ")
		if m == method && strings.HasSuffix(u, suffix) {
			return a.status, []byte(a.body), nil
		}
	}
	return 404, []byte(`{"error":{"message":"not found"}}`), nil
}

func (f *fake) on(method, suffix string, status int, body string) *fake {
	if f.answers == nil {
		f.answers = map[string]struct {
			status int
			body   string
		}{}
	}
	f.answers[method+" "+suffix] = struct {
		status int
		body   string
	}{status, body}
	return f
}

func TestParseKeyNeedsTheWholeFile(t *testing.T) {
	if _, err := ParseKey("-----BEGIN PRIVATE KEY-----"); err == nil {
		t.Fatal("a bare PEM was taken as a key file")
	}
	k, err := ParseKey(testKey(t))
	if err != nil || k.Email != "enplace@demo.iam.gserviceaccount.com" {
		t.Fatalf("key = %+v, %v", k, err)
	}
}

func TestTracksReadsAndDropsAnEdit(t *testing.T) {
	f := (&fake{}).
		on("POST", "/com.demo/edits", 200, `{"id":"e1"}`).
		on("GET", "/edits/e1/tracks", 200, `{"tracks":[{"track":"internal","releases":[{"name":"0.8.0","versionCodes":["214"],"status":"completed"}]}]}`).
		on("DELETE", "/edits/e1", 204, ``)
	c, err := New(f.call, testKey(t), "com.demo", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ts, err := c.Tracks()
	if err != nil || len(ts) != 1 || ts[0].Releases[0].Code() != "214" {
		t.Fatalf("tracks = %+v, %v", ts, err)
	}
	if last := f.calls[len(f.calls)-1]; !strings.HasPrefix(last, "DELETE ") {
		t.Fatalf("the edit wasn't dropped: %v", f.calls)
	}
}

func TestErrorsSayWhatsMissing(t *testing.T) {
	for _, tc := range []struct {
		status int
		msg    string
		want   string
	}{
		{403, "Google Play Android Developer API has not been used in project 123 before or it is disabled.", ReasonAPIOff},
		{403, "The caller does not have permission", ReasonNotInvited},
		{404, "Package not found: com.demo.", ReasonNoApp},
		{401, "Request had invalid authentication credentials.", ReasonKey},
	} {
		body, _ := json.Marshal(map[string]any{"error": map[string]string{"message": tc.msg}})
		f := (&fake{}).on("POST", "/edits", tc.status, string(body))
		c, _ := New(f.call, testKey(t), "com.demo", time.Now())
		_, err := c.Tracks()
		e, ok := err.(*Error)
		if !ok || e.Reason != tc.want {
			t.Errorf("%q: reason = %v, want %s", tc.msg, err, tc.want)
		}
	}
}

func TestRolloutGoesUpAndCompletes(t *testing.T) {
	ts := []Track{{Track: Production, Releases: []Release{
		{Name: "0.7.0", VersionCodes: []string{"180"}, Status: Completed},
		{Name: "0.7.1", VersionCodes: []string{"190"}, Status: InProgress, UserFraction: 0.2},
	}}}
	up, err := Rollout(ts, 0.5, false, false)
	if err != nil || up.Releases[1].UserFraction != 0.5 || len(up.Releases) != 2 {
		t.Fatalf("raise = %+v, %v", up, err)
	}
	if _, err := Rollout(ts, 0.1, false, false); err == nil {
		t.Fatal("a rollout went down")
	}
	all, _ := Rollout(ts, 1, false, false)
	if len(all.Releases) != 1 || all.Releases[0].Name != "0.7.1" || all.Releases[0].Status != Completed {
		t.Fatalf("complete = %+v", all)
	}
	halted, _ := Rollout(ts, 0, true, false)
	if halted.Releases[1].Status != Halted {
		t.Fatalf("halt = %+v", halted)
	}
	if _, err := Rollout([]Track{{Track: Production}}, 0.5, false, false); err != ErrNoRollout {
		t.Fatalf("no rollout: %v", err)
	}
}

func TestPromoteToProductionStagesIt(t *testing.T) {
	ts := []Track{{Track: Production, Releases: []Release{{Name: "0.7.1", VersionCodes: []string{"190"}, Status: Completed}}}}
	r := Release{Name: "0.8.0", VersionCodes: []string{"214"}, Status: Completed}
	got := Promote(ts, r, Production, 0.1)
	if len(got.Releases) != 2 || got.Releases[1].Status != InProgress || got.Releases[1].UserFraction != 0.1 {
		t.Fatalf("production = %+v", got)
	}
	closed := Promote(ts, r, Closed, 0)
	if closed.Track != Closed || len(closed.Releases) != 1 || closed.Releases[0].Status != Completed {
		t.Fatalf("closed = %+v", closed)
	}
}

func TestLivePrefersTheRollout(t *testing.T) {
	r, ok := Live(Track{Releases: []Release{{Name: "old", Status: Completed}, {Name: "new", Status: InProgress}}})
	if !ok || r.Name != "new" {
		t.Fatalf("live = %+v", r)
	}
}

// The listing's icon comes from the same edit as the tracks (ENP-380).
func TestTracksAndIconReadsTheListingsIcon(t *testing.T) {
	f := (&fake{}).
		on("POST", "/com.demo/edits", 200, `{"id":"e1"}`).
		on("GET", "/edits/e1/tracks", 200, `{"tracks":[]}`).
		on("GET", "/edits/e1/details", 200, `{"defaultLanguage":"en-US"}`).
		on("GET", "/edits/e1/listings/en-US/icon", 200, `{"images":[{"id":"i1","url":"https://lh3.googleusercontent.com/icon"}]}`).
		on("DELETE", "/edits/e1", 204, ``)
	c, err := New(f.call, testKey(t), "com.demo", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, icon, err := c.TracksAndIcon(true); err != nil || icon != "https://lh3.googleusercontent.com/icon" {
		t.Fatalf("icon = %q, %v", icon, err)
	}
	f.calls = nil
	if _, icon, _ := c.TracksAndIcon(false); icon != "" || len(f.calls) != 3 {
		t.Fatalf("read the icon when not asked: %q %v", icon, f.calls)
	}
}
