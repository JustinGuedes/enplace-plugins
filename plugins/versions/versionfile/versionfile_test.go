package versionfile

import (
	"reflect"
	"testing"
)

func TestCandidates(t *testing.T) {
	got := Candidates([]string{
		"README.md",
		"web/package.json",
		"web/node_modules/x/package.json",
		"VERSION",
		"Enplace/Info.plist",
		"project.yml",
		"Enplace.xcodeproj/project.pbxproj",
		"Config/Version.xcconfig",
		"Config/Shared.xcconfig",
		"a/b/c/d/e/package.json",
		".github/package.json",
	})
	want := []string{"project.yml", "Config/Version.xcconfig", "web/package.json", "VERSION", "Enplace.xcodeproj/project.pbxproj", "Config/Shared.xcconfig", "Enplace/Info.plist"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestRead(t *testing.T) {
	cases := []struct{ path, body, want string }{
		{"project.yml", "name: Enplace\nsettings:\n  base:\n    MARKETING_VERSION: 1.5\n    CURRENT_PROJECT_VERSION: 42\n", "1.5"},
		{"project.yml", "settings:\n  MARKETING_VERSION: \"2.0.1\"\n", "2.0.1"},
		{"App.xcodeproj/project.pbxproj", "\t\t\t\tMARKETING_VERSION = 1.4.2;\n", "1.4.2"},
		{"App.xcodeproj/project.pbxproj", "\t\t\t\t\"MARKETING_VERSION[sdk=iphoneos*]\" = 2.1;\n", "2.1"},
		{"project.yml", "settings: {CURRENT_PROJECT_VERSION: 7, MARKETING_VERSION: 3.0}\n", "3.0"},
		{"Version.xcconfig", "// Bump before each release.\n// MARKETING_VERSION = 9.9\nMARKETING_VERSION = 0.3.0\nCURRENT_PROJECT_VERSION = 42\n", "0.3.0"},
		{"Config/Release.xcconfig", "#include \"Base.xcconfig\"\nMARKETING_VERSION[config=Release] = 1.2 // shipped\n", "1.2"},
		{"package.json", `{"name":"x","version":"0.3.0","dependencies":{"a":"1.0"}}`, "0.3.0"},
		{"pubspec.yaml", "name: x\nversion: 1.4.0+12\n", "1.4.0"},
		{"app.json", `{"expo":{"name":"x","version":"3.1.0"}}`, "3.1.0"},
		{"android/app/build.gradle", "android {\n  defaultConfig {\n    versionCode 12\n    versionName \"1.4\"\n  }\n}", "1.4"},
		{"build.gradle.kts", "    versionName = \"2.2.0\"\n", "2.2.0"},
		{"Cargo.toml", "[dependencies]\nserde = { version = \"1\" }\n[package]\nname = \"x\"\nversion = \"0.9.1\"\n", "0.9.1"},
		{"pyproject.toml", "[tool.poetry]\nversion = \"1.2.3\"\n", "1.2.3"},
		{"Info.plist", "<key>CFBundleShortVersionString</key>\n\t<string>1.7</string>", "1.7"},
		{"VERSION", "v1.8.0\n", "1.8.0"},
		{"internal/version.go", "package x\n\nconst Version = \"0.4.1\"\n", "0.4.1"},
	}
	for _, c := range cases {
		got, err := Read(c.path, []byte(c.body))
		if err != nil || got != c.want {
			t.Errorf("%s: got %q, %v; want %q", c.path, got, err, c.want)
		}
	}
	for _, c := range []struct{ path, body string }{
		{"Info.plist", "<key>CFBundleShortVersionString</key>\n\t<string>$(MARKETING_VERSION)</string>"},
		{"package.json", `{"name":"x"}`},
		{"Base.xcconfig", "MARKETING_VERSION = $(APP_VERSION)\n"},
		{"Debug.xcconfig", "SWIFT_VERSION = 5.0\n"},
		{"README.md", "Nothing here"},
	} {
		if v, err := Read(c.path, []byte(c.body)); err == nil {
			t.Errorf("%s: read %q, want none", c.path, v)
		}
	}
}

func TestBuild(t *testing.T) {
	cases := []struct{ path, body, want string }{
		{"Version.xcconfig", "MARKETING_VERSION = 0.3.0\nCURRENT_PROJECT_VERSION = 42\n", "42"},
		{"project.yml", "settings:\n  base:\n    MARKETING_VERSION: 1.5\n    CURRENT_PROJECT_VERSION: 42\n", "42"},
		{"App.xcodeproj/project.pbxproj", "\t\t\t\tCURRENT_PROJECT_VERSION = 7;\n", "7"},
		{"pubspec.yaml", "version: 1.4.0+12\n", "12"},
		{"Version.xcconfig", "CURRENT_PROJECT_VERSION = $(BUILD)\n", ""},
		{"package.json", `{"version":"1.0.0"}`, ""},
	}
	for _, c := range cases {
		if got := Build(c.path, []byte(c.body)); got != c.want {
			t.Errorf("%s: got %q, want %q", c.path, got, c.want)
		}
	}
}

func TestCompare(t *testing.T) {
	for _, c := range [][2]string{{"1.4", "1.4.0"}, {"v1.4", "1.4"}, {"2", "2.0.0"}, {"1.04", "1.4"}} {
		if !Same(c[0], c[1]) {
			t.Errorf("%s and %s should be the same", c[0], c[1])
		}
	}
	for _, c := range [][2]string{{"1.4", "1.4.1"}, {"1.5", "1.4"}} {
		if Same(c[0], c[1]) {
			t.Errorf("%s and %s should differ", c[0], c[1])
		}
	}
	if !Newer("1.10", "1.9") || Newer("1.4", "1.4.0") || !Newer("2.0", "1.99.9") || Newer("x", "1.0") {
		t.Error("Newer is wrong")
	}
	if FromTag("v1.4.0") != "1.4.0" || FromTag("release-2.1") != "2.1" || FromTag("latest") != "" {
		t.Error("FromTag is wrong")
	}
}
