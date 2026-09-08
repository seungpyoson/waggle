package brokerstate

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/seungpyoson/waggle/internal/config"
)

// ConversionConfig locates the one canonical store, the snapshots taken before
// it is changed, and the retired broker's endpoints a conversion leaves behind.
// Every value comes from resolved project paths.
type ConversionConfig struct {
	Database    string
	SnapshotDir string
	// LegacyPID and LegacySocket are the retired broker's endpoints, which a
	// committed conversion removes. They are never served, opened or dialled.
	LegacyPID          string
	LegacySocket       string
	TransactionTimeout time.Duration
}

func NewConversionConfig(paths config.Paths) ConversionConfig {
	return ConversionConfig{
		Database:           paths.DB,
		SnapshotDir:        paths.SnapshotDir,
		LegacyPID:          paths.LegacyPID,
		LegacySocket:       paths.LegacySocket,
		TransactionTimeout: config.Defaults.BusyTimeout,
	}
}

func (c ConversionConfig) Validate() error {
	if !filepath.IsAbs(c.Database) || !filepath.IsAbs(c.SnapshotDir) {
		return fmt.Errorf("conversion requires absolute database and snapshot paths")
	}
	if !filepath.IsAbs(c.LegacyPID) || !filepath.IsAbs(c.LegacySocket) {
		return fmt.Errorf("conversion requires absolute legacy endpoint paths")
	}
	// Retirement removes files, so what may be removed is decided here, once,
	// against the only place the retired broker's names are defined. A native
	// endpoint carries a different name, so it can never be named here, and
	// these two names must stay distinct from the native ones for that to hold.
	if config.Defaults.LegacyPIDFile == config.Defaults.PIDFile ||
		config.Defaults.LegacySocketFile == config.Defaults.SocketFile {
		return fmt.Errorf("the retired broker's endpoint names must differ from the native ones")
	}
	if filepath.Base(c.LegacyPID) != config.Defaults.LegacyPIDFile ||
		filepath.Base(c.LegacySocket) != config.Defaults.LegacySocketFile {
		return fmt.Errorf("conversion retires only %s and %s, not %s and %s",
			config.Defaults.LegacyPIDFile, config.Defaults.LegacySocketFile,
			filepath.Base(c.LegacyPID), filepath.Base(c.LegacySocket))
	}
	if c.TransactionTimeout <= 0 {
		return fmt.Errorf("conversion deadline must be positive")
	}
	return nil
}

// Handle is one observed holder: a process with the canonical store open, or a
// running old Waggle executable. It never carries an environment or arguments.
type Handle struct {
	PID     int
	Command string
	Path    string
}

// WriterCensus is the OS-level proof that the offline state actually holds.
// Both methods must fail loudly rather than report an empty partial listing.
// Errors are returned as the tools gave them; Convert and Rollback are what
// classify an unanswered census as ErrCensusUnavailable.
type WriterCensus interface {
	// OpenHandles lists processes holding any of paths (or their -wal/-shm
	// siblings) open, the caller included: which holders matter is the caller's
	// judgement, not the census's.
	OpenHandles(ctx context.Context, paths []string) ([]Handle, error)
	// WaggleProcesses lists running processes whose executable basename is the
	// Waggle binary, excluding self.
	WaggleProcesses(ctx context.Context) ([]Handle, error)
	// Scope describes, for an operator, how far these two answers actually
	// reach. A census that cannot see everything is still worth taking, but what
	// it could not see is part of its answer and is recorded with the result.
	Scope() string
}

// DomainUpgrade checks the source read-only before snapshotting, then applies
// the upgrade inside the conversion transaction. Check runs again inside that
// transaction so a change since preflight cannot evade the domain's rules.
// Apply returns any legacy tables it preserved under retired names.
type DomainUpgrade struct {
	Check func(Reader) error
	Apply func(*WriteTx) ([]string, error)
}

