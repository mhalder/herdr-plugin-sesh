package namer

import (
	"context"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

type Runner interface {
	Run(context.Context, string, ...string) (string, error)
}
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, bin string, args ...string) (string, error) {
	//nolint:gosec // command is the fixed git lookup used to name local sessions.
	b, err := exec.CommandContext(ctx, bin, args...).Output()
	return strings.TrimSpace(string(b)), err
}

type Namer struct{ Runner Runner }

func (n Namer) Name(ctx context.Context, path string, dirLength int) string {
	if dirLength < 1 {
		dirLength = 1
	}
	r := n.Runner
	if r == nil {
		r = ExecRunner{}
	}
	if root, err := r.Run(ctx, "git", "-C", path, "rev-parse", "--show-toplevel"); err == nil && root != "" {
		if remote, err := r.Run(ctx, "git", "-C", path, "config", "--get", "remote.origin.url"); err == nil && remote != "" {
			return repoName(remote)
		}
		return filepath.Base(root)
	}
	return LastComponents(path, dirLength)
}
func repoName(remote string) string {
	remote = strings.TrimSuffix(remote, ".git")
	if strings.Contains(remote, ":") && !strings.Contains(remote, "://") {
		parts := strings.Split(remote, ":")
		remote = parts[len(parts)-1]
	}
	remote = strings.TrimRight(remote, "/")
	parts := strings.Split(remote, "/")
	name := parts[len(parts)-1]
	re := regexp.MustCompile(`[^A-Za-z0-9._/-]+`)
	return re.ReplaceAllString(name, "-")
}

// LastComponents returns the final n path components of path joined by the OS
// separator, falling back to the whole cleaned path when it has fewer than n
// components. It is shared by the direct-path namer and the zoxide source so
// both name a directory the same way.
func LastComponents(path string, n int) string {
	p := filepath.Clean(path)
	parts := strings.Split(p, string(filepath.Separator))
	out := []string{}
	for _, part := range parts {
		if part != "" {
			out = append(out, part)
		}
	}
	if len(out) == 0 {
		return filepath.Base(p)
	}
	if n < 1 {
		n = 1
	}
	if n > len(out) {
		n = len(out)
	}
	return strings.Join(out[len(out)-n:], "/")
}
