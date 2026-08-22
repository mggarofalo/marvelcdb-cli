package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The whole point of the Extra machinery is that `-o json` gives back exactly
// what MarvelCDB sent, including fields this build has never heard of and the
// explicit nulls it sprinkles through nested objects.
func TestModelsRoundTripVerbatim(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		file   string
		decode func([]byte) (any, error)
	}{
		{
			name: "card with unknown field and nested nulls",
			file: "card.json",
			decode: func(b []byte) (any, error) {
				var c Card
				return &c, json.Unmarshal(b, &c)
			},
		},
		{
			name: "deck with empty tags and null ignore list",
			file: "deck.json",
			decode: func(b []byte) (any, error) {
				var d Deck
				return &d, json.Unmarshal(b, &d)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			raw, err := os.ReadFile(filepath.Join("testdata", tt.file))
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			decoded, err := tt.decode(raw)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			out, err := json.Marshal(decoded)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}

			var want, got any
			if err := json.Unmarshal(raw, &want); err != nil {
				t.Fatalf("decode fixture: %v", err)
			}
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatalf("decode result: %v", err)
			}
			assertJSONEqual(t, want, got)
		})
	}
}

func TestExtraFieldsCapturesOmittedForms(t *testing.T) {
	t.Parallel()

	var c Card
	raw := []byte(`{"code":"01001a","attack":null,"traits":"","brand_new":42}`)
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("decode: %v", err)
	}

	for _, key := range []string{"attack", "traits", "brand_new"} {
		if _, ok := c.Extra[key]; !ok {
			t.Errorf("expected %q to be retained in Extra, got keys %v", key, mapKeys(c.Extra))
		}
	}
	if c.Code != "01001a" {
		t.Errorf("Code = %q, want 01001a", c.Code)
	}
}

func TestMergeExtrasDoesNotOverwriteRealFields(t *testing.T) {
	t.Parallel()

	c := Card{
		Code:  "01001a",
		Name:  "Spider-Man",
		Extra: map[string]json.RawMessage{"code": json.RawMessage(`"tampered"`)},
	}
	out, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded["code"] != "01001a" {
		t.Errorf("code = %v, want the struct field to win", decoded["code"])
	}
}

func mapKeys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// assertJSONEqual compares decoded JSON, reporting the first path that differs.
func assertJSONEqual(t *testing.T, want, got any) {
	t.Helper()
	if diff := jsonDiff("", want, got); diff != "" {
		t.Errorf("JSON differs: %s", diff)
	}
}

func jsonDiff(path string, want, got any) string {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return path + ": type changed"
		}
		for k, wv := range w {
			gv, present := g[k]
			if !present {
				return path + "." + k + ": missing"
			}
			if d := jsonDiff(path+"."+k, wv, gv); d != "" {
				return d
			}
		}
		for k := range g {
			if _, present := w[k]; !present {
				return path + "." + k + ": unexpected"
			}
		}
		return ""
	case []any:
		g, ok := got.([]any)
		if !ok {
			return path + ": type changed"
		}
		if len(w) != len(g) {
			return path + ": length changed"
		}
		for i := range w {
			if d := jsonDiff(path+"[]", w[i], g[i]); d != "" {
				return d
			}
		}
		return ""
	default:
		if want != got {
			return path + ": values differ"
		}
		return ""
	}
}
