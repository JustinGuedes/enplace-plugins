package bundle

import (
	"errors"
	"strings"
	"testing"
)

func TestTypesAndNeeds(t *testing.T) {
	m, err := ParseManifest([]byte(`{"id":"beta","name":"Beta","version":"1.0.0","kind":"service","type":"beta",
		"needs":[{"type":"ci","contract":"^1","for":"to get builds"},{"type":"store","optional":true,"scope":"product"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if m.Type != "beta" || len(m.Needs) != 2 || m.Needs[0].For != "to get builds" || !m.Needs[1].Optional {
		t.Fatalf("got %+v", m)
	}
}

func TestSourceIsWorkType(t *testing.T) {
	m, err := ParseManifest([]byte(`{"id":"src","name":"Src","version":"1.0.0","kind":"source"}`))
	if err != nil || m.Type != "work" {
		t.Fatalf("type %q, err %v", m.Type, err)
	}
}

func TestLifecycleRejects(t *testing.T) {
	for _, c := range []struct{ json, want string }{
		{`"kind":"agent","type":"ci","contributes":{"runner":{"agent":"X","where":"computer","pays":"plan","kind":"prompt","prompt":"{key}"}}`, "has no type"},
		{`"kind":"service","type":"Not a key"`, "is a key"},
		{`"kind":"service","needs":[{"type":"ci"},{"type":"ci"}]`, "listed twice"},
		{`"kind":"service","needs":[{"type":"ci","contract":"one"}]`, "contract"},
		{`"kind":"service","needs":[{"type":"ci","scope":"galaxy"}]`, "scope"},
		{`"kind":"extension","contributes":{"stages":[{"key":"build","name":"Build"}]}`, "repeated stage"},
		{`"kind":"extension","contributes":{"types":[{"key":"ci","name":"CI","stage":"build","contract":1}]}`, "repeated type"},
		{`"kind":"extension","contributes":{"types":[{"key":"crash","name":"Crashes","stage":"moon","contract":1}]}`, "stage \"moon\""},
		{`"kind":"extension","contributes":{"types":[{"key":"crash","name":"Crashes","stage":"watch","contract":0}]}`, "contract starts at 1"},
		{`"kind":"extension","contributes":{"types":[{"key":"crash","name":"Crashes","stage":"watch","contract":1,"publishes":["crashes"]}]}`, "public state key"},
	} {
		_, err := ParseManifest([]byte(`{"id":"demo","name":"Demo","version":"1.0.0",` + c.json + `}`))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want %q", c.json, err, c.want)
		}
	}
}

// A plugin can bring a stage and a type of its own, and another plugin
// can be of that type.
func TestPluginDefinedTypes(t *testing.T) {
	m, err := ParseManifest([]byte(`{"id":"print","name":"Print","version":"1.0.0","kind":"extension","type":"printer",
		"contributes":{"stages":[{"key":"print","name":"Print","order":35}],
		"types":[{"key":"printer","name":"Printer","stage":"print","contract":1,"publishes":["public.print"]}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if m.Contributes.Types[0].View != "plugin" {
		t.Errorf("a plugin's own type is drawn by the plugin, got %q", m.Contributes.Types[0].View)
	}
}

func TestNewerFormatIsTurnedAway(t *testing.T) {
	_, err := ParseManifest([]byte(`{"id":"demo","name":"Demo","version":"1.0.0","format":99,"some_future_field":true}`))
	if !errors.Is(err, ErrNewerFormat) {
		t.Fatalf("got %v", err)
	}
	if _, err := ParseManifest([]byte(`{"id":"demo","name":"Demo","version":"1.0.0","format":2}`)); err != nil {
		t.Fatal(err)
	}
}

func TestBuiltInCatalogue(t *testing.T) {
	for _, ty := range Types {
		if _, ok := StageByKey(ty.Stage); !ok {
			t.Errorf("type %s sits in unknown stage %s", ty.Key, ty.Stage)
		}
		if ty.Contract < 1 || ty.Name == "" {
			t.Errorf("type %s: needs a name and contract", ty.Key)
		}
	}
	if s, ok := StageByKey("build"); !ok || s.Page != "Builds" {
		t.Errorf("build stage: %+v", s)
	}
}

func TestPerTrackSettingsAndPurposes(t *testing.T) {
	m, err := ParseManifest([]byte(`{"id":"ci","name":"CI","version":"1.0.0","kind":"service","type":"ci","format":3,
		"settings":[{"key":"workflow","name":"Workflow","type":"text","scope":"project","per":"track"}],
		"contributes":{"purposes":[{"key":"nightly","name":"Nightly"}]}}`))
	if err != nil || m.Settings[0].Per != "track" || m.Contributes.Purposes[0].Key != "nightly" {
		t.Fatalf("%+v %v", m, err)
	}
	for _, c := range []struct{ json, want string }{
		{`"settings":[{"key":"k","name":"K","type":"secret","scope":"project","per":"track"}]`, "per must be track"},
		{`"settings":[{"key":"k","name":"K","type":"text","scope":"project","per":"release"}]`, "per must be track"},
		{`"contributes":{"purposes":[{"key":"internal","name":"Mine"}]}`, "needs a new key"},
	} {
		_, err := ParseManifest([]byte(`{"id":"demo","name":"Demo","version":"1.0.0","kind":"service",` + c.json + `}`))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want %q", c.json, err, c.want)
		}
	}
	if p, ok := PurposeByKey("check"); !ok || p.Makes {
		t.Fatal("a check track makes nothing")
	}
}
