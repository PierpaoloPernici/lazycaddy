package backup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
)

// layout is the timestamp embedded in every backup name,
// e.g. "2026-08-01T20-10-00". The dashes (not colons) keep the name
// valid as a filename on every platform.
const layout = "2006-01-02T15-04-05"

// sourceSuffix is the suffix of the identity sidecar written next to
// every backup. The sidecar is a plain-text file holding the exact,
// canonical source path this backup belongs to, so a backup can be
// resolved to exactly one source file even when two imported documents
// share a basename. It is deliberately plain text (not JSON) and its
// content is the path only — never the source bytes.
const sourceSuffix = ".src"

// Options configures a Creator. Dir is required; the other fields are
// optional.
type Options struct {
	// Dir is the directory backups are written to. It is created on
	// first use when missing. Required.
	Dir string
	// Clock supplies timestamps for backup names. It defaults to
	// time.Now.
	Clock func() time.Time
}

// Creator creates atomic, collision-safe backups of a source file.
// The app layer depends on this interface so save workflows can be
// tested with a fake. Create is safe for concurrent use: the sequence
// scan, name selection and file writes are serialized, so concurrent
// Creates never collide on a backup name.
type Creator interface {
	// Create backs up the file at srcPath and returns the path of the
	// new backup. The backup name embeds a timestamp and a
	// collision-safe sequence, e.g. "2026-08-01T20-10-00-001-Caddyfile".
	Create(srcPath string) (string, error)
}

// creator is the concrete Creator returned by New.
type creator struct {
	opts Options
	mu   sync.Mutex
}

// New returns a Creator with the given options. An empty Dir is an
// error.
func New(opts Options) (Creator, error) {
	if opts.Dir == "" {
		return nil, errors.New("backup: Dir is required")
	}
	return &creator{opts: opts}, nil
}

// Create implements Creator.
func (c *creator) Create(srcPath string) (string, error) {
	// Serialize the sequence scan + name selection + file creation so two
	// concurrent Creates can never pick the same sequence and overwrite
	// each other's backup.
	c.mu.Lock()
	defer c.mu.Unlock()

	data, err := os.ReadFile(srcPath)
	if err != nil {
		return "", fmt.Errorf("backup: read: %w", err)
	}
	if err := os.MkdirAll(c.opts.Dir, 0o755); err != nil {
		return "", fmt.Errorf("backup: dir: %w", err)
	}
	now := time.Now()
	if c.opts.Clock != nil {
		now = c.opts.Clock()
	}
	seq, err := c.nextSequence(now)
	if err != nil {
		return "", err
	}
	name := nameFor(now, seq, srcPath)
	path := filepath.Join(c.opts.Dir, name)
	if err := caddyfile.WriteAtomic(path, data); err != nil {
		return "", fmt.Errorf("backup: write: %w", err)
	}
	// Identity sidecar: the exact canonical source path, so the backup
	// can later be resolved to exactly one source file even when another
	// document shares its basename. The sidecar is written atomically
	// (same-directory temp file + rename) like the backup itself, so a
	// crash can never leave a backup without its identity; and with the
	// backup's own permission bits. A sidecar failure must abort the
	// whole Create and remove the already-written backup — an orphaned,
	// un-attributable copy is worse than no copy.
	if err := writeSidecar(path, srcPath); err != nil {
		_ = os.Remove(path)
		_ = os.Remove(path + sourceSuffix)
		return "", fmt.Errorf("backup: identity: %w", err)
	}
	return path, nil
}

// writeSidecar atomically writes the identity sidecar for the backup at
// path: the exact canonical source path on one line, written to a
// same-directory temporary file, fsynced and renamed over the final
// sidecar path. The sidecar inherits the backup file's permission bits
// so it never leaks readable content to a group or world that cannot
// read the backup itself. The temporary file is removed on any failure.
func writeSidecar(path, srcPath string) error {
	mode := os.FileMode(0o600) // fallback when the backup cannot be stat'd
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	data := []byte(filepath.Clean(srcPath) + "\n")
	f, err := os.CreateTemp(filepath.Dir(path), ".lazycaddy-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path+sourceSuffix); err != nil {
		return err
	}
	return nil
}

