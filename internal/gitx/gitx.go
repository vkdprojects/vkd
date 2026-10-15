// Package gitx is the small slice of git vkd needs. It shells out to the git binary: no library, no cgo.
// Nothing here touches the user's index or worktree: pristine trees are built in a throwaway index file.
package gitx

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// BaseRef is the ref holding the pristine render of the last create/update.
const BaseRef = "refs/vkd/base"

// Repo is a git repository root.
type Repo struct{ Dir string }

// Open returns the repository containing dir, or an error when dir is not inside one.
func Open(dir string) (*Repo, error) {
	r := &Repo{Dir: dir}
	out, err := r.run(nil, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("%s is not inside a git repository (vkd update needs git; run `git init`)", dir)
	}
	return &Repo{Dir: strings.TrimSpace(string(out))}, nil
}

// Init runs `git init` in dir.
func Init(dir string) (*Repo, error) {
	r := &Repo{Dir: dir}
	if _, err := r.run(nil, nil, "init", "-q"); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Repo) run(stdin []byte, env []string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = r.Dir
	cmd.Env = append(os.Environ(), env...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.Bytes(), nil
}

// HasBase reports whether refs/vkd/base exists.
func (r *Repo) HasBase() bool {
	_, err := r.run(nil, nil, "rev-parse", "--verify", "-q", BaseRef)
	return err == nil
}

// ReadBase returns the content of path in the pristine tree, and whether it exists there.
func (r *Repo) ReadBase(path string) ([]byte, bool, error) {
	out, err := r.run(nil, nil, "cat-file", "blob", BaseRef+":"+path)
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") || strings.Contains(err.Error(), "exists on disk, but not in") ||
			strings.Contains(err.Error(), "Not a valid object name") {
			return nil, false, nil
		}
		return nil, false, err
	}
	return out, true, nil
}

// BasePaths lists every file in the pristine tree.
func (r *Repo) BasePaths() ([]string, error) {
	out, err := r.run(nil, nil, "ls-tree", "-r", "--name-only", "-z", BaseRef)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	return paths, nil
}

// Blob is one file of a pristine tree.
type Blob struct {
	Path string
	Data []byte
	Exec bool
}

// WriteBase records blobs as the new tip of refs/vkd/base. The commit's parent is the previous tip (none for
// the first). The tree is built in a temporary index, so the user's index and worktree are untouched.
func (r *Repo) WriteBase(blobs []Blob, message string) error {
	tmp, err := os.MkdirTemp("", "vkd-index-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	idx := []string{"GIT_INDEX_FILE=" + filepath.Join(tmp, "index")}

	var info bytes.Buffer
	for _, b := range blobs {
		out, err := r.run(b.Data, nil, "hash-object", "-w", "--stdin")
		if err != nil {
			return err
		}
		mode := "100644"
		if b.Exec {
			mode = "100755"
		}
		fmt.Fprintf(&info, "%s %s\t%s\x00", mode, strings.TrimSpace(string(out)), b.Path)
	}
	if info.Len() > 0 {
		if _, err := r.run(info.Bytes(), idx, "update-index", "--add", "-z", "--index-info"); err != nil {
			return err
		}
	}
	treeOut, err := r.run(nil, idx, "write-tree")
	if err != nil {
		return err
	}
	tree := strings.TrimSpace(string(treeOut))

	args := []string{"commit-tree", tree, "-m", message}
	if r.HasBase() {
		parent, err := r.run(nil, nil, "rev-parse", BaseRef)
		if err != nil {
			return err
		}
		args = append(args, "-p", strings.TrimSpace(string(parent)))
	}
	// A fixed identity: refs/vkd/base is vkd's bookkeeping and must not depend on (or fail without) user.name.
	ident := []string{"GIT_AUTHOR_NAME=vkd", "GIT_AUTHOR_EMAIL=vkd@localhost", "GIT_COMMITTER_NAME=vkd", "GIT_COMMITTER_EMAIL=vkd@localhost"}
	commit, err := r.run(nil, ident, args...)
	if err != nil {
		return err
	}
	_, err = r.run(nil, nil, "update-ref", BaseRef, strings.TrimSpace(string(commit)))
	return err
}

// MergeFile three-way merges ours/base/theirs with `git merge-file -p`. conflicts is true when markers were
// written. Labels name the three sides in the markers.
func MergeFile(ours, base, theirs []byte, path string) (merged []byte, conflicts bool, err error) {
	dir, err := os.MkdirTemp("", "vkd-merge-")
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	files := map[string][]byte{"ours": ours, "base": base, "theirs": theirs}
	for n, d := range files {
		if err := os.WriteFile(filepath.Join(dir, n), d, 0o600); err != nil {
			return nil, false, err
		}
	}
	cmd := exec.Command("git", "merge-file", "-p",
		"-L", path+" (yours)", "-L", path+" (vkd previous)", "-L", path+" (vkd new)",
		filepath.Join(dir, "ours"), filepath.Join(dir, "base"), filepath.Join(dir, "theirs"))
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err = cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return out.Bytes(), false, nil
	case errors.As(err, &ee) && ee.ExitCode() > 0 && ee.ExitCode() < 128:
		// exit code = number of conflicts; output still holds the merge with markers.
		return out.Bytes(), true, nil
	default:
		return nil, false, fmt.Errorf("git merge-file: %w: %s", err, strings.TrimSpace(errb.String()))
	}
}
