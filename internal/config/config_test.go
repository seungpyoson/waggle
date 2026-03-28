package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

// setDefaultField sets a Defaults struct field by name and registers a cleanup
// to restore the original value. This centralizes the save/set/restore pattern
// and makes it safe against panics mid-test. Tests that use this MUST NOT call
// t.Parallel() — Defaults is shared global state.
func setDefaultField(t *testing.T, fieldName string, val any) {
	t.Helper()
	v := reflect.ValueOf(&Defaults).Elem()
	fv := v.FieldByName(fieldName)
	if !fv.IsValid() {
		t.Fatalf("no field %q in Defaults", fieldName)
	}
	orig := reflect.New(fv.Type()).Elem()
	orig.Set(fv)
	t.Cleanup(func() { fv.Set(orig) })
	fv.Set(reflect.ValueOf(val))
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	orig, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(orig) })
}

func createGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"git", "init"}, {"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"}, {"git", "commit", "--allow-empty", "-m", "root"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s\n%s", args, err, out)
		}
	}
	return dir
}

func createGitRepoWithMergedHistory(t *testing.T) string {
	t.Helper()
	repo1, repo2 := createGitRepo(t), createGitRepo(t)
	for _, args := range [][]string{
		{"git", "remote", "add", "other", repo2}, {"git", "fetch", "other"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = repo1
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s\n%s", args, err, out)
		}
	}
	cmd := exec.Command("git", "merge", "other/main", "--allow-unrelated-histories", "--no-edit")
	cmd.Dir = repo1
	if _, err := cmd.CombinedOutput(); err != nil {
		cmd2 := exec.Command("git", "merge", "other/master", "--allow-unrelated-histories", "--no-edit")
		cmd2.Dir = repo1
		if out, err := cmd2.CombinedOutput(); err != nil {
			t.Fatalf("merge: %s\n%s", err, out)
		}
	}
	return repo1
}

// C8, C5 partial: no .git anywhere → error mentions WAGGLE_ROOT
func TestFindProjectRoot_NoGitDir(t *testing.T) {
	tmp := t.TempDir()
	child := filepath.Join(tmp, "a", "b")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := FindProjectRoot(child)
	if err == nil {
		t.Fatal("expected error when no .git exists")
	}
	if got := err.Error(); !regexp.MustCompile(`WAGGLE_ROOT`).MatchString(got) {
		t.Fatalf("error should mention WAGGLE_ROOT, got: %s", got)
	}
}