// nextSequence returns the sequence for a backup at time t: it scans the
// backup directory for names sharing the exact timestamp and returns one
// past the highest sequence found, so a second Create in the same second
// becomes -002-. Scan errors are reported because an unknown directory
// state is preferable to silently overwriting an existing backup.
func (c *creator) nextSequence(t time.Time) (int, error) {
	prefix := t.Format(layout) + "-"
	des, err := os.ReadDir(c.opts.Dir)
	if err != nil {
		return 0, fmt.Errorf("backup: scan: %w", err)
	}
	max := 0
	for _, de := range des {
		name := de.Name()
		if strings.HasSuffix(name, sourceSuffix) || !strings.HasPrefix(name, prefix) || len(name) < 25 {
			continue
		}
		// The prefix guarantees the timestamp and its dash are at the
		// start; the sequence is the zero-padded 3-digit field at 20:23
		// and must be followed by the basename dash.
		if name[23] != '-' {
			continue
		}
		seq, err := strconv.Atoi(name[20:23])
		if err != nil {
			continue
		}
		if seq > max {
			max = seq
		}
	}
	return max + 1, nil
}

// nameFor builds a backup filename for srcPath at time t with the given
// sequence, e.g. "2026-08-01T20-10-00-001-Caddyfile".
func nameFor(t time.Time, seq int, srcPath string) string {
	return fmt.Sprintf("%s-%03d-%s", t.Format(layout), seq, filepath.Base(srcPath))
}

// Entry describes one backup on disk, rebuilt from its filename and its
// identity sidecar.
type Entry struct {
	Path     string
	Time     time.Time
	Sequence int
	// Base is the source file's basename parsed from the backup
	// filename, e.g. "Caddyfile". It is the fallback identity used by
	// BelongsTo for legacy backups without a sidecar.
	Base string
	// Source is the exact canonical path of the source file this backup
	// belongs to, recovered from the backup's identity sidecar. It is
	// empty when the sidecar is missing — a legacy backup created before
	// identity sidecars existed. A legacy backup can only be resolved to
	// a source file through its basename, which is ambiguous when two
	// imported documents share a basename; callers must never restore a
	// legacy backup to a same-basename document.
	Source      string
	SourceKnown bool
}

// BelongsTo reports whether the entry belongs to the exact source file
// srcPath. Entries with a known identity match the canonical path
// exactly; legacy entries fall back to a basename match, which callers
// must treat as ambiguous when another document shares the basename.
func (e Entry) BelongsTo(srcPath string) bool {
	if e.SourceKnown {
		return filepath.Clean(e.Source) == filepath.Clean(srcPath)
	}
	return e.Base == filepath.Base(srcPath)
}

// List rebuilds the backup index from the files in dir, sorted newest
// first (Time desc, then Sequence desc). Identity sidecars are not
// entries; for every backup the sidecar (when present) is read to
// recover the exact source path. Files that do not match the backup
// name format are ignored. A missing dir yields no entries and no
// error.
func List(dir string) ([]Entry, error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		// A directory that has never been created is not an error: it is
		// simply an empty index.
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("backup: list: %w", err)
	}
	out := make([]Entry, 0, len(des))
	for _, de := range des {
		if de.IsDir() {
			continue
		}
		name := de.Name()
		if strings.HasSuffix(name, sourceSuffix) {
			// An identity sidecar is part of its backup entry, not an
			// entry of its own.
			continue
		}
		e, ok := parseName(name)
		if !ok {
			continue
		}
		e.Path = filepath.Join(dir, de.Name())
		e.Base = name[24:]
		e.Source, e.SourceKnown = readSourceSidecar(e.Path)
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Time.Equal(out[j].Time) {
			return out[i].Sequence > out[j].Sequence
		}
		return out[i].Time.After(out[j].Time)
	})
	return out, nil
}

// readSourceSidecar recovers the exact source path of a backup from its
// identity sidecar. A missing, unreadable or empty sidecar reports an
// unknown source (SourceKnown=false), which keeps legacy backups
// browsable while never letting them be restored to the wrong file.
func readSourceSidecar(path string) (string, bool) {
	data, err := os.ReadFile(path + sourceSuffix)
	if err != nil {
		return "", false
	}
	src := strings.TrimSpace(string(data))
	if src == "" {
		return "", false
	}
	return filepath.Clean(src), true
}

