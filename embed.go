// Package vkd embeds the shipped blueprints and the pinned versions.
package vkd

import (
	"embed"
	"fmt"

	"gopkg.in/yaml.v3"
)

// Blueprints holds blueprints/<name>/... . `all:` keeps dotfiles (.gitignore, .github/, .env.example).
//
//go:embed all:blueprints
var Blueprints embed.FS

//go:embed versions.yaml
var versionsYAML []byte

// Versions returns the parsed versions.yaml.
func Versions() (map[string]any, error) {
	var v map[string]any
	if err := yaml.Unmarshal(versionsYAML, &v); err != nil {
		return nil, fmt.Errorf("versions.yaml: %w", err)
	}
	return v, nil
}