// Walk up from subdir to find .git in parent
func TestFindProjectRoot_FromSubdir(t *testing.T) {
	tmp := t.TempDir()
	// create .git dir at root
	if err := os.Mkdir(filepath.Join(tmp, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(tmp, "a", "b")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}

	root, err := FindProjectRoot(child)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Resolve tmp too for comparison (macOS /private/var symlinks)
	wantRoot, _ := filepath.EvalSymlinks(tmp)
	if root != wantRoot {
		t.Fatalf("got %q, want %q", root, wantRoot)
	}
}

// C5: WAGGLE_ROOT overrides .git detection completely
func TestFindProjectRoot_EnvOverride(t *testing.T) {
	tmp := t.TempDir()
	override := filepath.Join(tmp, "override")
	if err := os.MkdirAll(override, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("WAGGLE_ROOT", override)

	unrelated := filepath.Join(tmp, "unrelated")
	if err := os.MkdirAll(unrelated, 0o755); err != nil {
		t.Fatal(err)
	}

	root, err := FindProjectRoot(unrelated)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantRoot, _ := filepath.EvalSymlinks(override)
	if root != wantRoot {
		t.Fatalf("got %q, want %q", root, wantRoot)
	}
}

// WAGGLE_ROOT pointing to a file (not a directory) → error
func TestFindProjectRoot_EnvNotDirectory(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WAGGLE_ROOT", file)

	_, err := FindProjectRoot("/anywhere")
	if err == nil {
		t.Fatal("expected error when WAGGLE_ROOT is a file")
	}
}

// FindProjectRoot works with relative startDir
func TestFindProjectRoot_RelativePath(t *testing.T) {
	tmp := t.TempDir()
	if err := os.Mkdir(filepath.Join(tmp, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(tmp, "sub")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}

	// chdir to child, call with "."
	orig, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(orig) })
	os.Chdir(child)

	root, err := FindProjectRoot(".")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !filepath.IsAbs(root) {
		t.Fatalf("root should be absolute, got %q", root)
	}
}

// C4: Symlinked paths to same dir produce same root
func TestFindProjectRoot_SymlinkResolved(t *testing.T) {
	tmp := t.TempDir()
	real := filepath.Join(tmp, "real")
	if err := os.MkdirAll(filepath.Join(real, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tmp, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	rootFromReal, err := FindProjectRoot(real)
	if err != nil {
		t.Fatalf("real: %v", err)
	}
	rootFromLink, err := FindProjectRoot(link)
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	if rootFromReal != rootFromLink {
		t.Fatalf("symlink not resolved: real=%q link=%q", rootFromReal, rootFromLink)
	}
}

// C7: All Paths fields are non-empty and absolute (Socket empty when HOME missing)
func TestPaths_AllFieldsPopulated(t *testing.T) {
	p := NewPaths("test-project")
	for name, val := range map[string]string{
		"ProjectID": p.ProjectID, "DataDir": p.DataDir,
		"DB": p.DB, "PID": p.PID, "Lock": p.Lock, "Log": p.Log, "Socket": p.Socket,
	} {
		if val == "" {
			t.Errorf("%s is empty", name)
		}
	}
	for name, path := range map[string]string{
		"DataDir": p.DataDir, "DB": p.DB, "PID": p.PID,
		"Lock": p.Lock, "Log": p.Log, "Socket": p.Socket,
	} {
		if !filepath.IsAbs(path) {
			t.Errorf("%s not absolute: %q", name, path)
		}
	}
}

// All fields derived correctly from root
func TestPaths_DerivedFromProjectID(t *testing.T) {
	p := NewPaths("my-project")
	if p.ProjectID != "my-project" {
		t.Fatalf("ProjectID = %q", p.ProjectID)
	}
	if filepath.Base(p.DB) != Defaults.DBFile {
		t.Fatalf("DB filename = %q, want %q", filepath.Base(p.DB), Defaults.DBFile)
	}
	if filepath.Base(p.PID) != Defaults.PIDFile {
		t.Fatalf("PID filename = %q", filepath.Base(p.PID))
	}
}

// C2: Same root → same socket
func TestPaths_SocketHashDeterministic(t *testing.T) {
	a := NewPaths("project-x")
	b := NewPaths("project-x")
	if a.Socket != b.Socket {
		t.Fatalf("same ID produced different sockets: %q vs %q", a.Socket, b.Socket)
	}
}

// C3: Different roots → different sockets
func TestPaths_SocketHashDiffers(t *testing.T) {
	a := NewPaths("project-a")
	b := NewPaths("project-b")
	if a.Socket == b.Socket {
		t.Fatal("different IDs produced same socket")
	}
}

// C6: Socket hash is exactly 12 hex chars
func TestPaths_SocketHashLength(t *testing.T) {
	p := NewPaths("test-project")
	dir := filepath.Base(filepath.Dir(p.Socket))
	re := regexp.MustCompile(`^[0-9a-f]+$`)
	if !re.MatchString(dir) {
		t.Fatalf("socket parent dir %q is not hex", dir)
	}
	if len(dir) != 12 {
		t.Fatalf("hash length = %d, want 12, hash = %q", len(dir), dir)
	}
	if filepath.Base(p.Socket) != "broker.sock" {
		t.Fatalf("socket filename = %q, want %q", filepath.Base(p.Socket), "broker.sock")
	}
}

func TestNewPaths_IncludesRuntimePaths(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	p := NewPaths("test-project")
	for name, val := range map[string]string{
		"RuntimeDir":          p.RuntimeDir,
		"RuntimeDB":           p.RuntimeDB,
		"RuntimePID":          p.RuntimePID,
		"RuntimeLog":          p.RuntimeLog,
		"RuntimeState":        p.RuntimeState,
		"RuntimeStartLockDir": p.RuntimeStartLockDir,
	} {
		if val == "" {
			t.Fatalf("%s is empty", name)
		}
		if !filepath.IsAbs(val) {
			t.Fatalf("%s not absolute: %q", name, val)
		}
	}

	if got := filepath.Base(p.RuntimeDB); got != Defaults.RuntimeDBFile {
		t.Fatalf("RuntimeDB filename = %q, want %q", got, Defaults.RuntimeDBFile)
	}
	if got := filepath.Base(p.RuntimePID); got != Defaults.RuntimePIDFile {
		t.Fatalf("RuntimePID filename = %q, want %q", got, Defaults.RuntimePIDFile)
	}
	if got := filepath.Base(p.RuntimeLog); got != Defaults.RuntimeLogFile {
		t.Fatalf("RuntimeLog filename = %q, want %q", got, Defaults.RuntimeLogFile)
	}
	if got := filepath.Base(p.RuntimeState); got != Defaults.RuntimeStateFile {
		t.Fatalf("RuntimeState filename = %q, want %q", got, Defaults.RuntimeStateFile)
	}
	if got := filepath.Base(p.RuntimeStartLockDir); got != Defaults.RuntimeStartLockDirName {
		t.Fatalf("RuntimeStartLockDir filename = %q, want %q", got, Defaults.RuntimeStartLockDirName)
	}
	if got, want := filepath.Dir(p.RuntimeDB), p.RuntimeDir; got != want {
		t.Fatalf("RuntimeDB dir = %q, want %q", got, want)
	}
	if got, want := filepath.Dir(p.RuntimePID), p.RuntimeDir; got != want {
		t.Fatalf("RuntimePID dir = %q, want %q", got, want)
	}
	if got, want := filepath.Dir(p.RuntimeLog), p.RuntimeDir; got != want {
		t.Fatalf("RuntimeLog dir = %q, want %q", got, want)
	}
	if got, want := filepath.Dir(p.RuntimeState), p.RuntimeDir; got != want {
		t.Fatalf("RuntimeState dir = %q, want %q", got, want)
	}
	if got, want := filepath.Dir(p.RuntimeStartLockDir), p.RuntimeDir; got != want {
		t.Fatalf("RuntimeStartLockDir dir = %q, want %q", got, want)
	}
	if got := filepath.Base(p.RuntimeDir); got != Defaults.RuntimeDirName {
		t.Fatalf("RuntimeDir basename = %q, want %q", got, Defaults.RuntimeDirName)
	}
}

func TestNewPaths_RuntimePathsSharedAcrossProjects(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	a := NewPaths("project-a")
	b := NewPaths("project-b")

	if a.RuntimeDir != b.RuntimeDir {
		t.Fatalf("RuntimeDir should be machine-local: %q vs %q", a.RuntimeDir, b.RuntimeDir)
	}
	if a.RuntimeDB != b.RuntimeDB {
		t.Fatalf("RuntimeDB should be machine-local: %q vs %q", a.RuntimeDB, b.RuntimeDB)
	}
	if a.RuntimePID != b.RuntimePID {
		t.Fatalf("RuntimePID should be machine-local: %q vs %q", a.RuntimePID, b.RuntimePID)
	}
}

func TestResolveProjectID_UnchangedForRuntime(t *testing.T) {
	t.Setenv("WAGGLE_PROJECT_ID", "")
	t.Setenv("WAGGLE_ROOT", "")

	repo := createGitRepo(t)
	chdir(t, repo)

	id, err := ResolveProjectID()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	p := NewPaths(id)
	if p.ProjectID != id {
		t.Fatalf("ProjectID = %q, want %q", p.ProjectID, id)
	}

	if p.RuntimeDir == "" || p.RuntimeDB == "" || p.RuntimePID == "" || p.RuntimeLog == "" || p.RuntimeState == "" || p.RuntimeStartLockDir == "" {
		t.Fatalf("runtime paths should be populated for a resolved project: %#v", p)
	}

	id2, err := ResolveProjectID()
	if err != nil {
		t.Fatalf("unexpected error on second call: %v", err)
	}
	if id2 != id {
		t.Fatalf("ResolveProjectID changed after NewPaths: got %q, want %q", id2, id)
	}
}

// LeaseDuration is set to 5 minutes for task claims
func TestDefaults_LeaseDuration(t *testing.T) {
	if Defaults.LeaseDuration != 5*time.Minute {
		t.Fatalf("LeaseDuration = %v, want %v (5 minutes)", Defaults.LeaseDuration, 5*time.Minute)
	}
}

// MaxRetries is set to 3 for task retry attempts
func TestDefaults_MaxRetries(t *testing.T) {
	if Defaults.MaxRetries != 3 {
		t.Fatalf("MaxRetries = %d, want %d", Defaults.MaxRetries, 3)
	}
}

// New config fields for #35: all tunables centralized
func TestDefaults_BusyTimeout(t *testing.T) {
	if Defaults.BusyTimeout != 5*time.Second {
		t.Fatalf("BusyTimeout = %v, want 5s", Defaults.BusyTimeout)
	}
}

func TestDefaults_LeaseCheckPeriod(t *testing.T) {
	if Defaults.LeaseCheckPeriod != 30*time.Second {
		t.Fatalf("LeaseCheckPeriod = %v, want 30s", Defaults.LeaseCheckPeriod)
	}
}

func TestDefaults_IdleCheckInterval(t *testing.T) {
	if Defaults.IdleCheckInterval != 1*time.Second {
		t.Fatalf("IdleCheckInterval = %v, want 1s", Defaults.IdleCheckInterval)
	}
}

func TestDefaults_StartupPollInterval(t *testing.T) {
	if Defaults.StartupPollInterval != 100*time.Millisecond {
		t.Fatalf("StartupPollInterval = %v, want 100ms", Defaults.StartupPollInterval)
	}
}

func TestDefaults_StartupTimeout(t *testing.T) {
	if Defaults.StartupTimeout != 2*time.Second {
		t.Fatalf("StartupTimeout = %v, want 2s", Defaults.StartupTimeout)
	}
}

func TestDefaults_RuntimeStartLockStaleThreshold(t *testing.T) {
	if Defaults.RuntimeStartLockStaleThreshold != 10*time.Second {
		t.Fatalf("RuntimeStartLockStaleThreshold = %v, want 10s", Defaults.RuntimeStartLockStaleThreshold)
	}
}

func TestValidateDefaults_PassesWithDefaults(t *testing.T) {
	if err := ValidateDefaults(); err != nil {
		t.Fatalf("ValidateDefaults() failed on stock defaults: %v", err)
	}
}

func TestValidateDefaults_RejectsNegativeDurations(t *testing.T) {
	setDefaultField(t, "LeaseCheckPeriod", -1*time.Second)
	if err := ValidateDefaults(); err == nil {
		t.Fatal("ValidateDefaults() should reject negative LeaseCheckPeriod")
	}
}

func TestValidateDefaults_RejectsSubSecondLeaseDuration(t *testing.T) {
	setDefaultField(t, "LeaseDuration", 500*time.Millisecond)
	err := ValidateDefaults()
	if err == nil {
		t.Fatal("ValidateDefaults() should reject sub-second LeaseDuration")
	}
	if got := err.Error(); !strings.Contains(got, "LeaseDuration") || !strings.Contains(got, ">= 1s") {
		t.Fatalf("unexpected error: %s", got)
	}
}

// TestValidateDefaults_RejectsZeroOnEveryNumericField uses reflection to zero
// each int/int64/Duration field and verify ValidateDefaults catches it.
// This is the comprehensive test — individual per-field tests are unnecessary
// because this auto-discovers all fields. MUST NOT use t.Parallel().
func TestValidateDefaults_RejectsZeroOnEveryNumericField(t *testing.T) {
	v := reflect.ValueOf(&Defaults).Elem()
	typ := v.Type()
	for i := 0; i < v.NumField(); i++ {
		field := typ.Field(i)
		fv := v.Field(i)
		if fv.Kind() != reflect.Int && fv.Kind() != reflect.Int64 {
			continue
		}
		t.Run(field.Name, func(t *testing.T) {
			// NOT parallel — mutates shared Defaults
			orig := fv.Int()
			t.Cleanup(func() { fv.SetInt(orig) })
			fv.SetInt(0)

			err := ValidateDefaults()
			if err == nil {
				t.Fatalf("ValidateDefaults() should reject zero %s", field.Name)
			}
			if !strings.Contains(err.Error(), field.Name) {
				t.Fatalf("error should mention %s, got: %s", field.Name, err.Error())
			}
		})
	}
}

func TestValidateDefaults_DeterministicErrorOrder(t *testing.T) {
	setDefaultField(t, "ShutdownTimeout", time.Duration(0))
	for i := 0; i < 10; i++ {
		err := ValidateDefaults()
		if err == nil {
			t.Fatal("expected error")
		}
		if got := err.Error(); got != "config.Defaults.ShutdownTimeout must be positive, got 0s" {
			t.Fatalf("run %d: unexpected error: %s", i, got)
		}
	}
}

func TestResolveProjectID_EnvOverride(t *testing.T) {
	t.Setenv("WAGGLE_PROJECT_ID", "custom-id-123")
	id, err := ResolveProjectID()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "custom-id-123" {
		t.Fatalf("got %q, want %q", id, "custom-id-123")
	}
}

func TestResolveProjectID_GitRootCommit(t *testing.T) {
	t.Setenv("WAGGLE_PROJECT_ID", "")
	t.Setenv("WAGGLE_ROOT", "")
	repo := createGitRepo(t)
	chdir(t, repo)

	id, err := ResolveProjectID()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(id) != 40 {
		t.Fatalf("expected 40-char SHA, got %q (len=%d)", id, len(id))
	}
	matched, _ := regexp.MatchString(`^[0-9a-f]{40}$`, id)
	if !matched {
		t.Fatalf("expected hex SHA, got %q", id)
	}
}

func TestResolveProjectID_GitWorktree(t *testing.T) {
	t.Setenv("WAGGLE_PROJECT_ID", "")
	t.Setenv("WAGGLE_ROOT", "")
	repo := createGitRepo(t)

	wt := filepath.Join(t.TempDir(), "worktree")
	cmd := exec.Command("git", "worktree", "add", wt, "-b", "test-branch")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %s\n%s", err, out)
	}
	t.Cleanup(func() {
		exec.Command("git", "-C", repo, "worktree", "remove", wt).Run()
	})

	chdir(t, repo)
	idMain, err := ResolveProjectID()
	if err != nil {
		t.Fatalf("main repo: %v", err)
	}

	chdir(t, wt)
	idWT, err := ResolveProjectID()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}

	if idMain != idWT {
		t.Fatalf("worktree ID %q != main repo ID %q", idWT, idMain)
	}
}

func TestResolveProjectID_MultipleRootCommits(t *testing.T) {
	t.Setenv("WAGGLE_PROJECT_ID", "")
	t.Setenv("WAGGLE_ROOT", "")
	repo := createGitRepoWithMergedHistory(t)
	chdir(t, repo)

	id, err := ResolveProjectID()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(id) != 40 {
		t.Fatalf("expected 40-char SHA, got %q (len=%d)", id, len(id))
	}
	// Must be deterministic
	id2, _ := ResolveProjectID()
	if id != id2 {
		t.Fatalf("not deterministic: %q != %q", id, id2)
	}
}

func TestResolveProjectID_WaggleRootFallback(t *testing.T) {
	t.Setenv("WAGGLE_PROJECT_ID", "")
	t.Setenv("WAGGLE_ROOT", "/some/project/root")
	chdir(t, t.TempDir())

	id, err := ResolveProjectID()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "path:/some/project/root" {
		t.Fatalf("got %q, want %q", id, "path:/some/project/root")
	}
}

func TestResolveProjectID_Error(t *testing.T) {
	t.Setenv("WAGGLE_PROJECT_ID", "")
	t.Setenv("WAGGLE_ROOT", "")
	chdir(t, t.TempDir())

	_, err := ResolveProjectID()
	if err == nil {
		t.Fatal("expected error when no git and no env vars")
	}
	if !strings.Contains(err.Error(), "WAGGLE_PROJECT_ID") || !strings.Contains(err.Error(), "WAGGLE_ROOT") {
		t.Fatalf("error should mention both env vars, got: %s", err.Error())
	}
}

func TestResolveProjectID_EmptyRepo(t *testing.T) {
	t.Setenv("WAGGLE_PROJECT_ID", "")
	t.Setenv("WAGGLE_ROOT", "")
	tmp := t.TempDir()
	cmd := exec.Command("git", "init")
	cmd.Dir = tmp
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %s\n%s", err, out)
	}
	chdir(t, tmp)

	_, err := ResolveProjectID()
	if err == nil {
		t.Fatal("expected error for empty git repo with no env vars")
	}
}

