// Package catalogue is the signed list a plugin source publishes: what's in
// it, where each bundle downloads from and its SHA-256, so Enplace can show a
// store without downloading every bundle, and check each download against
// the list before installing it.
//
// A catalogue is one JSON file. Its signature covers everything else in it
// (see Digest), made with the same ed25519 key as the bundles it lists, so
// a server that trusts the publisher (ENPLACE_PLUGIN_KEYS) trusts the list.
package catalogue

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/bundle"
)

// MaxSize is the largest catalogue file Enplace reads.
const MaxSize = 2 << 20

// Kinds of plugin, as the store shelves them. A plugin can be several.
const (
	// KindSkills brings skills (contributes.skills).
	KindSkills = "skills"
	// KindMemory answers recall through its logic (contributes.recall).
	KindMemory = "memory"
)

// Catalogue is catalogue.json.
type Catalogue struct {
	Name      string  `json:"name"`
	Publisher string  `json:"publisher,omitempty"`
	Updated   string  `json:"updated,omitempty"` // RFC 3339
	Plugins   []Entry `json:"plugins"`
	// Key and Signature: the publisher's ed25519 public key and its
	// signature over Digest, both base64.
	Key       string `json:"key,omitempty"`
	Signature string `json:"signature,omitempty"`
}

// Entry is one plugin in a catalogue: enough of its manifest to show and
// shelve it, and where to get it.
type Entry struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Version  string         `json:"version"`
	Author   string         `json:"author,omitempty"`
	Summary  string         `json:"summary,omitempty"`
	Colour   string         `json:"colour,omitempty"`
	Icon     string         `json:"icon,omitempty"`
	Glyph    bundle.Glyph   `json:"glyph,omitempty"`
	Levels   []string       `json:"levels,omitempty"`
	Kinds    []string       `json:"kinds,omitempty"`
	Skills   []bundle.Skill `json:"skills,omitempty"`
	HasCode  bool           `json:"has_code,omitempty"` // logic or UI scripts
	Download string         `json:"download"`           // https, or relative to the catalogue
	SHA256   string         `json:"sha256"`
	Size     int            `json:"size,omitempty"`
}

// Kinds says what a manifest makes a plugin, for the store's shelves.
func Kinds(m bundle.Manifest) []string {
	var out []string
	if len(m.Contributes.Skills) > 0 {
		out = append(out, KindSkills)
	}
	if m.Contributes.Recall {
		out = append(out, KindMemory)
	}
	return out
}

// HasCode reports whether a bundle carries anything that runs: logic or UI
// scripts. A plugin without code only brings settings, looks and skills.
func HasCode(files map[string][]byte) bool {
	if bundle.HasLogic(files) {
		return true
	}
	for n := range files {
		if strings.HasPrefix(n, "ui/") {
			return true
		}
	}
	return false
}

// EntryFor describes a packed bundle, to be downloaded from download.
func EntryFor(data []byte, download string) (Entry, error) {
	b, err := bundle.Read(data, nil)
	if err != nil {
		return Entry{}, err
	}
	m := b.Manifest
	sum := sha256.Sum256(data)
	return Entry{
		ID: m.ID, Name: m.Name, Version: m.Version, Author: m.Author, Summary: m.Summary,
		Colour: m.Colour, Icon: m.Icon, Glyph: m.Glyph, Levels: m.OnLevels(), Kinds: Kinds(m),
		Skills: slices.Clone(m.Contributes.Skills), HasCode: HasCode(b.Files),
		Download: download, SHA256: hex.EncodeToString(sum[:]), Size: len(data),
	}, nil
}

// Digest is what the signature covers: the catalogue without its key and
// signature, as compact JSON, after a fixed prefix.
func (c *Catalogue) Digest() []byte {
	cp := *c
	cp.Key, cp.Signature = "", ""
	raw, _ := json.Marshal(cp)
	return append([]byte("enplace-catalogue-v1\n"), raw...)
}

// Sign signs the catalogue with a publisher key.
func (c *Catalogue) Sign(key ed25519.PrivateKey) {
	c.Key = base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey))
	c.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(key, c.Digest()))
}

// Trust is what a check found about a catalogue's signature.
type Trust string

// Trust values.
const (
	Verified Trust = bundle.Verified // signed by a trusted key
	Signed   Trust = bundle.Signed   // signed by a key this server doesn't trust
	Unsigned Trust = bundle.Unsigned
)

var (
	idRe   = regexp.MustCompile(`^[a-z][a-z0-9-]{1,39}$`)
	shaRe  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	errBad = errors.New("the catalogue isn't one Enplace can read")
)

// Parse reads a catalogue, checks its signature against the trusted keys
// and checks each entry. A signature that doesn't match is an error: the
// list was changed after it was signed.
func Parse(data []byte, trusted []string) (*Catalogue, Trust, error) {
	if len(data) > MaxSize {
		return nil, "", errors.New("the catalogue is bigger than 2 MB")
	}
	var c Catalogue
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&c); err != nil {
		return nil, "", errBad
	}
	if strings.TrimSpace(c.Name) == "" || len(c.Name) > 80 || len(c.Plugins) > 500 {
		return nil, "", errBad
	}
	trust := Unsigned
	if c.Signature != "" || c.Key != "" {
		key, err1 := base64.StdEncoding.DecodeString(c.Key)
		sig, err2 := base64.StdEncoding.DecodeString(c.Signature)
		if err1 != nil || err2 != nil || len(key) != ed25519.PublicKeySize {
			return nil, "", errors.New("the catalogue's signature is unreadable")
		}
		if !ed25519.Verify(key, c.Digest(), sig) {
			return nil, "", errors.New("the catalogue's signature doesn't match: it was changed after it was signed")
		}
		trust = Signed
		if slices.Contains(trusted, c.Key) {
			trust = Verified
		}
	}
	seen := map[string]bool{}
	for i, e := range c.Plugins {
		if !idRe.MatchString(e.ID) || seen[e.ID] {
			return nil, "", fmt.Errorf("entry %d: bad or repeated id %q", i+1, e.ID)
		}
		seen[e.ID] = true
		if e.Name == "" || len(e.Name) > 60 || len(e.Summary) > 300 {
			return nil, "", fmt.Errorf("%s: name or summary missing or too long", e.ID)
		}
		if _, err := bundle.ParseVersion(e.Version); err != nil {
			return nil, "", fmt.Errorf("%s: bad version %q", e.ID, e.Version)
		}
		if !shaRe.MatchString(e.SHA256) || e.Download == "" {
			return nil, "", fmt.Errorf("%s: needs a download link and its sha256", e.ID)
		}
	}
	return &c, trust, nil
}

// DownloadURL resolves an entry's download link against the catalogue's
// own address. Only https links come back.
func DownloadURL(catalogueURL string, e Entry) (string, error) {
	base, err := url.Parse(catalogueURL)
	if err != nil {
		return "", err
	}
	ref, err := url.Parse(e.Download)
	if err != nil {
		return "", fmt.Errorf("%s: bad download link", e.ID)
	}
	u := base.ResolveReference(ref)
	if u.Scheme != "https" || u.Host == "" || u.User != nil {
		return "", fmt.Errorf("%s: downloads only over https", e.ID)
	}
	return u.String(), nil
}

// Stamp sets Updated to now (for the publish tool).
func (c *Catalogue) Stamp(now time.Time) { c.Updated = now.UTC().Format(time.RFC3339) }

// Has reports whether the catalogue lists plugin id at this version.
func (c *Catalogue) Has(id, version string) bool {
	for _, e := range c.Plugins {
		if e.ID == id && e.Version == version {
			return true
		}
	}
	return false
}