// Report is what an operator sees after a conversion or a rollback. A rollback
// restores the snapshot as VACUUM INTO wrote it, so the store it leaves behind
// is in SQLite's default journal mode, which the retired broker set to WAL on
// its next open.
type Report struct {
	Database    string
	Snapshot    string
	FromVersion int
	ToVersion   int
	// Tasks counts the rows carried across. The task table is the one domain
	// detail this package names, because preserving those rows is the stated
	// purpose of the conversion and of the rollback that undoes it.
	Tasks int
	// PreservedLegacyTables names retired tables the conversion kept under a new
	// name. They are never read by the native runtime.
	PreservedLegacyTables []string
	// RetiredEndpoints names the retired broker's socket and PID file that this
	// conversion removed. A rollback restores the store, not these: they are a
	// dead process's leftovers, and the retired broker writes its own on start.
	RetiredEndpoints []string
	// CensusScope is how far the census that cleared this change could see, in
	// the census's own words. It is reported rather than assumed, because an
	// operator deciding whether the proof was enough needs to know what it
	// covered, and no census here claims to cover everything.
	CensusScope string
	ConvertedAt time.Time
}

// Provenance records where a prepared store came from, in the conversion
// transaction itself. It is the only record a rollback trusts.
type Provenance struct {
	SourceSchema int
	ConvertedAt  string
	Snapshot     string
}

// Inspection is the read-only operator view of one project's storage.
type Inspection struct {
	Database string
	Exists   bool
	// SchemaVersion is the highest version row found, and VersionRows how many
	// there are. Conversion requires exactly one, so an operator can see a
	// duplicate-row store here instead of meeting the refusal later.
	SchemaVersion       int
	VersionRows         int
	Cutover             string
	Provenance          *Provenance
	LegacyPIDPresent    bool
	LegacySocketPresent bool
	Snapshots           []string
}