// parseName rebuilds an Entry from a backup filename of the form
// <timestamp>-<seq>-<basename>: the first 19 runes are the timestamp,
// rune 19 is a dash, runes 20-22 are the zero-padded sequence, rune 23
// is a dash and the rest is the basename, which may itself contain
// dashes. It returns ok=false for any name that does not match exactly,
// so stray files in the backup directory are silently ignored. The
// prefix fields are pure ASCII, so rune- and byte-indexing agree there;
// invalid names fail parsing rather than panic because time.Parse and
// Atoi reject them.
func parseName(name string) (Entry, bool) {
	if len(name) < 25 { // 19 + 1 + 3 + 1 + at least one basename rune
		return Entry{}, false
	}
	if name[19] != '-' || name[23] != '-' {
		return Entry{}, false
	}
	seq, err := strconv.Atoi(name[20:23])
	if err != nil || seq < 0 {
		return Entry{}, false
	}
	t, err := time.Parse(layout, name[:19])
	if err != nil {
		return Entry{}, false
	}
	return Entry{Time: t, Sequence: seq}, true
}

// Retention is a cleanup policy applied after a successful save or
// rollback. Keep is the maximum number of backups retained per source
// file: after cleanup each source keeps its newest Keep backups, always
// including the newest backup of the source and any protected path (the
// backup created for the current operation). A non-positive Keep
// disables cleanup entirely. Dir is the backup directory the policy
// scans and prunes.
type Retention struct {
	// Dir is the backup directory to prune. Required when Enabled.
	Dir string
	// Keep is the maximum number of backups per source file. Zero or
	// negative means "disabled": Apply is a no-op.
	Keep int
}

// Enabled reports whether the policy should run: a positive Keep means
// retention is configured.
func (r Retention) Enabled() bool { return r.Keep > 0 }

// Apply removes the oldest backups of every source until each source has
// at most Keep backups. The newest backup of each source is always kept,
// and every path in protected (the backup created for the current
// operation, which is normally also the newest) is never removed.
// Identity-less legacy backups, stray files and identity sidecars that
// do not belong to a removed backup are never touched, because their
// source cannot be proven. Apply is deterministic: groups follow the
// index order and removal walks each group from the least old to the
// oldest entry. It returns the removed backup paths and an error
// aggregating any removal failure; the returned removals are complete
// even when the error is non-nil, so callers can report what failed
// without losing what succeeded.
func (r Retention) Apply(protected ...string) ([]string, error) {
	if !r.Enabled() {
		return nil, nil
	}
	entries, err := List(r.Dir)
	if err != nil {
		return nil, fmt.Errorf("retention: list: %w", err)
	}
	protectedSet := make(map[string]struct{}, len(protected))
	for _, p := range protected {
		protectedSet[filepath.Clean(p)] = struct{}{}
	}
	// Group by exact source identity. List is sorted newest first, so
	// each group keeps that order and the group's oldest entries are at
	// the end. Legacy entries without a known source are excluded: they
	// can never be auto-removed.
	groups := map[string][]Entry{}
	var order []string
	for _, e := range entries {
		if !e.SourceKnown {
			continue
		}
		key := filepath.Clean(e.Source)
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], e)
	}
	var removed []string
	var errs []error
	for _, key := range order {
		group := groups[key]
		if len(group) <= r.Keep {
			continue
		}
		// group[0] is the newest backup of the source and is always
		// preserved; the entries beyond the Keep newest are removed from
		// the least old to the oldest.
		for _, e := range group[r.Keep:] {
			if _, ok := protectedSet[filepath.Clean(e.Path)]; ok {
				continue
			}
			if err := os.Remove(e.Path); err != nil {
				errs = append(errs, fmt.Errorf("retention: remove %s: %w", e.Path, err))
				continue
			}
			removed = append(removed, e.Path)
			// The identity sidecar is part of the backup unit; removing
			// it is best-effort so a missing or unwritable sidecar never
			// blocks the backup removal itself.
			_ = os.Remove(e.Path + sourceSuffix)
		}
	}
	return removed, errors.Join(errs...)
}
