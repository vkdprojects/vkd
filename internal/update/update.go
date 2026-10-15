// Package update applies a new pristine render onto a project the user has edited: copier-style, git-native.
//
// For each path three versions meet: base (the previous pristine render, from refs/vkd/base), ours (the
// worktree) and theirs (the new pristine render). The decision is a pure function of those three (Decide).
package update

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/vkdprojects/vkd/internal/gitx"
	"github.com/vkdprojects/vkd/internal/plan"
)

// Action is what happens to one path.
type Action string

const (
	Create   Action = "create"   // new in the template, not on disk
	Update   Action = "update"   // user did not touch it; template changed: overwritten
	Merge    Action = "merge"    // both changed, merged cleanly
	Conflict Action = "conflict" // both changed, markers written
	Skip     Action = "skip"     // nothing to do (identical, or only the user changed it)
	Delete   Action = "delete"   // template dropped it and the user did not touch it
)

// Version is one side of the three-way comparison. Exists false means the path is absent there.
type Version struct {
	Data   []byte
	Exists bool
}

// Decide is the 3-way decision table.
//
//	base  ours  theirs   result
//	 -     -     T       create
//	 -     O     T       O==T skip; else both added it: merge against an empty base (conflict if they differ)
//	 B     -     T       user deleted it: skip (a deleted file stays deleted)
//	 B     O     -       O==B delete (template dropped it); else skip (user edited it, keep)
//	 B     O     T       O==B: B==T skip, else update.  O==T skip.  B==T skip (only the user changed it).
//	                     all three differ: merge
func Decide(base, ours, theirs Version) Action {
	switch {
	case !base.Exists && !ours.Exists && theirs.Exists:
		return Create
	case !base.Exists && ours.Exists && theirs.Exists:
		if bytes.Equal(ours.Data, theirs.Data) {
			return Skip
		}
		return Merge
	case base.Exists && !ours.Exists:
		return Skip
	case base.Exists && ours.Exists && !theirs.Exists:
		if bytes.Equal(ours.Data, base.Data) {
			return Delete
		}
		return Skip
	case base.Exists && ours.Exists && theirs.Exists:
		switch {
		case bytes.Equal(ours.Data, theirs.Data), bytes.Equal(base.Data, theirs.Data):
			return Skip
		case bytes.Equal(ours.Data, base.Data):
			return Update
		default:
			return Merge
		}
	}
	return Skip
}

// Result is the outcome for one path.
type Result struct {
	Path   string `json:"path"`
	Action Action `json:"action"`
}

// Apply compares the new plan with the worktree under root, writes the result unless dryRun, and returns one
// Result per affected path (Skip results are included so the output explains itself). It does not move
// refs/vkd/base: the caller records the new pristine render once the worktree is written.
func Apply(repo *gitx.Repo, root string, p *plan.Plan, dryRun bool) ([]Result, error) {
	if !repo.HasBase() {
		return nil, fmt.Errorf("no %s in this repository: this directory was not created by vkd (or the ref was deleted); run `vkd create` first", gitx.BaseRef)
	}
	theirs := map[string]plan.File{}
	for _, f := range p.Files {
		theirs[f.Path] = f
	}
	basePaths, err := repo.BasePaths()
	if err != nil {
		return nil, err
	}
	all := map[string]bool{}
	for path := range theirs {
		all[path] = true
	}
	for _, path := range basePaths {
		all[path] = true
	}
	paths := make([]string, 0, len(all))
	for path := range all {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	var results []Result
	for _, path := range paths {
		baseData, baseOK, err := repo.ReadBase(path)
		if err != nil {
			return nil, err
		}
		abs := filepath.Join(root, filepath.FromSlash(path))
		oursData, err := os.ReadFile(abs)
		oursOK := err == nil
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		t, tOK := theirs[path]

		base := Version{baseData, baseOK}
		ours := Version{oursData, oursOK}
		th := Version{t.Content, tOK}
		act := Decide(base, ours, th)
		// An earlier update left markers that nobody resolved: report it as a conflict again, but write nothing.
		stale := act == Skip && oursOK && hasConflictMarkers(oursData)

		var out []byte
		switch act {
		case Create, Update:
			out = t.Content
		case Merge:
			merged, conflicts, err := gitx.MergeFile(oursData, baseData, t.Content, path)
			if err != nil {
				return nil, err
			}
			out = merged
			if conflicts {
				act = Conflict
			}
		}
		if stale {
			results = append(results, Result{Path: path, Action: Conflict})
			continue
		}
		if !dryRun {
			switch act {
			case Create, Update, Merge, Conflict:
				mode := t.Mode
				if mode == 0 {
					mode = 0o644
				}
				if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
					return nil, err
				}
				if err := os.WriteFile(abs, out, mode); err != nil {
					return nil, err
				}
				if err := os.Chmod(abs, mode); err != nil {
					return nil, err
				}
			case Delete:
				if err := os.Remove(abs); err != nil {
					return nil, err
				}
			}
		}
		results = append(results, Result{Path: path, Action: act})
	}
	return results, nil
}

// Blobs converts a plan to the blobs recorded in refs/vkd/base.
func Blobs(p *plan.Plan) []gitx.Blob {
	out := make([]gitx.Blob, len(p.Files))
	for i, f := range p.Files {
		out[i] = gitx.Blob{Path: f.Path, Data: f.Content, Exec: f.Mode&0o111 != 0}
	}
	return out
}

// hasConflictMarkers reports a full conflict block (start, separator and end at the beginning of lines).
func hasConflictMarkers(data []byte) bool {
	start := bytes.HasPrefix(data, []byte("<<<<<<< ")) || bytes.Contains(data, []byte("\n<<<<<<< "))
	return start && bytes.Contains(data, []byte("\n=======\n")) && bytes.Contains(data, []byte("\n>>>>>>> "))
}