// Convert performs the offline upgrade of a schema-v1 store to native storage.
// The domain schema is not this package's to know: upgrade receives the same
// scoped write capability every other domain transaction gets, inside the one
// transaction that moves the version record.
//
// The order is the whole safety argument: a full census proves nothing is
// holding the store, a consistent snapshot is taken, the census is repeated
// immediately before any change, and every change then happens in one
// transaction that also moves the version record. An interruption at any point
// leaves the source untouched and the snapshot present. The converted store is
// prepared, never active: activation stays a separate, ownership-guarded
// decision.
func Convert(ctx context.Context, cfg ConversionConfig, census WriterCensus, inspector ProcessInspector, upgrade DomainUpgrade) (_ Report, err error) {
	if err := cfg.Validate(); err != nil {
		return Report{}, err
	}
	if census == nil || inspector == nil || upgrade.Check == nil || upgrade.Apply == nil {
		return Report{}, fmt.Errorf("conversion requires a writer census, process identity and a domain upgrade")
	}
	if err := requireRegularFile(cfg.Database); err != nil {
		return Report{}, err
	}
	self, err := inspector.Current(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("identify converting process: %w", err)
	}
	if err := self.validate(); err != nil {
		return Report{}, err
	}
	if err := survey(ctx, census, cfg.Database); err != nil {
		return Report{}, err
	}
	// The source is examined read-only first, so a store this conversion must
	// refuse is never opened for writing and never gains a snapshot.
	if err := preflightLegacyStore(ctx, cfg, upgrade.Check); err != nil {
		return Report{}, err
	}

	db, err := openCanonical(cfg.Database, "rw", cfg.TransactionTimeout)
	if err != nil {
		return Report{}, err
	}
	defer func() { err = errors.Join(err, db.Close()) }()

	snapshot, err := takeSnapshot(ctx, cfg, db)
	if err != nil {
		return Report{}, err
	}
	// Recheck immediately before conversion: the census above is only as fresh
	// as the moment it ran, and the snapshot took time.
	if err := survey(ctx, census, cfg.Database); err != nil {
		return Report{}, err
	}

	converted := time.Now().UTC()
	report := Report{
		Database:    cfg.Database,
		Snapshot:    snapshot,
		FromVersion: config.LegacySchemaVersion,
		ToVersion:   config.NativeSchemaVersion,
		CensusScope: census.Scope(),
		ConvertedAt: converted,
	}
	deadline, cancel := context.WithTimeout(ctx, cfg.TransactionTimeout)
	defer cancel()
	if err := reserved(deadline, db, writeAccess, func(conn *sql.Conn) error {
		if err := legacyVersion(deadline, conn); err != nil {
			return err
		}
		// The upgrade runs first, on a capability that expires with this
		// callback, so the catalogue it inspects is the legacy store's alone.
		// It carries no ownership fence: exclusive access here was proven by the
		// census, since no incarnation owns a legacy store.
		tx := &WriteTx{state: &transactionState{conn: conn, ctx: deadline, active: true}}
		defer func() { tx.state.mu.Lock(); tx.state.active = false; tx.state.mu.Unlock() }()
		if err := upgrade.Check(tx); err != nil {
			return fmt.Errorf("check domain schema: %w", err)
		}
		preserved, err := upgrade.Apply(tx)
		if err != nil {
			return fmt.Errorf("upgrade domain schema: %w", err)
		}
		report.PreservedLegacyTables = preserved
		if _, err := conn.ExecContext(deadline, ownershipTables); err != nil {
			return err
		}
		if err := claimOwnership(deadline, conn, rand.Text(), self,
			sql.NullString{String: converted.Format(time.RFC3339Nano), Valid: true}); err != nil {
			return err
		}
		if err := affectsOneRow(conn.ExecContext(deadline, `UPDATE cutover SET source_schema = ?, converted_at = ?, snapshot = ?
			WHERE singleton = 1 AND state = 'prepared'`, config.LegacySchemaVersion, converted.Format(time.RFC3339Nano), snapshot)); err != nil {
			return fmt.Errorf("record conversion provenance: %w", err)
		}
		if err := affectsOneRow(conn.ExecContext(deadline, "UPDATE schema_version SET version = ? WHERE version = ?",
			config.NativeSchemaVersion, config.LegacySchemaVersion)); err != nil {
			return fmt.Errorf("update schema version: %w", err)
		}
		return conn.QueryRowContext(deadline, "SELECT count(*) FROM tasks").Scan(&report.Tasks)
	}); err != nil {
		return Report{}, fmt.Errorf("convert canonical store: %w", err)
	}
	// The journal mode is part of what makes the store native, and it can only
	// be set outside a transaction. SQLite reports a refused change by returning
	// the unchanged mode rather than an error, so the result is read back: a
	// converted store that keeps the old journal is one Acquire would reject.
	var journal string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode="+config.CanonicalJournalMode).Scan(&journal); err != nil {
		return Report{}, fmt.Errorf("initialize canonical journal (store is converted; roll it back): %w", err)
	}
	if journal != config.CanonicalJournalMode {
		return Report{}, fmt.Errorf("canonical journal stayed %s (store is converted; roll it back)", journal)
	}
	// Last, and only once the store is wholly converted: the retired broker's
	// endpoints outlived it, and nothing may find them again. A conversion that
	// stopped anywhere above leaves them where they are.
	retired, err := retireLegacyEndpoints(cfg)
	if err != nil {
		return Report{}, fmt.Errorf("%w (store is converted; roll it back to undo the conversion)", err)
	}
	report.RetiredEndpoints = retired
	return report, nil
}

// retireLegacyEndpoints removes the retired broker's socket and PID file, in
// that fixed order, and reports what it removed. It runs only after a
// conversion has committed: the census proved no process holds the store, the
// store now refuses the retired broker, and the names it may remove were fixed
// by ConversionConfig.Validate, which accepts only the retired broker's own
// names. The native broker's endpoints are never named here.
func retireLegacyEndpoints(cfg ConversionConfig) ([]string, error) {
	var retired []string
	for _, endpoint := range []string{cfg.LegacyPID, cfg.LegacySocket} {
		info, err := os.Lstat(endpoint)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return retired, fmt.Errorf("inspect legacy endpoint: %w", err)
		}
		// A PID file and a socket are all the retired broker left here. Anything
		// else under these names was put there by something this package does not
		// know, and is not this package's to remove.
		if !info.Mode().IsRegular() && info.Mode().Type() != os.ModeSocket {
			return retired, fmt.Errorf("legacy endpoint %s is neither a file nor a socket (%s)", endpoint, info.Mode())
		}
		if err := os.Remove(endpoint); err != nil && !errors.Is(err, os.ErrNotExist) {
			return retired, fmt.Errorf("retire legacy endpoint: %w", err)
		}
		retired = append(retired, endpoint)
	}
	return retired, nil
}

