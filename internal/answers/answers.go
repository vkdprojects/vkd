// Package answers is the data every render sees: global answers plus per-blueprint answers.
package answers

import (
	"fmt"
	"hash/fnv"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Licenses vkd can write.
var Licenses = []string{"MIT", "Apache-2.0", "AGPL-3.0", "proprietary"}

// Answers is serialized to .vkd/answers.yaml and is the template data root.
type Answers struct {
	Name        string `yaml:"name" json:"name"`
	Owner       string `yaml:"owner" json:"owner"`
	Description string `yaml:"description" json:"description"`
	OpenSource  bool   `yaml:"open_source" json:"open_source"`
	License     string `yaml:"license" json:"license"`
	PortBase    int    `yaml:"port_base" json:"port_base"`
	// Year is the copyright year, recorded at create time. Never read from the clock at render time:
	// `vkd update` must re-render byte for byte.
	Year int `yaml:"year" json:"year"`
	// Components are the blueprint names the user picked (stacks and addons), without base and auto-added ones.
	Components []string `yaml:"components" json:"components"`
	// Per holds per-blueprint answers: Per["go-api"]["module"].
	Per map[string]map[string]string `yaml:"per,omitempty" json:"per,omitempty"`
}

var projectNameRE = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

var ownerRE = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?$`)

// Defaults fills what can be derived: License, PortBase, Description.
func (a *Answers) Defaults() {
	if a.License == "" {
		if a.OpenSource {
			a.License = "MIT"
		} else {
			a.License = "proprietary"
		}
	}
	if a.PortBase == 0 {
		a.PortBase = DerivePortBase(a.Name)
	}
	if a.Year == 0 {
		a.Year = time.Now().Year()
	}
	if a.Description == "" && a.Name != "" {
		a.Description = a.Name
	}
	if a.Per == nil {
		a.Per = map[string]map[string]string{}
	}
}

// Validate checks the global answers. It returns every problem, naming the flag that fixes it.
func (a *Answers) Validate() error {
	var errs []string
	if !projectNameRE.MatchString(a.Name) {
		errs = append(errs, fmt.Sprintf("name %q must be lowercase letters, digits and dashes, starting with a letter (argument NAME)", a.Name))
	}
	if !ownerRE.MatchString(a.Owner) {
		errs = append(errs, fmt.Sprintf("owner %q is not a valid GitHub owner (--owner)", a.Owner))
	}
	ok := false
	for _, l := range Licenses {
		if a.License == l {
			ok = true
		}
	}
	if !ok {
		errs = append(errs, fmt.Sprintf("license %q must be one of %s (--license)", a.License, strings.Join(Licenses, ", ")))
	}
	if a.OpenSource && a.License == "proprietary" {
		errs = append(errs, "--oss needs an open source license, got proprietary (--license)")
	}
	if a.PortBase < 1024 || a.PortBase > 65000 {
		errs = append(errs, fmt.Sprintf("port_base %d must be in 1024..65000 (--port-base)", a.PortBase))
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

// DerivePortBase picks a stable base in 3000..9000, step 10, from the project name.
func DerivePortBase(name string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	return 3000 + int(h.Sum32()%601)*10
}

// Get returns a per-blueprint answer.
func (a *Answers) Get(blueprint, id string) string { return a.Per[blueprint][id] }

// Set stores a per-blueprint answer.
func (a *Answers) Set(blueprint, id, value string) {
	if a.Per == nil {
		a.Per = map[string]map[string]string{}
	}
	if a.Per[blueprint] == nil {
		a.Per[blueprint] = map[string]string{}
	}
	a.Per[blueprint][id] = value
}

// ParseSet parses "blueprint.id=value" (the value may contain '=' and '.').
func ParseSet(s string) (blueprint, id, value string, err error) {
	key, value, ok := strings.Cut(s, "=")
	if !ok {
		return "", "", "", fmt.Errorf("--set %q must look like blueprint.id=value", s)
	}
	blueprint, id, ok = strings.Cut(key, ".")
	if !ok || blueprint == "" || id == "" {
		return "", "", "", fmt.Errorf("--set %q must look like blueprint.id=value", s)
	}
	return blueprint, id, value, nil
}

// UniqueComponents returns Components without duplicates, in the order first given.
func (a *Answers) UniqueComponents() []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range a.Components {
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

// Keys returns the per-blueprint answer keys, for deterministic output.
func (a *Answers) Keys() []string {
	var out []string
	for bp, m := range a.Per {
		for id := range m {
			out = append(out, bp+"."+id)
		}
	}
	sort.Strings(out)
	return out
}
