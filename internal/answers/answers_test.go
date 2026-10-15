package answers

import (
	"strings"
	"testing"
)

func TestDerivePortBase(t *testing.T) {
	seen := map[int]string{}
	for _, name := range []string{"demo", "atlas", "ac", "wi", "zelvo", "digital", "r2k", "koatla", "a", "my-long-project-name"} {
		p := DerivePortBase(name)
		if p < 3000 || p > 9000 || p%10 != 0 {
			t.Errorf("DerivePortBase(%q) = %d, want 3000..9000 in steps of 10", name, p)
		}
		if DerivePortBase(name) != p {
			t.Errorf("DerivePortBase(%q) is not stable", name)
		}
		if prev, dup := seen[p]; dup {
			t.Errorf("%q and %q share port base %d", name, prev, p)
		}
		seen[p] = name
	}
}

func TestParseSet(t *testing.T) {
	cases := []struct {
		in         string
		bp, id, v  string
		wantErrSub string
	}{
		{"go-api.module=github.com/a/b", "go-api", "module", "github.com/a/b", ""},
		{"x.y=a=b", "x", "y", "a=b", ""},
		{"x.y=", "x", "y", "", ""},
		{"novalue", "", "", "", "blueprint.id=value"},
		{"nodot=1", "", "", "", "blueprint.id=value"},
		{".id=1", "", "", "", "blueprint.id=value"},
		{"bp.=1", "", "", "", "blueprint.id=value"},
	}
	for _, c := range cases {
		bp, id, v, err := ParseSet(c.in)
		if c.wantErrSub != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErrSub) {
				t.Errorf("ParseSet(%q) err = %v, want %q", c.in, err, c.wantErrSub)
			}
			continue
		}
		if err != nil || bp != c.bp || id != c.id || v != c.v {
			t.Errorf("ParseSet(%q) = %q %q %q %v", c.in, bp, id, v, err)
		}
	}
}

func TestDefaultsAndValidate(t *testing.T) {
	a := &Answers{Name: "demo", Owner: "acme"}
	a.Defaults()
	if a.License != "proprietary" || a.PortBase != DerivePortBase("demo") || a.Year == 0 || a.Description != "demo" || a.Per == nil {
		t.Fatalf("closed source defaults wrong: %+v", a)
	}
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	b := &Answers{Name: "demo", Owner: "acme", OpenSource: true}
	b.Defaults()
	if b.License != "MIT" {
		t.Errorf("an open source project defaults to MIT, got %q", b.License)
	}
	keep := &Answers{Name: "demo", Owner: "acme", License: "AGPL-3.0", PortBase: 5000, Year: 2020, Description: "d"}
	keep.Defaults()
	if keep.License != "AGPL-3.0" || keep.PortBase != 5000 || keep.Year != 2020 || keep.Description != "d" {
		t.Errorf("Defaults must not overwrite given answers: %+v", keep)
	}

	// every problem is reported at once, each naming its flag.
	bad := &Answers{Name: "Bad Name", Owner: "-x", License: "WTFPL", PortBase: 80}
	err := bad.Validate()
	if err == nil {
		t.Fatal("want errors")
	}
	for _, want := range []string{"NAME", "--owner", "--license", "--port-base"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

func TestSetGetAndUniqueComponents(t *testing.T) {
	a := &Answers{Components: []string{"b", "a", "b", "c", "a"}}
	if a.Get("x", "y") != "" {
		t.Error("Get on an empty Answers must be empty")
	}
	a.Set("x", "y", "1")
	a.Set("x", "z", "2")
	a.Set("w", "y", "3")
	if a.Get("x", "y") != "1" || a.Get("w", "y") != "3" {
		t.Errorf("Get/Set: %v", a.Per)
	}
	if strings.Join(a.Keys(), ",") != "w.y,x.y,x.z" {
		t.Errorf("Keys = %v", a.Keys())
	}
	if strings.Join(a.UniqueComponents(), ",") != "b,a,c" {
		t.Errorf("components keep first-seen order without duplicates, got %v", a.UniqueComponents())
	}
}