// Rollback restores the snapshot a prepared store records as its origin. It
// refuses an active store, refuses a census that is not clean, and never
// guesses which snapshot belongs to this store.
func Rollback(ctx context.Context, cfg ConversionConfig, census WriterCensus) (_ Report, err error) {
	if err := cfg.Validate(); err != nil {
		return Report{}, err
	}
	if census == nil {
		return Report{}, fmt.Errorf("rollback requires a writer census")
	}
	if err := requireRegularFile(cfg.Database); err != nil {
		return Report{}, err
	}
	if err := survey(ctx, census, cfg.Database); err != nil {
		return Report{}, err
	}
	origin, err := preparedProvenance(ctx, cfg)
	if err != nil {
		return Report{}, err
	}
	if err := requireRegularFile(origin.Snapshot); err != nil {
		return Report{}, fmt.Errorf("recorded snapshot: %w", err)
	}
	tasks, err := verifySnapshot(ctx, cfg, origin.Snapshot, origin.SourceSchema)
	if err != nil {
		return Report{}, fmt.Errorf("verify recorded snapshot: %w", err)
	}
	// The census proved nothing holds this store, so the sidecars belong to no
	// live reader and would otherwise be applied to the restored file.
	for _, sidecar := range sidecars(cfg.Database) {
		if err := os.Remove(sidecar); err != nil && !errors.Is(err, os.ErrNotExist) {
			return Report{}, fmt.Errorf("remove journal sidecar: %w", err)
		}
	}
	if err := replaceFile(origin.Snapshot, cfg.Database); err != nil {
		return Report{}, err
	}
	report := Report{
		Database:    cfg.Database,
		Snapshot:    origin.Snapshot,
		FromVersion: config.NativeSchemaVersion,
		ToVersion:   origin.SourceSchema,
		Tasks:       tasks,
		CensusScope: census.Scope(),
		ConvertedAt: time.Now().UTC(),
	}
	return report, nil
}

// Inspect reports what an operator needs before deciding, and changes nothing.
// It opens the database read-only, never creates it, and leaves no journal
// sidecar behind that it did not find (see readOnly).
func Inspect(ctx context.Context, cfg ConversionConfig, paths config.Paths) (Inspection, error) {
	if err := cfg.Validate(); err != nil {
		return Inspection{}, err
	}
	view := Inspection{Database: cfg.Database}
	view.LegacyPIDPresent = exists(paths.LegacyPID)
	view.LegacySocketPresent = exists(paths.LegacySocket)
	snapshots, err := listSnapshots(cfg)
	if err != nil {
		return Inspection{}, err
	}
	view.Snapshots = snapshots
	info, err := os.Lstat(cfg.Database)
	if errors.Is(err, os.ErrNotExist) {
		return view, nil
	}
	if err != nil {
		return Inspection{}, fmt.Errorf("inspect canonical store: %w", err)
	}
	if !info.Mode().IsRegular() {
		return Inspection{}, fmt.Errorf("canonical store must be a regular file, not a symlink")
	}
	view.Exists = true
	if err := readOnly(ctx, cfg, func(ctx context.Context, db *sql.DB) error {
		// Which records a store carries is itself part of the answer: a legacy
		// store has no cutover row, and a store this young has no version row.
		// Asking the catalogue first keeps a missing record distinct from an
		// unreadable database, which stays an error.
		present, err := tablesPresent(ctx, db, "schema_version", "cutover")
		if err != nil {
			return err
		}
		if present["schema_version"] {
			if err := db.QueryRowContext(ctx, "SELECT count(*), coalesce(max(version), 0) FROM schema_version").
				Scan(&view.VersionRows, &view.SchemaVersion); err != nil {
				return fmt.Errorf("read schema version: %w", err)
			}
		}
		if !present["cutover"] {
			return nil
		}
		var state string
		var source sql.NullInt64
		var at, snapshot sql.NullString
		if err := db.QueryRowContext(ctx, "SELECT state, source_schema, converted_at, snapshot FROM cutover WHERE singleton = 1").
			Scan(&state, &source, &at, &snapshot); err != nil {
			return fmt.Errorf("read cutover state: %w", err)
		}
		view.Cutover = state
		if source.Valid || at.Valid || snapshot.Valid {
			view.Provenance = &Provenance{SourceSchema: int(source.Int64), ConvertedAt: at.String, Snapshot: snapshot.String}
		}
		return nil
	}); err != nil {
		return Inspection{}, err
	}
	return view, nil
}

