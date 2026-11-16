package catalogue

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func sample() *Catalogue {
	return &Catalogue{Name: "Enplace", Plugins: []Entry{{
		ID: "plain-words", Name: "Plain-words writing", Version: "1.0.0",
		Download: "bundles/plain-words-1.0.0.enplace", SHA256: strings.Repeat("a", 64),
	}}}
}

func TestSignAndParse(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	c := sample()
	c.Sign(priv)
	raw, _ := json.Marshal(c)
	trusted := []string{base64.StdEncoding.EncodeToString(pub)}
	if _, tr, err := Parse(raw, trusted); err != nil || tr != Verified {
		t.Fatalf("trusted: %v %v", tr, err)
	}
	if _, tr, err := Parse(raw, nil); err != nil || tr != Signed {
		t.Fatalf("untrusted: %v %v", tr, err)
	}
	bad := strings.Replace(string(raw), "1.0.0", "1.0.1", 1)
	if _, _, err := Parse([]byte(bad), trusted); err == nil || !strings.Contains(err.Error(), "changed after") {
		t.Fatalf("tampered: %v", err)
	}
	u, _ := json.Marshal(sample())
	if _, tr, err := Parse(u, trusted); err != nil || tr != Unsigned {
		t.Fatalf("unsigned: %v %v", tr, err)
	}
}

func TestParseChecksEntries(t *testing.T) {
	for _, mut := range []func(*Entry){
		func(e *Entry) { e.ID = "Bad id" },
		func(e *Entry) { e.SHA256 = "nope" },
		func(e *Entry) { e.Version = "one" },
		func(e *Entry) { e.Download = "" },
	} {
		c := sample()
		mut(&c.Plugins[0])
		raw, _ := json.Marshal(c)
		if _, _, err := Parse(raw, nil); err == nil {
			t.Fatalf("accepted %+v", c.Plugins[0])
		}
	}
}

func TestDownloadURL(t *testing.T) {
	e := sample().Plugins[0]
	got, err := DownloadURL("https://raw.githubusercontent.com/o/enplace-plugins/main/catalogue.json", e)
	if err != nil || got != "https://raw.githubusercontent.com/o/enplace-plugins/main/bundles/plain-words-1.0.0.enplace" {
		t.Fatalf("%q %v", got, err)
	}
	e.Download = "http://example.com/x.enplace"
	if _, err := DownloadURL("https://a.test/catalogue.json", e); err == nil {
		t.Fatal("allowed http")
	}
}