func TestNewPaths_DataDirUnderHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	p := NewPaths("test-project-id")
	wantPrefix := filepath.Join(home, ".waggle", "data")
	if !strings.HasPrefix(p.DataDir, wantPrefix) {
		t.Fatalf("DataDir %q not under %q", p.DataDir, wantPrefix)
	}
	for name, path := range map[string]string{"DB": p.DB, "PID": p.PID, "Lock": p.Lock, "Log": p.Log} {
		if !strings.HasPrefix(path, p.DataDir) {
			t.Fatalf("%s %q not under DataDir %q", name, path, p.DataDir)
		}
	}
	wantSocketPrefix := filepath.Join(home, ".waggle", "sockets")
	if !strings.HasPrefix(p.Socket, wantSocketPrefix) {
		t.Fatalf("Socket %q not under %q", p.Socket, wantSocketPrefix)
	}
}

func TestNewPaths_SameIDSamePaths(t *testing.T) {
	a := NewPaths("same-id")
	b := NewPaths("same-id")
	if a.DataDir != b.DataDir || a.Socket != b.Socket || a.DB != b.DB {
		t.Fatal("same ID produced different paths")
	}
}

func TestNewPaths_DifferentIDDifferentPaths(t *testing.T) {
	a := NewPaths("project-alpha")
	b := NewPaths("project-beta")
	if a.DataDir == b.DataDir || a.Socket == b.Socket {
		t.Fatal("different IDs produced same paths")
	}
}

