package purge

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Git answers the two questions purge asks a repository. Paths are relative
// to the repository root with forward slashes.
type Git interface {
	// Tracked reports which of rels contain at least one file in Git's
	// index (or are a submodule). An error means the answer is unknown.
	Tracked(ctx context.Context, repo string, rels []string) (map[string]bool, error)
	// Ignored reports which of rels Git ignores.
	Ignored(ctx context.Context, repo string, rels []string) (map[string]bool, error)
}

// ErrGitMissing means Git is not installed (or not on PATH).
var ErrGitMissing = errors.New("Git is not installed or not on PATH")

// ExecGit runs the git executable. It only reads: `ls-files` lists the index
// and `check-ignore` evaluates ignore rules. Repository configuration that
// could run programs (core.fsmonitor, hooks) is overridden, Git environment
// variables inherited from the caller are dropped, and pathspecs are literal.
type ExecGit struct {
	// Path is the git executable; empty means look it up on PATH.
	Path string
	// Timeout bounds each call (default 60 s).
	Timeout time.Duration
}

// cmdLineBudget keeps each command line well below the Windows limit of
// 32,767 characters.
const cmdLineBudget = 24000

func (g ExecGit) exe() (string, error) {
	if g.Path != "" {
		if _, err := os.Stat(g.Path); err != nil {
			return "", ErrGitMissing
		}
		return g.Path, nil
	}
	p, err := exec.LookPath("git")
	if err != nil {
		return "", ErrGitMissing
	}
	return p, nil
}

func (g ExecGit) run(ctx context.Context, repo string, args []string, okCodes ...int) ([]byte, error) {
	exe, err := g.exe()
	if err != nil {
		return nil, err
	}
	timeout := g.Timeout
	if timeout <= 0 {
		timeout = time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	full := append([]string{"-C", repo, "--no-pager",
		"-c", "core.fsmonitor=false", "-c", "core.hooksPath=NUL", "-c", "core.untrackedCache=false"}, args...)
	cmd := exec.CommandContext(ctx, exe, full...)
	cmd.Env = gitEnv()
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	err = cmd.Run()
	if err == nil {
		return out.Bytes(), nil
	}
	sub := "command"
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			sub = a
			break
		}
	}
	var ee *exec.ExitError
	switch {
	case ctx.Err() != nil:
		return nil, fmt.Errorf("git %s: %w", sub, ctx.Err())
	case errors.As(err, &ee):
		for _, c := range okCodes {
			if ee.ExitCode() == c {
				return out.Bytes(), nil
			}
		}
		msg := strings.TrimSpace(firstLine(stderr.String()))
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("git %s: %s", sub, msg)
	}
	return nil, fmt.Errorf("git %s: %w", sub, err)
}

// gitEnv is the caller's environment without GIT_* variables (GIT_DIR,
// GIT_WORK_TREE, GIT_INDEX_FILE ... would point git elsewhere), with
// optional locks and prompts disabled.
func gitEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(strings.ToUpper(kv), "GIT_") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
}

// Tracked runs `git ls-files --cached` with literal, case-insensitive
// pathspecs anchored at the repository root.
func (g ExecGit) Tracked(ctx context.Context, repo string, rels []string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, chunk := range chunks(rels) {
		args := []string{"ls-files", "-z", "--cached", "--"}
		for _, r := range chunk {
			args = append(args, ":(top,literal,icase)"+r)
		}
		data, err := g.run(ctx, repo, args)
		if err != nil {
			return nil, err
		}
		for _, p := range splitNUL(data) {
			if r := owner(chunk, p); r != "" {
				out[r] = true
			}
		}
	}
	return out, nil
}

// Ignored runs `git check-ignore` (exit code 1 means "none ignored").
func (g ExecGit) Ignored(ctx context.Context, repo string, rels []string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, chunk := range chunks(rels) {
		args := append([]string{"--literal-pathspecs", "check-ignore", "-z", "--"}, chunk...)
		data, err := g.run(ctx, repo, args, 1)
		if err != nil {
			return nil, err
		}
		for _, p := range splitNUL(data) {
			for _, r := range chunk {
				if strings.EqualFold(strings.TrimSuffix(p, "/"), r) {
					out[r] = true
				}
			}
		}
	}
	return out, nil
}

// owner returns the entry of rels that p (a path relative to the repository
// root) lies in, compared case-insensitively.
func owner(rels []string, p string) string {
	lp := strings.ToLower(p)
	for _, r := range rels {
		lr := strings.ToLower(r)
		if lp == lr || strings.HasPrefix(lp, lr+"/") {
			return r
		}
	}
	return ""
}

func chunks(rels []string) [][]string {
	var out [][]string
	var cur []string
	size := 0
	for _, r := range rels {
		if size+len(r)+32 > cmdLineBudget && len(cur) > 0 {
			out = append(out, cur)
			cur, size = nil, 0
		}
		cur = append(cur, r)
		size += len(r) + 32
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

func splitNUL(data []byte) []string {
	var out []string
	for _, p := range bytes.Split(data, []byte{0}) {
		if len(p) > 0 {
			out = append(out, string(p))
		}
	}
	return out
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// relSlash returns p relative to repo with forward slashes.
func relSlash(repo, p string) (string, error) {
	r, err := filepath.Rel(repo, p)
	if err != nil {
		return "", err
	}
	if r == "." || strings.HasPrefix(r, "..") {
		return "", fmt.Errorf("%s is not inside %s", p, repo)
	}
	return filepath.ToSlash(r), nil
}