// tablesPresent reports which of the named tables the store actually has.
func tablesPresent(ctx context.Context, db *sql.DB, names ...string) (_ map[string]bool, err error) {
	rows, err := db.QueryContext(ctx, "SELECT name FROM sqlite_schema WHERE type = 'table'")
	if err != nil {
		return nil, fmt.Errorf("read store catalogue: %w", err)
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	found := make(map[string]bool, len(names))
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		for _, want := range names {
			if name == want {
				found[name] = true
			}
		}
	}
	return found, rows.Err()
}

// survey is one full census: running old executables and open handles on the
// canonical store. Any uncertainty blocks, and an empty partial listing is not
// proof, so an error is never downgraded to "nothing found".
//
// The census reports every holder of the store, including this process, which
// holds it open from the moment it takes the snapshot until the conversion is
// done. The writers a conversion has to wait for are the other ones, so its own
// handles are set aside here rather than in the census, which stays a plain
// report of what the operating system sees.
func survey(ctx context.Context, census WriterCensus, database string) error {
	processes, err := census.WaggleProcesses(ctx)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrCensusUnavailable, err)
	}
	if len(processes) > 0 {
		return fmt.Errorf("%w: running %s", ErrWritersPresent, describe(processes))
	}
	handles, err := census.OpenHandles(ctx, []string{database})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrCensusUnavailable, err)
	}
	if foreign := heldElsewhere(handles, os.Getpid()); len(foreign) > 0 {
		return fmt.Errorf("%w: open handles held by %s", ErrWritersPresent, describe(foreign))
	}
	return nil
}

// heldElsewhere drops the handles this process holds itself.
func heldElsewhere(handles []Handle, self int) []Handle {
	foreign := make([]Handle, 0, len(handles))
	for _, h := range handles {
		if h.PID != self {
			foreign = append(foreign, h)
		}
	}
	return foreign
}

func describe(handles []Handle) string {
	out := ""
	for i, h := range handles {
		if i > 0 {
			out += ", "
		}
		out += fmt.Sprintf("pid=%d command=%s path=%s", h.PID, h.Command, h.Path)
	}
	return out
}

// preflightLegacyStore checks the version and domain through one read-only
// snapshot, before creating an undo copy or opening the source for writing.
func preflightLegacyStore(ctx context.Context, cfg ConversionConfig, check func(Reader) error) error {
	return readOnly(ctx, cfg, func(ctx context.Context, db *sql.DB) error {
		return reserved(ctx, db, readAccess, func(conn *sql.Conn) error {
			if err := legacyVersion(ctx, conn); err != nil {
				return err
			}
			tx := &ReadTx{state: &transactionState{conn: conn, ctx: ctx, active: true}}
			defer func() { tx.state.mu.Lock(); tx.state.active = false; tx.state.mu.Unlock() }()
			if err := check(tx); err != nil {
				return fmt.Errorf("check legacy domain before snapshot: %w", err)
			}
			return nil
		})
	})
}

// scanner is the single-row read shared by a plain connection and a reserved
// transaction connection, so the version rule has one implementation.
type scanner interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// legacyVersion accepts exactly one version row holding LegacySchemaVersion.
// Missing, duplicate or unexpected rows are refusals, never repairs.
func legacyVersion(ctx context.Context, from scanner) error {
	var count, low, high int
	if err := from.QueryRowContext(ctx, "SELECT count(*), coalesce(min(version), 0), coalesce(max(version), 0) FROM schema_version").
		Scan(&count, &low, &high); err != nil {
		return fmt.Errorf("%w: read schema version: %w", ErrNotLegacy, err)
	}
	if count > 1 {
		return fmt.Errorf("%w: %d duplicate schema version rows (%d..%d)", ErrNotLegacy, count, low, high)
	}
	if count != 1 || low != config.LegacySchemaVersion || high != config.LegacySchemaVersion {
		return fmt.Errorf("%w: found %d version rows reporting %d", ErrNotLegacy, count, high)
	}
	return nil
}

