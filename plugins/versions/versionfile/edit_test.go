package versionfile

import "testing"

func TestEdits(t *testing.T) {
	cases := []struct{ path, body, version, build, want string }{
		{"Version.xcconfig", "// MARKETING_VERSION = 9.9\nMARKETING_VERSION = 0.3.0\nCURRENT_PROJECT_VERSION = 42\n", "0.4.0", "43",
			"// MARKETING_VERSION = 9.9\nMARKETING_VERSION = 0.4.0\nCURRENT_PROJECT_VERSION = 43\n"},
		{"App.xcodeproj/project.pbxproj", "\t\tMARKETING_VERSION = 1.4;\n\t\tCURRENT_PROJECT_VERSION = 7;\n\t\tMARKETING_VERSION = 1.4;\n", "1.5", "",
			"\t\tMARKETING_VERSION = 1.5;\n\t\tCURRENT_PROJECT_VERSION = 7;\n\t\tMARKETING_VERSION = 1.5;\n"},
		{"project.yml", "settings:\n  MARKETING_VERSION: \"2.0.1\"\n", "2.1.0", "", "settings:\n  MARKETING_VERSION: \"2.1.0\"\n"},
		{"package.json", "{\n  \"name\": \"x\",\n  \"version\": \"0.3.0\",\n  \"dependencies\": {}\n}\n", "0.4.0", "9",
			"{\n  \"name\": \"x\",\n  \"version\": \"0.4.0\",\n  \"dependencies\": {}\n}\n"},
		{"pubspec.yaml", "name: x\nversion: 1.4.0+12\n", "1.5.0", "13", "name: x\nversion: 1.5.0+13\n"},
		{"pubspec.yaml", "name: x\nversion: 1.4.0+12\n", "1.5.0", "", "name: x\nversion: 1.5.0+12\n"},
		{"Cargo.toml", "[dependencies]\nserde = { version = \"1\" }\n[package]\nversion = \"0.9.1\"\n", "0.10.0", "",
			"[dependencies]\nserde = { version = \"1\" }\n[package]\nversion = \"0.10.0\"\n"},
		{"Info.plist", "<key>CFBundleShortVersionString</key>\n\t<string>1.7</string>", "1.8", "", "<key>CFBundleShortVersionString</key>\n\t<string>1.8</string>"},
		{"VERSION", "v1.8.0\n", "1.9.0", "", "v1.9.0\n"},
	}
	for _, c := range cases {
		edits, err := Edits(c.path, []byte(c.body), c.version, c.build)
		if err != nil {
			t.Errorf("%s: %v", c.path, err)
			continue
		}
		got, ok := Apply(c.body, edits)
		if !ok || got != c.want {
			t.Errorf("%s: got %q (fits %v), want %q", c.path, got, ok, c.want)
		}
		if v, _ := Read(c.path, []byte(got)); v != c.version && v != "v"+c.version {
			t.Errorf("%s: reads back as %q", c.path, v)
		}
	}
	if _, err := Edits("Base.xcconfig", []byte("MARKETING_VERSION = $(V)\n"), "1.0", ""); err == nil {
		t.Error("a file without a version can't be edited")
	}
	if _, ok := Apply("MARKETING_VERSION = 0.5.0\n", []Edit{{Old: "MARKETING_VERSION = 0.3.0", New: "MARKETING_VERSION = 0.4.0"}}); ok {
		t.Error("an edit that no longer fits must say so")
	}
}

func TestNext(t *testing.T) {
	cases := []struct{ v, style, bump, want string }{
		{"1.4", Short, Minor, "1.5"},
		{"1.4", Short, Major, "2.0"},
		{"1.4", Short, Patch, "1.4.1"},
		{"0.3.0", Semantic, Minor, "0.4.0"},
		{"", Semantic, Minor, "1.0.0"},
		{"14", Number, Minor, "15"},
		{"2026.11", Calendar, Minor, "2026.11.1"},
		{"2026.10.2", Calendar, Minor, "2026.11"},
	}
	for _, c := range cases {
		if got := Next(c.v, c.style, c.bump, "2026-11-20"); got != c.want {
			t.Errorf("Next(%q, %s, %s) = %q, want %q", c.v, c.style, c.bump, got, c.want)
		}
	}
	for b, want := range map[string]string{"42": "43", "": "1", "1.4.9": "1.4.10", "abc": "abc"} {
		if got := NextBuild(b); got != want {
			t.Errorf("NextBuild(%q) = %q, want %q", b, got, want)
		}
	}
}
