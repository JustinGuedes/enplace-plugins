// Package bundle reads, checks, packs and signs Enplace plugin bundles.
//
// A bundle is one zip file (".enplace") holding:
//
//	manifest.json   what the plugin is and what it adds (see Manifest)
//	logic.wasm      optional sandboxed logic (a WASI command module)
//	assets/...      optional images (icons, badge art) served to the UI
//	skills/...      optional skills the plugin brings (see skills.go)
//	LICENSE         optional licence the plugin's files come under
//	SIGNATURE       optional ed25519 signature over every other file
//
// Nothing in a bundle is trusted until it is checked here: paths, sizes,
// the manifest and the signature.
package bundle

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Limits keep a bundle from filling the database or memory.
const (
	MaxBundle = 20 << 20 // the whole zip
	MaxFile   = 10 << 20 // one file, unpacked
	MaxTotal  = 40 << 20 // every file, unpacked
	MaxFiles  = 400
)

// SignatureFile is the name of the signature inside a bundle.
const SignatureFile = "SIGNATURE"

// Signature states.
const (
	Verified = "verified" // signed by a key this Enplace trusts
	Signed   = "signed"   // signed, but by a key it doesn't know
	Unsigned = "unsigned"
)

// Bundle is an unpacked, checked plugin.
type Bundle struct {
	Manifest Manifest
	Files    map[string][]byte // every file but the signature, by path
	SHA256   string            // of the zip as uploaded
	// Signature is Verified, Signed or Unsigned; Key is the signer's public
	// key (base64) when there is one.
	Signature string
	Key       string
}

// Signature is the content of SIGNATURE.
type signature struct {
	Key string `json:"key"` // base64 ed25519 public key
	Sig string `json:"sig"` // base64 signature over Digest
}

var safePath = regexp.MustCompile(`^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$`)

// Read unpacks and checks a bundle. trusted are the base64 public keys whose
// signatures count as verified.
func Read(data []byte, trusted []string) (*Bundle, error) {
	if len(data) > MaxBundle {
		return nil, fmt.Errorf("the bundle is over %d MB", MaxBundle>>20)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, errors.New("not a plugin bundle (it should be a zip file)")
	}
	if len(zr.File) > MaxFiles {
		return nil, fmt.Errorf("too many files (%d, at most %d)", len(zr.File), MaxFiles)
	}
	b := &Bundle{Files: map[string][]byte{}, Signature: Unsigned}
	sum := sha256.Sum256(data)
	b.SHA256 = hex.EncodeToString(sum[:])
	var sigRaw []byte
	total := 0
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := f.Name
		if !safePath.MatchString(name) || strings.Contains(name, "..") {
			return nil, fmt.Errorf("unsafe file name %q", name)
		}
		if f.UncompressedSize64 > MaxFile {
			return nil, fmt.Errorf("%s is too big", name)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		body, err := io.ReadAll(io.LimitReader(rc, MaxFile+1))
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if len(body) > MaxFile {
			return nil, fmt.Errorf("%s is too big", name)
		}
		total += len(body)
		if total > MaxTotal {
			return nil, errors.New("the bundle unpacks to more than 40 MB")
		}
		if _, dup := b.Files[name]; dup {
			return nil, fmt.Errorf("%s appears twice", name)
		}
		if name == SignatureFile {
			sigRaw = body
			continue
		}
		b.Files[name] = body
	}
	raw, ok := b.Files["manifest.json"]
	if !ok {
		return nil, errors.New("the bundle has no manifest.json")
	}
	m, err := ParseManifest(raw)
	if err != nil {
		return nil, err
	}
	b.Manifest = *m
	if sigRaw != nil {
		var s signature
		if err := json.Unmarshal(sigRaw, &s); err != nil {
			return nil, errors.New("the signature is unreadable")
		}
		key, err1 := base64.StdEncoding.DecodeString(s.Key)
		sig, err2 := base64.StdEncoding.DecodeString(s.Sig)
		if err1 != nil || err2 != nil || len(key) != ed25519.PublicKeySize {
			return nil, errors.New("the signature is unreadable")
		}
		if !ed25519.Verify(key, Digest(b.Files), sig) {
			return nil, errors.New("the signature doesn't match the files: the bundle was changed after it was signed")
		}
		b.Key = s.Key
		b.Signature = Signed
		if slices.Contains(trusted, s.Key) {
			b.Signature = Verified
		}
	}
	// After the signature check: this takes skills' front matter off.
	if err := checkFiles(&b.Manifest, b.Files); err != nil {
		return nil, err
	}
	return b, nil
}

// checkFiles checks the files against the manifest and fills in what they
// describe: icons from their SVG files, and skills from their front matter
// (which comes off the files).
func checkFiles(m *Manifest, files map[string][]byte) error {
	if err := iconsFromFiles(m, files); err != nil {
		return err
	}
	if err := skillsFromFiles(m, files); err != nil {
		return err
	}
	if err := checkKindFiles(m, files); err != nil {
		return err
	}
	for _, s := range m.Contributes.Skills {
		if _, ok := SkillPath(files, s.Name); !ok {
			return fmt.Errorf("skill %s is listed but skills/%s.md is missing", s.Name, s.Name)
		}
	}
	return nil
}

// Digest is what a signature covers: every file's path and SHA-256, in path
// order, so the zip's own layout and timestamps don't matter.
func Digest(files map[string][]byte) []byte {
	names := make([]string, 0, len(files))
	for n := range files {
		if n != SignatureFile {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	var buf bytes.Buffer
	buf.WriteString("enplace-plugin-v1\n")
	for _, n := range names {
		h := sha256.Sum256(files[n])
		fmt.Fprintf(&buf, "%x  %s\n", h, n)
	}
	return buf.Bytes()
}

// Pack zips files into a bundle, signing it when key is set.
func Pack(files map[string][]byte, key ed25519.PrivateKey) ([]byte, error) {
	m, err := ParseManifest(files["manifest.json"])
	if err != nil {
		return nil, err
	}
	if err := checkFiles(m, maps.Clone(files)); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	write := func(name string, body []byte) error {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
		if err != nil {
			return err
		}
		_, err = w.Write(body)
		return err
	}
	for _, n := range names {
		if n == SignatureFile {
			continue
		}
		if err := write(n, files[n]); err != nil {
			return nil, err
		}
	}
	if key != nil {
		sig := ed25519.Sign(key, Digest(files))
		pub := key.Public().(ed25519.PublicKey)
		s, _ := json.Marshal(signature{Key: base64.StdEncoding.EncodeToString(pub), Sig: base64.StdEncoding.EncodeToString(sig)})
		if err := write(SignatureFile, s); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ContentType guesses the type a bundle file is served with. Anything
// unknown is served as plain bytes, never as HTML.
func ContentType(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json":
		return "application/json"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	case ".woff2":
		return "font/woff2"
	case ".woff":
		return "font/woff"
	}
	return "application/octet-stream"
}
