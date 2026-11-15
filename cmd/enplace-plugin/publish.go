package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/bundle"
	"github.com/JustinGuedes/enplace-plugins/sdk/catalogue"
)

// publishCmd packs plugin folders into a catalogue folder: bundles/ and a
// signed catalogue.json. Serve that folder and every Enplace that reads it
// sees the new versions in its store.
//
//	enplace-plugin publish plugins/plain-words --catalogue catalogue
//
// Each plugin's version must go up when its files change: a version that's
// already published with different files is refused.
func publishCmd(args []string) error {
	fset := flag.NewFlagSet("publish", flag.ContinueOnError)
	out := fset.String("catalogue", "", "the catalogue folder: it holds catalogue.json and bundles/")
	keyFile := fset.String("key", "", "private key file from keygen (or $ENPLACE_PLUGIN_SIGNING_KEY)")
	name := fset.String("name", "Enplace", "the catalogue's name, shown in the store")
	newOnly := fset.Bool("new-only", false, "skip plugins whose version is already published, and leave the catalogue alone when nothing is new")
	var dirs []string
	for len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		dirs, args = append(dirs, args[0]), args[1:]
	}
	if err := fset.Parse(args); err != nil {
		return err
	}
	dirs = append(dirs, fset.Args()...)
	if *out == "" || len(dirs) == 0 {
		return errors.New("usage: enplace-plugin publish DIR... --catalogue FOLDER [--key FILE] [--new-only]")
	}
	key, err := signingKey(*keyFile)
	if err != nil {
		return err
	}
	if key == nil {
		return errors.New("publishing needs the signing key: --key or $ENPLACE_PLUGIN_SIGNING_KEY")
	}
	catPath := filepath.Join(*out, "catalogue.json")
	cat := &catalogue.Catalogue{Name: *name, Publisher: *name}
	if raw, err := os.ReadFile(catPath); err == nil {
		if err := json.Unmarshal(raw, cat); err != nil {
			return fmt.Errorf("reading %s: %w", catPath, err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Join(*out, "bundles"), 0o755); err != nil {
		return err
	}
	published := 0
	for _, dir := range dirs {
		files, err := collect(dir)
		if err != nil {
			return fmt.Errorf("%s: %w", dir, err)
		}
		if *newOnly {
			if m, err := bundle.ParseManifest(files["manifest.json"]); err == nil && cat.Has(m.ID, m.Version) {
				fmt.Printf("%s %s is already published\n", m.ID, m.Version)
				continue
			}
		}
		if st, err := os.Stat(filepath.Join(dir, "logic")); err == nil && st.IsDir() {
			wasm, err := buildLogic(dir)
			if err != nil {
				return err
			}
			files["logic.wasm"] = wasm
		}
		data, err := bundle.Pack(files, key)
		if err != nil {
			return fmt.Errorf("%s: %w", dir, err)
		}
		b, err := bundle.Read(data, nil)
		if err != nil {
			return fmt.Errorf("%s: the packed bundle doesn't check out: %w", dir, err)
		}
		m := b.Manifest
		file := fmt.Sprintf("bundles/%s-%s.enplace", m.ID, m.Version)
		e, err := catalogue.EntryFor(data, file)
		if err != nil {
			return err
		}
		replaced := false
		for i, old := range cat.Plugins {
			if old.ID != m.ID {
				continue
			}
			if old.Version == m.Version && !sameBundle(filepath.Join(*out, file), b) {
				return fmt.Errorf("%s %s is already published with different files: bump its version", m.ID, m.Version)
			}
			cat.Plugins[i], replaced = e, true
		}
		if !replaced {
			cat.Plugins = append(cat.Plugins, e)
		}
		if err := os.WriteFile(filepath.Join(*out, file), data, 0o644); err != nil {
			return err
		}
		fmt.Printf("Published %s %s (%d KB)\n", m.ID, m.Version, len(data)/1024)
		published++
	}
	if *newOnly && published == 0 {
		fmt.Println("Nothing new to publish.")
		return nil
	}
	sort.Slice(cat.Plugins, func(i, j int) bool { return cat.Plugins[i].ID < cat.Plugins[j].ID })
	cat.Stamp(time.Now())
	cat.Sign(key)
	signed, err := json.MarshalIndent(cat, "", "  ")
	if err != nil {
		return err
	}
	if _, _, err := catalogue.Parse(signed, nil); err != nil {
		return fmt.Errorf("the catalogue doesn't check out: %w", err)
	}
	if err := os.WriteFile(catPath, append(signed, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Printf("Wrote %s with %d plugins, signed. Deploy %s to publish.\n", catPath, len(cat.Plugins), *out)
	return nil
}

// sameBundle reports whether the published file has the same contents as b
// (signatures aside: the digest covers every file).
func sameBundle(path string, b *bundle.Bundle) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	old, err := bundle.Read(raw, nil)
	if err != nil {
		return false
	}
	return bytes.Equal(bundle.Digest(old.Files), bundle.Digest(b.Files))
}

// buildLogic builds a plugin's logic/ for WASI, small and reproducible.
func buildLogic(dir string) ([]byte, error) {
	tmp, err := os.CreateTemp("", "logic-*.wasm")
	if err != nil {
		return nil, err
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags", "-s -w", "-o", tmp.Name(), "./"+filepath.ToSlash(filepath.Join(dir, "logic")))
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("building %s/logic: %v\n%s", dir, err, out)
	}
	return os.ReadFile(tmp.Name())
}
