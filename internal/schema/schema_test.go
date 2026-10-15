package schema

import (
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/vkdprojects/vkd/internal/blueprint"
)

func registry(t *testing.T) *blueprint.Registry {
	t.Helper()
	r := blueprint.NewRegistry()
	err := r.LoadFS(fstest.MapFS{
		"base/blueprint.yaml": {Data: []byte("name: base\nkind: base\nsummary: s\n")},
		"api/blueprint.yaml": {Data: []byte(`name: api
kind: stack
summary: s
requires: [base]
questions:
  - {id: module, prompt: Go module, default: "github.com/{{.Owner}}/{{.Name}}"}
  - {id: tier, prompt: Tier, options: [free, pro], required: true}
`)},
		"plain/blueprint.yaml": {Data: []byte("name: plain\nkind: addon\nsummary: s\nrequires: [base]\n")},
	}, ".", "test")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCreateSchemaIsDerivedFromTheRegistry(t *testing.T) {
	raw, err := json.Marshal(Create(registry(t)))
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Schema     string   `json:"$schema"`
		Required   []string `json:"required"`
		Properties map[string]struct {
			Enum       []string `json:"enum"`
			Items      struct{ Enum []string }
			Properties map[string]struct {
				Required             []string `json:"required"`
				AdditionalProperties *bool    `json:"additionalProperties"`
				Properties           map[string]struct {
					Default string   `json:"default"`
					Enum    []string `json:"enum"`
				} `json:"properties"`
			} `json:"properties"`
		} `json:"properties"`
		AdditionalProperties *bool `json:"additionalProperties"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	if s.Schema != "https://json-schema.org/draft/2020-12/schema" {
		t.Errorf("$schema = %q", s.Schema)
	}
	if strings.Join(s.Required, ",") != "name,owner" {
		t.Errorf("required = %v", s.Required)
	}
	if s.AdditionalProperties == nil || *s.AdditionalProperties {
		t.Error("an unknown top-level key must be rejected, or a typo like `oss` is silently ignored")
	}
	// components list stacks and addons, never the always-on base.
	if got := strings.Join(s.Properties["components"].Items.Enum, ","); got != "api,plain" {
		t.Errorf("components enum = %q", got)
	}
	if strings.Join(s.Properties["license"].Enum, ",") != "MIT,Apache-2.0,AGPL-3.0,proprietary" {
		t.Errorf("license enum = %v", s.Properties["license"].Enum)
	}
	per := s.Properties["per"].Properties
	if _, ok := per["plain"]; ok {
		t.Error("a blueprint without questions has no entry under per")
	}
	api, ok := per["api"]
	if !ok {
		t.Fatalf("api questions missing from per: %v", per)
	}
	if strings.Join(api.Required, ",") != "tier" {
		t.Errorf("api required = %v: only `required: true` questions are required", api.Required)
	}
	if api.Properties["module"].Default != "github.com/{{.Owner}}/{{.Name}}" {
		t.Errorf("module default = %q", api.Properties["module"].Default)
	}
	if strings.Join(api.Properties["tier"].Enum, ",") != "free,pro" {
		t.Errorf("tier enum = %v", api.Properties["tier"].Enum)
	}
	if api.AdditionalProperties == nil || *api.AdditionalProperties {
		t.Error("an unknown question id must be rejected")
	}
}
