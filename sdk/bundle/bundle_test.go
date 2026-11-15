package bundle

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
)

const manifest = `{"id":"demo","name":"Demo","version":"1.0.0","scope":"workspace","contributes":{"brief":true}}`

func TestSignAndVerify(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	files := map[string][]byte{"manifest.json": []byte(manifest), "assets/a.png": {1, 2, 3}}
	data, err := Pack(files, priv)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Read(data, nil)
	if err != nil || b.Signature != Signed {
		t.Fatalf("unknown key: %v %v", b, err)
	}
	b, err = Read(data, []string{base64.StdEncoding.EncodeToString(pub)})
	if err != nil || b.Signature != Verified {
		t.Fatalf("trusted key: %v %v", b, err)
	}
	// Changing a file after signing is caught.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	zr, _ := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	for _, f := range zr.File {
		w, _ := zw.Create(f.Name)
		rc, _ := f.Open()
		var body bytes.Buffer
		_, _ = body.ReadFrom(rc)
		rc.Close()
		if f.Name == "assets/a.png" {
			body.Reset()
			body.WriteString("evil")
		}
		_, _ = w.Write(body.Bytes())
	}
	zw.Close()
	if _, err := Read(buf.Bytes(), nil); err == nil {
		t.Fatal("tampered bundle accepted")
	}
}

func TestUnsafePaths(t *testing.T) {
	for _, name := range []string{"../x", "/abs", "a/../../b", "a\\b"} {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		w, _ := zw.Create("manifest.json")
		_, _ = w.Write([]byte(manifest))
		w, _ = zw.CreateHeader(&zip.FileHeader{Name: name})
		_, _ = w.Write([]byte("x"))
		zw.Close()
		if _, err := Read(buf.Bytes(), nil); err == nil {
			t.Errorf("%q accepted", name)
		}
	}
}

func TestManifestChecks(t *testing.T) {
	bad := []string{
		`{"id":"Bad","name":"x","version":"1.0.0","scope":"workspace"}`,
		`{"id":"enplace","name":"x","version":"1.0.0","scope":"workspace"}`,
		`{"id":"ok","name":"x","version":"one","scope":"workspace"}`,
		`{"id":"ok","name":"x","version":"1.0.0","scope":"everywhere"}`,
		`{"id":"ok","name":"x","version":"1.0.0","scope":"workspace","permissions":{"read":["secrets"]}}`,
		`{"id":"ok","name":"x","version":"1.0.0","scope":"workspace","permissions":{"ops":["work.delete"]}}`,
		`{"id":"ok","name":"x","version":"1.0.0","scope":"workspace","contributes":{"look":{"tokens":{"--accent":"red;}body{display:none"}}}}`,
		`{"id":"ok","name":"x","version":"1.0.0","scope":"workspace","contributes":{"pages":[{"key":"p","name":"P","where":"project"}]}}`,
		`{"id":"ok","name":"x","version":"1.0.0","scope":"both","contributes":{"pages":[{"key":"p","name":"P","where":"project","nav":"main"}]}}`,
		`{"id":"ok","name":"x","version":"1.0.0","scope":"workspace","contributes":{"pages":[{"key":"p","name":"P","where":"workspace","nav":"top"}]}}`,
		`{"id":"ok","name":"x","version":"1.0.0","scope":"workspace","surprise":true}`,
		`{"id":"ok","name":"x","version":"1.0.0","scope":"project","contributes":{"project_cards":[{"key":"t","name":"T","where":"sidebar"}]}}`,
		`{"id":"ok","name":"x","version":"1.0.0","scope":"both","contributes":{"dashboard":[{"key":"t","name":"T","where":"work"}]}}`,
		`{"id":"ok","name":"x","version":"1.0.0","scope":"project","settings":[{"key":"m","name":"M","type":"select","scope":"project","options":["a","b"],"allows":{"when":"c","action":"create"}}]}`,
		`{"id":"ok","name":"x","version":"1.0.0","scope":"project","settings":[{"key":"m","name":"M","type":"select","scope":"project","options":["a","b"],"allows":{"when":"a","action":"delete"}}]}`,
		`{"id":"ok","name":"x","version":"1.0.0","scope":"project","settings":[{"key":"m","name":"M","type":"text","scope":"project","allows":{"when":"a","action":"create"}}]}`,
		`{"id":"ok","name":"x","version":"1.0.0","scope":"project","settings":[{"key":"f","name":"F","type":"toggle","scope":"project","level":"team"}]}`,
		`{"id":"ok","name":"x","version":"1.0.0","scope":"both","settings":[{"key":"f","name":"F","type":"toggle","scope":"workspace","level":"product"}]}`,
		`{"id":"ok","name":"x","version":"1.0.0","scope":"project","settings":[{"key":"d","name":"D","type":"text","scope":"project","hide_when":"f"}]}`,
		`{"id":"ok","name":"x","version":"1.0.0","scope":"project","settings":[{"key":"f","name":"F","type":"text","scope":"project"},{"key":"d","name":"D","type":"text","scope":"project","hide_when":"f"}]}`,
	}
	if _, err := ParseManifest([]byte(`{"id":"ok","name":"x","version":"1.0.0","scope":"project","settings":[{"key":"f","name":"F","type":"toggle","scope":"project","level":"product"},{"key":"d","name":"D","type":"text","scope":"project","hide_when":"f"}]}`)); err != nil {
		t.Errorf("a product toggle that hides a setting: %v", err)
	}
	if _, err := ParseManifest([]byte(`{"id":"ok","name":"x","version":"1.0.0","scope":"project","contributes":{"project_cards":[{"key":"t","name":"T","where":"work"}]}}`)); err != nil {
		t.Errorf("a work card: %v", err)
	}
	for _, m := range bad {
		if _, err := ParseManifest([]byte(m)); err == nil {
			t.Errorf("accepted %s", m)
		}
	}
	if _, err := ParseManifest([]byte(manifest)); err != nil {
		t.Fatal(err)
	}
}

func TestVersions(t *testing.T) {
	for _, c := range []struct {
		v, r string
		ok   bool
	}{
		{"1.2.3", "^1.2", true}, {"2.0.0", "^1.2", false}, {"1.1.9", "^1.2", false},
		{"0.3.1", "^0.3", true}, {"0.4.0", "^0.3", false},
		{"1.2.9", "~1.2", true}, {"1.3.0", "~1.2", false},
		{"5.0.0", ">=1.0", true}, {"1.0.0", "1.0", true}, {"1.0.1", "1.0.0", false}, {"9.9.9", "", true},
	} {
		if got := Satisfies(c.v, c.r); got != c.ok {
			t.Errorf("%s in %q = %v", c.v, c.r, got)
		}
	}
}
