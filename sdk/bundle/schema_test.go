package bundle

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite schema/manifest.schema.json")

const schemaPath = "../../schema/manifest.schema.json"

// The JSON schema editors use comes from the Manifest type itself, so it
// can't drift: change the type, then run go test ./sdk/bundle -update.
func TestSchemaIsCurrent(t *testing.T) {
	got := manifestSchema()
	if *update {
		if err := os.WriteFile(schemaPath, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Error("schema/manifest.schema.json is out of date: run go test ./sdk/bundle -update")
	}
}

func manifestSchema() []byte {
	s := schemaFor(reflect.TypeFor[Manifest]())
	s["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	s["$id"] = "https://raw.githubusercontent.com/JustinGuedes/enplace-plugins/main/schema/manifest.schema.json"
	s["title"] = "Enplace plugin manifest"
	s["required"] = []string{"id", "name", "version"}
	props := s["properties"].(map[string]any)
	props["kind"] = map[string]any{"enum": KindNames()}
	props["scope"] = map[string]any{"enum": []string{"workspace", "project", "both"}}
	props["inherit"] = map[string]any{"enum": []string{InheritDown, InheritOffer, InheritNone}}
	props["levels"] = map[string]any{"type": "array", "uniqueItems": true, "items": map[string]any{"enum": LevelNames}}
	// Each kind may only contribute its own pieces.
	var rules []any
	for _, k := range KindNames() {
		pieces := map[string]any{}
		for _, p := range kinds[k].pieces {
			pieces[p] = true
		}
		rules = append(rules, map[string]any{
			"if":   map[string]any{"properties": map[string]any{"kind": map[string]any{"const": k}}, "required": []string{"kind"}},
			"then": map[string]any{"properties": map[string]any{"contributes": map[string]any{"propertyNames": map[string]any{"enum": sortedKeys(pieces)}}}},
		})
	}
	s["allOf"] = rules
	out, _ := json.MarshalIndent(s, "", "  ")
	return append(out, '\n')
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// schemaFor describes a Go type by its JSON shape.
func schemaFor(t reflect.Type) map[string]any {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Implements(reflect.TypeFor[json.Marshaler]()) || reflect.PointerTo(t).Implements(reflect.TypeFor[json.Unmarshaler]()) {
		return map[string]any{} // custom JSON: anything
	}
	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int64, reflect.Int32:
		return map[string]any{"type": "integer"}
	case reflect.Float64, reflect.Float32:
		return map[string]any{"type": "number"}
	case reflect.Slice:
		return map[string]any{"type": "array", "items": schemaFor(t.Elem())}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": schemaFor(t.Elem())}
	case reflect.Struct:
		props := map[string]any{}
		for i := range t.NumField() {
			f := t.Field(i)
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if !f.IsExported() || name == "-" || name == "" {
				continue
			}
			props[name] = schemaFor(f.Type)
		}
		return map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	}
	return map[string]any{}
}