func TestNewPaths_ProjectIDStored(t *testing.T) {
	p := NewPaths("my-project-id")
	if p.ProjectID != "my-project-id" {
		t.Fatalf("ProjectID = %q, want %q", p.ProjectID, "my-project-id")
	}
}

func TestNewPaths_AllEmptyWithoutHome(t *testing.T) {
	t.Setenv("HOME", "")
	p := NewPaths("test-id")
	for name, val := range map[string]string{
		"DataDir": p.DataDir, "RuntimeDir": p.RuntimeDir,
		"RuntimeDB": p.RuntimeDB, "RuntimePID": p.RuntimePID, "RuntimeLog": p.RuntimeLog,
		"RuntimeState": p.RuntimeState, "RuntimeStartLockDir": p.RuntimeStartLockDir,
		"DB": p.DB, "PID": p.PID, "Lock": p.Lock, "Log": p.Log, "Socket": p.Socket,
	} {
		if val != "" {
			t.Errorf("expected empty %s without HOME, got %q", name, val)
		}
	}
	// ProjectID should still be set
	if p.ProjectID != "test-id" {
		t.Fatalf("ProjectID = %q, want %q", p.ProjectID, "test-id")
	}
}

func TestNewPaths_HashLength(t *testing.T) {
	p := NewPaths("test-id")
	if p.Socket == "" {
		t.Skip("no HOME set")
	}
	dir := filepath.Base(filepath.Dir(p.Socket))
	re := regexp.MustCompile(`^[0-9a-f]{12}$`)
	if !re.MatchString(dir) {
		t.Fatalf("hash = %q, want 12 hex chars", dir)
	}
}
