// Package schema builds the JSON Schema of `vkd create` input from the loaded blueprints, so the schema,
// the flags and the plan can never disagree.
package schema

import (
	"github.com/vkdprojects/vkd/internal/answers"
	"github.com/vkdprojects/vkd/internal/blueprint"
)

// Create returns the JSON Schema (draft 2020-12) of the create input: the global answers plus one object per
// blueprint that has questions, under "answers".
func Create(reg *blueprint.Registry) map[string]any {
	components := []any{}
	per := map[string]any{}
	for _, b := range reg.All() {
		if b.Kind != blueprint.KindBase {
			components = append(components, b.Name)
		}
		if len(b.Questions) == 0 {
			continue
		}
		props := map[string]any{}
		var required []any
		for _, q := range b.Questions {
			p := map[string]any{"type": "string", "description": q.Prompt}
			if q.Default != "" {
				p["default"] = q.Default
			}
			if len(q.Options) > 0 {
				opts := make([]any, len(q.Options))
				for i, o := range q.Options {
					opts[i] = o
				}
				p["enum"] = opts
			}
			props[q.ID] = p
			if q.Required {
				required = append(required, q.ID)
			}
		}
		obj := map[string]any{
			"type":                 "object",
			"description":          "Answers for blueprint " + b.Name + " (flag --" + b.Name + ".<id> or --set " + b.Name + ".<id>=value)",
			"properties":           props,
			"additionalProperties": false,
		}
		if len(required) > 0 {
			obj["required"] = required
		}
		per[b.Name] = obj
	}
	licenses := make([]any, len(answers.Licenses))
	for i, l := range answers.Licenses {
		licenses[i] = l
	}
	return map[string]any{
		"$schema":     "https://json-schema.org/draft/2020-12/schema",
		"title":       "vkd create input",
		"description": "Answers recorded by `vkd create` in .vkd/answers.yaml. Run `vkd list --json` for what each blueprint does.",
		"type":        "object",
		"required":    []any{"name", "owner"},
		"properties": map[string]any{
			"name":        map[string]any{"type": "string", "pattern": "^[a-z][a-z0-9-]*$", "description": "Project name (argument NAME)"},
			"owner":       map[string]any{"type": "string", "description": "GitHub owner of the repository (--owner)"},
			"description": map[string]any{"type": "string", "description": "One-line description (--description)"},
			"open_source": map[string]any{"type": "boolean", "default": false, "description": "Open source project: adds CONTRIBUTING, DCO, templates (--oss / --no-oss)"},
			"license":     map[string]any{"type": "string", "enum": licenses, "description": "License (--license); default MIT with --oss, else proprietary"},
			"port_base":   map[string]any{"type": "integer", "minimum": 1024, "maximum": 65000, "description": "First port of the project's 10-port block (--port-base); default derived from the name"},
			"components": map[string]any{
				"type": "array", "uniqueItems": true,
				"items":       map[string]any{"type": "string", "enum": components},
				"description": "Blueprints to include (--stack a,b). Base is always on; requirements and addons whose `when` holds are added automatically.",
			},
			"per": map[string]any{
				"type":                 "object",
				"description":          "Per-blueprint answers",
				"properties":           per,
				"additionalProperties": false,
			},
		},
		"additionalProperties": false,
	}
}
