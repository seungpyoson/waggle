package spawn

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestFormatGate guards seungpyoson/claude-config#774 D3 and D5.
//
// The .no-mistakes.yaml `format:` command was previously
//
//	test -z "$(gofmt -l . | grep -v ^\.worktrees/)"
//
// which masked gofmt's non-zero exit on parse errors (the pipe replaces
// gofmt's exit code with grep's, and `$(...)` swallows stderr). Broken Go
// files passed the gate (D3). A subsequent fix that captured gofmt's exit
// code explicitly exposed D5: parse errors confined to .worktrees/ also
// failed the gate, because parse errors go to stderr with rc=2 regardless
// of which file produced them, and post-hoc stdout filtering on gofmt -l's
// file list cannot reach them. The current command filters at the file-
// enumeration level (find -not -path) so .worktrees/ is pruned before gofmt
// runs.
//
// This test reads the live `format:` command from .no-mistakes.yaml so it
// guards the *current* gate, not a frozen historical snapshot. The four
// sub-tests cover the cartesian product of {parse error, unformatted} ×
// {main tree, .worktrees/}.
func TestFormatGate(t *testing.T) {
	cmd := readFormatCommand(t)

	const brokenGo = "package main\nfunc broken( {\n"
	const okGo = "package main\n"
	// goimports/gofmt-correct would be: "package main\n\nfunc f() {}\n"
	// An unformatted file: missing the blank line + extra spaces.
	const unformattedGo = "package main\nfunc  f() {}\n"

	cases := []struct {
		name     string
		setup    func(dir string) error
		wantExit int
		why      string
	}{
		{
			name: "parse error in main tree fails the gate (D3)",
			setup: func(dir string) error {
				return writeFiles(dir, map[string]string{
					"ok.go":     okGo,
					"broken.go": brokenGo,
				})
			},
			wantExit: 1,
			why:      "main-tree parse errors must fail; this is the D3 fix",
		},
		{
			name: "parse error confined to .worktrees/ does NOT fail the gate (D5)",
			setup: func(dir string) error {
				return writeFiles(dir, map[string]string{
					"ok.go":                                okGo,
					".worktrees/branch/internal/broken.go": brokenGo,
				})
			},
			wantExit: 0,
			why:      ".worktrees/ parse errors must be excluded; this is the D5 fix",
		},
		{
			name: "unformatted file in main tree fails the gate",
			setup: func(dir string) error {
				return writeFiles(dir, map[string]string{
					"ok.go":          okGo,
					"unformatted.go": unformattedGo,
				})
			},
			wantExit: 1,
			why:      "main-tree unformatted files must fail (the gate's primary purpose)",
		},
		{
			name: "unformatted file confined to .worktrees/ does NOT fail the gate",
			setup: func(dir string) error {
				return writeFiles(dir, map[string]string{
					"ok.go": okGo,
					".worktrees/branch/internal/unformatted.go": unformattedGo,
				})
			},
			wantExit: 0,
			why:      ".worktrees/ unformatted files must be excluded (the original .worktrees/ exclusion intent)",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := tc.setup(dir); err != nil {
				t.Fatalf("setup: %v", err)
			}
			exitCode := runShell(t, dir, cmd)
			if exitCode != tc.wantExit {
				t.Fatalf("format gate exit = %d, want %d (%s)\nCommand: %s",
					exitCode, tc.wantExit, tc.why, cmd)
			}
		})
	}
}

// readFormatCommand finds .no-mistakes.yaml by walking up from the test's
// working directory and extracts the `format:` command literal.
func readFormatCommand(t *testing.T) string {
	t.Helper()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	dir := cwd
	for {
		candidate := filepath.Join(dir, ".no-mistakes.yaml")
		if _, err := os.Stat(candidate); err == nil {
			return extractFormatCommand(t, candidate)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf(".no-mistakes.yaml not found walking up from %s", cwd)
		}
		dir = parent
	}
}

// extractFormatCommand pulls the value of `format:` out of .no-mistakes.yaml
// without pulling in a YAML dependency. The YAML uses single-quoted strings
// for `format:` because the command itself contains double quotes.
func extractFormatCommand(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	// Match a line of the form:
	//   format: 'literal'
	// or
	//   format: "literal"
	re := regexp.MustCompile(`(?m)^\s*format:\s*'([^']*)'\s*$|^\s*format:\s*"([^"]*)"\s*$`)
	m := re.FindStringSubmatch(string(data))
	if m == nil {
		t.Fatalf("could not find `format:` command in %s", path)
	}
	for _, g := range m[1:] {
		if g != "" {
			return g
		}
	}
	t.Fatalf("matched `format:` line but captured empty literal in %s", path)
	return ""
}

// runShell executes the command via /bin/sh in dir and returns its exit code.
func runShell(t *testing.T, dir, command string) int {
	t.Helper()

	c := exec.Command("/bin/sh", "-c", command)
	c.Dir = dir
	output, err := c.CombinedOutput()
	if err != nil {
		var ee *exec.ExitError
		if asExitError(err, &ee) {
			t.Logf("shell stderr/stdout: %s", strings.TrimSpace(string(output)))
			return ee.ExitCode()
		}
		t.Fatalf("shell run error: %v (output: %s)", err, output)
	}
	return 0
}

// asExitError unwraps to *exec.ExitError without pulling in errors.As's
// generic machinery (keeps the test minimal).
func asExitError(err error, out **exec.ExitError) bool {
	if ee, ok := err.(*exec.ExitError); ok {
		*out = ee
		return true
	}
	return false
}

// writeFiles writes a map of relative-path → contents under root.
func writeFiles(root string, files map[string]string) error {
	for rel, contents := range files {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, []byte(contents), 0o644); err != nil {
			return err
		}
	}
	return nil
}
