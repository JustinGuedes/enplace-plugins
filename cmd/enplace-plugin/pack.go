package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/JustinGuedes/enplace-plugins/sdk/bundle"
)

// pluginCmd is the plugin author's tooling: make a signing key, and pack a
// plugin folder into a bundle file Enplace can install.
func pluginCmd(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: enplace-plugin keygen|pack|publish|vendor")
	}
	switch args[0] {
	case "keygen":
		return keygenCmd(args[1:])
	case "pack":
		return packCmd(args[1:])
	case "publish":
		return publishCmd(args[1:])
	case "vendor":
		return vendorCmd(args[1:])
	}
	return fmt.Errorf("unknown plugin command %q", args[0])
}

func packCmd(args []string) error {
	fset := flag.NewFlagSet("pack", flag.ContinueOnError)
	logic := fset.String("logic", "", "a built logic.wasm to include")
	keyFile := fset.String("key", "", "private key file from keygen, to sign with (or $ENPLACE_PLUGIN_SIGNING_KEY)")
	out := fset.String("out", "", "the bundle file to write")
	var dir string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		dir, args = args[0], args[1:]
	}
	if err := fset.Parse(args); err != nil {
		return err
	}
	if dir == "" && fset.NArg() > 0 {
		dir = fset.Arg(0)
	}
	if dir == "" || *out == "" {
		return errors.New("usage: enplace-plugin pack DIR [--logic FILE] [--key FILE] --out FILE")
	}
	files, err := collect(dir)
	if err != nil {
		return err
	}
	if *logic != "" {
		b, err := os.ReadFile(*logic)
		if err != nil {
			return err
		}
		files["logic.wasm"] = b
	}
	key, err := signingKey(*keyFile)
	if err != nil {
		return err
	}
	data, err := bundle.Pack(files, key)
	if err != nil {
		return err
	}
	if _, err := bundle.Read(data, nil); err != nil {
		return fmt.Errorf("the packed bundle doesn't check out: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		return err
	}
	signed := "unsigned"
	if key != nil {
		signed = "signed"
	}
	fmt.Printf("Wrote %s (%d KB, %s)\n", *out, len(data)/1024, signed)
	return nil
}

// signingKey reads the publisher's private key from a file, or from
// $ENPLACE_PLUGIN_SIGNING_KEY; nil when there's neither.
func signingKey(file string) (ed25519.PrivateKey, error) {
	seed := strings.TrimSpace(os.Getenv("ENPLACE_PLUGIN_SIGNING_KEY"))
	if file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		seed = strings.TrimSpace(string(b))
	}
	if seed == "" {
		return nil, nil
	}
	raw, err := base64.StdEncoding.DecodeString(seed)
	if err != nil || len(raw) != ed25519.SeedSize {
		return nil, errors.New("the signing key isn't one from enplace-plugin keygen")
	}
	return ed25519.NewKeyFromSeed(raw), nil
}

// collect reads manifest.json, its README, icon and LICENSE, and assets/,
// icons/ and skills/ from a plugin folder. Source code (like logic/) and
// upstream.json stay out of the bundle.
func collect(dir string) (map[string][]byte, error) {
	files := map[string][]byte{}
	m, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, err
	}
	var head struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(m, &head); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Join(dir, "manifest.json"), err)
	}
	if head.Kind == "" {
		return nil, fmt.Errorf("%s: name its kind, one of %s", filepath.Join(dir, "manifest.json"), strings.Join(bundle.KindNames(), ", "))
	}
	files["manifest.json"] = m
	for _, extra := range []string{"README.md", "icon.svg", "LICENSE"} {
		if b, err := os.ReadFile(filepath.Join(dir, extra)); err == nil {
			files[extra] = b
		}
	}
	for _, sub := range []string{"assets", "icons", "skills"} {
		root := filepath.Join(dir, sub)
		if _, err := os.Stat(root); err != nil {
			continue
		}
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, _ := filepath.Rel(dir, p)
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			files[filepath.ToSlash(rel)] = b
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return files, nil
}

// keygenCmd makes a publisher key pair: the private key goes to a file only
// its owner can read, the public key is printed for servers to trust.
func keygenCmd(args []string) error {
	fset := flag.NewFlagSet("keygen", flag.ContinueOnError)
	out := fset.String("out", "", "where to write the private key")
	if err := fset.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return errors.New("usage: enplace-plugin keygen --out FILE")
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, []byte(base64.StdEncoding.EncodeToString(priv.Seed())+"\n"), 0o600); err != nil {
		return err
	}
	fmt.Println("Private key written to", *out, "(keep it secret).")
	fmt.Println("Public key, for ENPLACE_PLUGIN_KEYS on servers that should trust it:")
	fmt.Println(base64.StdEncoding.EncodeToString(pub))
	return nil
}