// preparedProvenance reads the origin a prepared store recorded for itself.
func preparedProvenance(ctx context.Context, cfg ConversionConfig) (Provenance, error) {
	var origin Provenance
	err := readOnly(ctx, cfg, func(ctx context.Context, db *sql.DB) error {
		var state string
		var source sql.NullInt64
		var at, snapshot sql.NullString
		if err := db.QueryRowContext(ctx, "SELECT state, source_schema, converted_at, snapshot FROM cutover WHERE singleton = 1").
			Scan(&state, &source, &at, &snapshot); err != nil {
			return fmt.Errorf("%w: %w", ErrNotPrepared, err)
		}
		if state != "prepared" {
			return fmt.Errorf("%w: cutover state is %q", ErrNotPrepared, state)
		}
		if !source.Valid || !snapshot.Valid {
			return ErrNoProvenance
		}
		origin = Provenance{SourceSchema: int(source.Int64), ConvertedAt: at.String, Snapshot: snapshot.String}
		return nil
	})
	return origin, err
}

// takeSnapshot writes a consistent copy, including committed WAL content, of
// the store as it stands before any change.
func takeSnapshot(ctx context.Context, cfg ConversionConfig, db *sql.DB) (string, error) {
	if err := os.MkdirAll(cfg.SnapshotDir, 0700); err != nil {
		return "", fmt.Errorf("create snapshot directory: %w", err)
	}
	snapshot := filepath.Join(cfg.SnapshotDir, fmt.Sprintf("%s.v%d-%s",
		filepath.Base(cfg.Database), config.LegacySchemaVersion, time.Now().UTC().Format(time.RFC3339Nano)))
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", snapshot); err != nil {
		return "", fmt.Errorf("snapshot canonical store: %w", err)
	}
	// VACUUM INTO does not sync its output. Persist the copy and its directory
	// entry before the conversion can commit provenance pointing to it.
	for _, path := range []string{snapshot, cfg.SnapshotDir, filepath.Dir(cfg.SnapshotDir)} {
		if err := syncPath(path); err != nil {
			return "", fmt.Errorf("persist snapshot: %w", err)
		}
	}
	if _, err := verifySnapshot(ctx, cfg, snapshot, config.LegacySchemaVersion); err != nil {
		return "", fmt.Errorf("verify new snapshot: %w", err)
	}
	return snapshot, nil
}

// verifySnapshot reads the undo copy before any destructive restore step. A
// readable version alone is insufficient: SQLite must also validate the file
// and read the tasks this snapshot promises to preserve.
func verifySnapshot(ctx context.Context, cfg ConversionConfig, snapshot string, version int) (tasks int, err error) {
	cfg.Database = snapshot
	err = readOnly(ctx, cfg, func(ctx context.Context, db *sql.DB) error {
		var check string
		if err := db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&check); err != nil {
			return fmt.Errorf("quick_check: %w", err)
		}
		if check != "ok" {
			return fmt.Errorf("quick_check: %s", check)
		}
		var count, low, high int
		if err := db.QueryRowContext(ctx, "SELECT count(*), coalesce(min(version), 0), coalesce(max(version), 0) FROM schema_version").
			Scan(&count, &low, &high); err != nil {
			return fmt.Errorf("read snapshot schema: %w", err)
		}
		if count != 1 || low != version || high != version {
			return fmt.Errorf("snapshot reports %d schema rows (%d..%d), expected one at %d", count, low, high, version)
		}
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM tasks").Scan(&tasks); err != nil {
			return fmt.Errorf("read snapshot tasks: %w", err)
		}
		if tasks < 0 {
			return fmt.Errorf("snapshot reports negative task count: %d", tasks)
		}
		return nil
	})
	return tasks, err
}

func syncPath(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s for sync: %w", path, err)
	}
	if err := errors.Join(file.Sync(), file.Close()); err != nil {
		return fmt.Errorf("sync %s: %w", path, err)
	}
	return nil
}

