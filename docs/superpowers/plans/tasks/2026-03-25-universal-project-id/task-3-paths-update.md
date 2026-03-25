# Task 3: TDD — NewPaths + Paths Struct Update

**Files:**
- Modify: `internal/config/config_test.go`
- Modify: `internal/config/config.go`

- [ ] **Step 1: Write failing tests for new NewPaths behavior**

Add to `internal/config/config_test.go`:

```go
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
		"DataDir": p.DataDir, "DB": p.DB, "PID": p.PID,
		"Lock": p.Lock, "Log": p.Log, "Socket": p.Socket,
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
```

- [ ] **Step 2: Run new tests — verify they fail**

Run: `cd ~/Projects/Claude/waggle && go test ./internal/config/ -v -run "TestNewPaths_(DataDir|SameID|DifferentID|ProjectID|AllEmpty|HashLength)" -count=1`

Expected: FAIL — `Paths` has no `ProjectID` or `DataDir` fields.

- [ ] **Step 3: Update Paths struct and NewPaths in config.go**

Replace `Paths` struct (lines 101-110) and `NewPaths` (lines 112-144):

```go
type Paths struct {
	ProjectID string
	DataDir   string
	DB        string
	PID       string
	Lock      string
	Log       string
	Socket    string
}

// NewPaths computes all derived paths from a project ID. All state paths live
// under ~/.waggle/data/<hash>/ and ~/.waggle/sockets/<hash>/. If os.UserHomeDir
// fails (no HOME set), all paths will be empty — callers must check before use.
func NewPaths(projectID string) Paths {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{ProjectID: projectID}
	}

	f := fnv.New64a()
	_, _ = f.Write([]byte(projectID))
	hash := fmt.Sprintf("%012x", f.Sum64()&0xffffffffffff)

	dataDir := filepath.Join(home, Defaults.DirName, "data", hash)
	socketDir := filepath.Join(home, Defaults.DirName, "sockets", hash)

	return Paths{
		ProjectID: projectID,
		DataDir:   dataDir,
		DB:        filepath.Join(dataDir, Defaults.DBFile),
		PID:       filepath.Join(dataDir, Defaults.PIDFile),
		Lock:      filepath.Join(dataDir, Defaults.LockFile),
		Log:       filepath.Join(dataDir, Defaults.LogFile),
		Socket:    filepath.Join(socketDir, "broker.sock"),
	}
}
```

- [ ] **Step 4: Update existing tests that reference removed fields**

Remove or rewrite tests that use `Root`, `WaggleDir`, `Config`:

- Delete `TestPaths_PathNormalization` (path normalization irrelevant for ID input)
- Rewrite `TestPaths_AllFieldsAbsolute` → `TestPaths_AllFieldsPopulated`:
  ```go
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
  ```
- Rewrite `TestPaths_SocketEmptyWithoutHome` to also check DataDir (covered by `TestNewPaths_AllEmptyWithoutHome` — delete old one)
- Rewrite `TestPaths_DerivedFromRoot` → `TestPaths_DerivedFromProjectID`:
  ```go
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
  ```
- Rewrite `TestPaths_SocketHashDeterministic` — change args from paths to project IDs:
  ```go
  func TestPaths_SocketHashDeterministic(t *testing.T) {
  	a := NewPaths("project-x")
  	b := NewPaths("project-x")
  	if a.Socket != b.Socket {
  		t.Fatalf("same ID produced different sockets: %q vs %q", a.Socket, b.Socket)
  	}
  }
  ```
- Rewrite `TestPaths_SocketHashDiffers` — change args from paths to project IDs:
  ```go
  func TestPaths_SocketHashDiffers(t *testing.T) {
  	a := NewPaths("project-a")
  	b := NewPaths("project-b")
  	if a.Socket == b.Socket {
  		t.Fatal("different IDs produced same socket")
  	}
  }
  ```
- Rewrite `TestPaths_SocketHashLength` — change arg from path to project ID:
  ```go
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
  ```

- [ ] **Step 5: Run ALL config tests**

Run: `cd ~/Projects/Claude/waggle && go test ./internal/config/ -v -count=1`

Expected: All tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
python3 ~/.claude/lib/safe_git.py commit -m "feat: update Paths struct and NewPaths to use project ID (#37)

Removed Root, WaggleDir, Config fields. Added ProjectID, DataDir.
All state now under ~/.waggle/data/<hash>/."
```
