package backup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
)

// layout is the timestamp embedded in every backup name,
// e.g. "2026-08-01T20-10-00". The dashes (not colons) keep the name
// valid as a filename on every platform.
const layout = "2006-01-02T15-04-05"

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
// tested with a fake.
type Creator interface {
	// Create backs up the file at srcPath and returns the path of the
	// new backup. The backup name embeds a timestamp and a
	// collision-safe sequence, e.g. "2026-08-01T20-10-00-001-Caddyfile".
	Create(srcPath string) (string, error)
}

// creator is the concrete Creator returned by New.
type creator struct {
	opts Options
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
	return path, nil
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
		if !strings.HasPrefix(name, prefix) || len(name) < 25 {
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

// Entry describes one backup on disk, rebuilt from its filename.
type Entry struct {
	Path     string
	Time     time.Time
	Sequence int
}

// List rebuilds the backup index from the files in dir, sorted newest
// first (Time desc, then Sequence desc). Files that do not match the
// backup name format are ignored. A missing dir yields no entries and
// no error.
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
		e, ok := parseName(de.Name())
		if !ok {
			continue
		}
		e.Path = filepath.Join(dir, de.Name())
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