// listSnapshots reports the snapshots belonging to this database, newest first
// by the timestamp in the name. A missing directory is an empty listing.
func listSnapshots(cfg ConversionConfig) ([]string, error) {
	entries, err := os.ReadDir(cfg.SnapshotDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list snapshots: %w", err)
	}
	prefix := fmt.Sprintf("%s.v%d-", filepath.Base(cfg.Database), config.LegacySchemaVersion)
	type snapshot struct {
		path string
		at   time.Time
	}
	var found []snapshot
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, strings.TrimPrefix(entry.Name(), prefix))
		if err != nil {
			continue // Not a snapshot this package wrote.
		}
		found = append(found, snapshot{path: filepath.Join(cfg.SnapshotDir, entry.Name()), at: at})
	}
	sort.Slice(found, func(i, j int) bool { return found[i].at.After(found[j].at) })
	paths := make([]string, 0, len(found))
	for _, s := range found {
		paths = append(paths, s.path)
	}
	return paths, nil
}

// readOnly runs one read against the canonical store through a connection that
// cannot create or change it, and leaves the directory as it found it.
//
// Reading a WAL store materializes SQLite's -wal and -shm sidecars, and a
// read-only connection cannot remove them again on close. So when this pass is
// what created them, it hands the cleanup back to SQLite: a connection that can
// write removes both when it closes, and only when it is the last one, which is
// exactly the decision that keeps a broker that started meanwhile untouched.
// Nothing is executed through that connection and nothing is removed by hand.
func readOnly(ctx context.Context, cfg ConversionConfig, view func(context.Context, *sql.DB) error) (err error) {
	untouched := noSidecars(cfg.Database)
	db, err := openCanonical(cfg.Database, "ro", cfg.TransactionTimeout)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, db.Close())
		if untouched && !noSidecars(cfg.Database) {
			// A cleanup that cannot run leaves only SQLite's own sidecars, and
			// the read it follows already succeeded, so its failure is not the
			// caller's answer.
			_ = releaseSidecars(cfg)
		}
	}()
	deadline, cancel := context.WithTimeout(ctx, cfg.TransactionTimeout)
	defer cancel()
	return view(deadline, db)
}

// noSidecars reports whether the store currently has neither journal sidecar.
func noSidecars(database string) bool {
	for _, sidecar := range sidecars(database) {
		if exists(sidecar) {
			return false
		}
	}
	return true
}

func releaseSidecars(cfg ConversionConfig) error {
	db, err := openCanonical(cfg.Database, "rw", cfg.TransactionTimeout)
	if err != nil {
		return err
	}
	return errors.Join(db.Ping(), db.Close())
}

// replaceFile installs source at target through a temporary file in the target
// directory, so a partial copy can never be left in place of the store. The
// restored store keeps the permissions of the one it replaces.
func replaceFile(source, target string) (err error) {
	mode := os.FileMode(0600)
	if info, statErr := os.Lstat(target); statErr == nil {
		mode = info.Mode().Perm()
	}
	in, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open snapshot: %w", err)
	}
	defer func() { err = errors.Join(err, in.Close()) }()
	staged := target + ".restoring"
	// A restore interrupted between staging and rename leaves this file behind.
	// The census proved nothing holds this store, so the leftover is this
	// package's to clear; keeping it would block every later rollback.
	if err := os.Remove(staged); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("clear interrupted restore: %w", err)
	}
	out, err := os.OpenFile(staged, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("stage restored store: %w", err)
	}
	defer func() {
		if err != nil {
			if cleanupErr := os.Remove(staged); !errors.Is(cleanupErr, os.ErrNotExist) {
				err = errors.Join(err, cleanupErr)
			}
		}
	}()
	if _, err = io.Copy(out, in); err != nil {
		return errors.Join(fmt.Errorf("copy snapshot: %w", err), out.Close())
	}
	if err = errors.Join(out.Sync(), out.Close()); err != nil {
		return fmt.Errorf("flush restored store: %w", err)
	}
	if err = os.Rename(staged, target); err != nil {
		return fmt.Errorf("install restored store: %w", err)
	}
	return syncPath(filepath.Dir(target))
}

func sidecars(database string) []string {
	return []string{database + "-wal", database + "-shm"}
}

func requireRegularFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect canonical store: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("canonical store must be a regular file, not a symlink: %s", path)
	}
	return nil
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func affectsOneRow(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return fmt.Errorf("statement changed %d rows, expected exactly 1", changed)
	}
	return nil
}
