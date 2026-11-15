package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JustinGuedes/enplace-plugins/sdk/bundle"
	"github.com/JustinGuedes/enplace-plugins/sdk/catalogue"
)

// skillPack writes a skills-only plugin folder: it needs no Go build.
func skillPack(t *testing.T, root, id, version, skill string) string {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(filepath.Join(dir, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"id":"` + id + `","name":"Test pack","version":"` + version + `","summary":"For tests.","scope":"both",
		"permissions":{},"contributes":{"skills":[{"name":"tidy","description":"Keep it tidy."}]}}`
	write(t, filepath.Join(dir, "manifest.json"), manifest)
	write(t, filepath.Join(dir, "skills", "tidy.md"), skill)
	return dir
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// keygen makes a key with the tool and returns its file and public key.
func keygen(t *testing.T) (string, string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "publisher.key")
	if err := pluginCmd([]string{"keygen", "--out", file}); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(file); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("key file %v, mode %v: only its owner may read it", err, st.Mode())
	}
	key, err := signingKey(file)
	if err != nil || key == nil {
		t.Fatalf("reading the key back: %v", err)
	}
	return file, base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey))
}

func readCatalogue(t *testing.T, repo, pub string) *catalogue.Catalogue {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repo, "catalogue.json"))
	if err != nil {
		t.Fatal(err)
	}
	cat, trust, err := catalogue.Parse(raw, []string{pub})
	if err != nil || trust != catalogue.Verified {
		t.Fatalf("catalogue doesn't check out against the publisher key: %v (%s)", err, trust)
	}
	return cat
}

func TestPackSignsWithTheKey(t *testing.T) {
	keyFile, pub := keygen(t)
	dir := skillPack(t, t.TempDir(), "tidy-pack", "1.0.0", "# Tidy\n")
	out := filepath.Join(t.TempDir(), "out", "tidy.enplace")
	if err := pluginCmd([]string{"pack", dir, "--key", keyFile, "--out", out}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	b, err := bundle.Read(data, []string{pub})
	if err != nil {
		t.Fatal(err)
	}
	if b.Manifest.ID != "tidy-pack" || string(b.Files["skills/tidy.md"]) != "# Tidy\n" {
		t.Errorf("bundle = %s with %d files", b.Manifest.ID, len(b.Files))
	}
	if b.Signature != bundle.Verified {
		t.Error("the bundle isn't signed by the publisher key")
	}
}

func TestPublishOnlyWhatIsNew(t *testing.T) {
	keyFile, pub := keygen(t)
	src, repo := t.TempDir(), t.TempDir()
	dir := skillPack(t, src, "tidy-pack", "1.0.0", "# Tidy\n")
	publish := func(extra ...string) error {
		return pluginCmd(append([]string{"publish", dir, "--catalogue", repo, "--key", keyFile}, extra...))
	}

	if err := publish("--new-only"); err != nil {
		t.Fatal(err)
	}
	cat := readCatalogue(t, repo, pub)
	if !cat.Has("tidy-pack", "1.0.0") {
		t.Fatalf("catalogue = %+v", cat.Plugins)
	}
	if _, err := os.Stat(filepath.Join(repo, "bundles", "tidy-pack-1.0.0.enplace")); err != nil {
		t.Fatal(err)
	}

	// Nothing new: the catalogue isn't rewritten, so a run commits nothing.
	before, _ := os.ReadFile(filepath.Join(repo, "catalogue.json"))
	if err := publish("--new-only"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(repo, "catalogue.json"))
	if string(before) != string(after) {
		t.Error("--new-only rewrote the catalogue with nothing new")
	}

	// Changed files under a version that's out already are refused...
	write(t, filepath.Join(dir, "skills", "tidy.md"), "# Tidier\n")
	if err := publish(); err == nil || !strings.Contains(err.Error(), "bump its version") {
		t.Fatalf("republishing changed files as 1.0.0: %v", err)
	}
	// ...and go out once the version goes up.
	manifest, _ := os.ReadFile(filepath.Join(dir, "manifest.json"))
	write(t, filepath.Join(dir, "manifest.json"), strings.Replace(string(manifest), "1.0.0", "1.0.1", 1))
	if err := publish("--new-only"); err != nil {
		t.Fatal(err)
	}
	cat = readCatalogue(t, repo, pub)
	if len(cat.Plugins) != 1 || cat.Plugins[0].Version != "1.0.1" {
		t.Errorf("catalogue = %+v", cat.Plugins)
	}
}

func TestPublishNeedsTheKey(t *testing.T) {
	t.Setenv("ENPLACE_PLUGIN_SIGNING_KEY", "")
	dir := skillPack(t, t.TempDir(), "tidy-pack", "1.0.0", "# Tidy\n")
	err := pluginCmd([]string{"publish", dir, "--catalogue", t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "signing key") {
		t.Errorf("publish without a key: %v", err)
	}
}

func TestSigningKeyFromTheEnvironment(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(nil)
	t.Setenv("ENPLACE_PLUGIN_SIGNING_KEY", base64.StdEncoding.EncodeToString(priv.Seed())+"\n")
	key, err := signingKey("")
	if err != nil || !key.Equal(priv) {
		t.Errorf("key from the environment: %v", err)
	}
	t.Setenv("ENPLACE_PLUGIN_SIGNING_KEY", "not a key")
	if _, err := signingKey(""); err == nil {
		t.Error("a malformed key was accepted")
	}
}

func TestUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"unknown"}, {"keygen"}, {"pack"}, {"publish"}} {
		if err := pluginCmd(args); err == nil {
			t.Errorf("%v: no error", args)
		}
	}
}
