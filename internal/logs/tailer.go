package logs

import (
	"bytes"
	"context"
	"io"
	"os"
)

// readChunkSize is the buffer used for each ReadAt on the followed file.
const readChunkSize = 32 * 1024

// Options configures a Tailer.
type Options struct {
	// Path is the log file to follow.
	Path string
	// MaxLines is the in-memory history capacity (<= 0 -> 1000).
	MaxLines int
}

// Tailer follows a log file with tail -F semantics: it reads appended
// lines, carries incomplete trailing lines across polls, and switches to
// a freshly created file when the original is rotated away (Caddy renames
// the active file and creates a new one at the same path). It is the v0.1
// read-only log source.
type Tailer struct {
	path     string
	buffer   *Buffer
	file     *os.File
	fileInfo os.FileInfo
	offset   int64
	carry    []byte
}

// NewTailer returns a Tailer for opts.Path. It does not touch the
// filesystem yet: the first Next call opens the file, so a log file that
// does not exist yet is fine (Next keeps returning nothing until it
// appears).
func NewTailer(opts Options) *Tailer {
	return &Tailer{
		path:   opts.Path,
		buffer: NewBuffer(opts.MaxLines),
	}
}

// Next returns entries for the complete new lines appended since the last
// call and appends them to the internal bounded history. Behavior:
//   - file does not exist yet -> return nil, nil (no error; keep polling)
//   - file cannot be opened/read for another reason -> return the error
//   - only a partial final line was appended since last read -> carry it;
//     return entries only for COMPLETE lines (ending with '\n')
//   - rotation detected (path now points to a file whose identity differs
//     from the one being read) -> first drain remaining complete lines
//     from the old handle, then open the new file and continue from the
//     start of the new file
//   - file truncated -> reset to the start of the current file; the
//     partial-line carry is dropped too, so a line spanning the
//     truncation point is never recombined with pre-truncation bytes
//
// The ctx value is checked between file operations; a cancelled ctx
// returns context.Canceled.
func (t *Tailer) Next(ctx context.Context) ([]Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if t.file == nil {
		if err := t.open(); err != nil {
			if os.IsNotExist(err) {
				return nil, nil
			}
			return nil, err
		}
	}

	pathInfo, err := os.Stat(t.path)
	if err != nil {
		if os.IsNotExist(err) {
			// The file is temporarily gone; keep the current handle and
			// keep polling (tail -F behavior).
			return nil, nil
		}
		return nil, err
	}

	if !os.SameFile(pathInfo, t.fileInfo) {
		// Rotation: the path now names a different file. Drain whatever is
		// left of the old file, then switch to the new one.
		drained, err := t.readAll(ctx)
		if err != nil {
			return nil, err
		}
		if err := t.file.Close(); err != nil {
			return nil, err
		}
		t.file = nil
		t.fileInfo = nil
		if err := t.open(); err != nil {
			if os.IsNotExist(err) {
				return drained, nil
			}
			return nil, err
		}
		rest, err := t.readAll(ctx)
		if err != nil {
			return nil, err
		}
		return append(drained, rest...), nil
	}

	if pathInfo.Size() < t.offset {
		// The file was truncated; keep the handle and start over. The
		// partial-line carry must be dropped too: prepending bytes read
		// before the truncation to the first post-truncation chunk would
		// corrupt the first record.
		t.offset = 0
		t.carry = nil
	}
	return t.readAll(ctx)
}

// Entries returns a copy of the current bounded history.
func (t *Tailer) Entries() []Entry {
	return t.buffer.Entries()
}

// Close closes the underlying file handle, if any. Safe to call more than
// once; subsequent Next calls will re-open.
func (t *Tailer) Close() error {
	if t.file == nil {
		return nil
	}
	err := t.file.Close()
	t.file = nil
	t.fileInfo = nil
	return err
}

// open opens opts.Path, resets the read offset and drops any partial carry.
func (t *Tailer) open() error {
	f, err := os.Open(t.path)
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	t.file = f
	t.fileInfo = fi
	t.offset = 0
	t.carry = nil
	return nil
}

// readAll reads from the current offset to EOF on the current handle,
// processing chunks into complete lines and appending them to history.
// It returns the newly parsed entries.
func (t *Tailer) readAll(ctx context.Context) ([]Entry, error) {
	buf := make([]byte, readChunkSize)
	var out []Entry
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, err := t.file.ReadAt(buf, t.offset)
		if n > 0 {
			t.offset += int64(n)
			out = append(out, t.processChunk(buf[:n])...)
		}
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return out, err
		}
	}
}

// processChunk splits one read chunk into complete lines (prepending any
// carried partial line), parses them, appends them to history and returns
// the newly parsed entries. The trailing segment without a newline becomes
// the new carry.
func (t *Tailer) processChunk(chunk []byte) []Entry {
	combined := chunk
	if len(t.carry) > 0 {
		c := make([]byte, 0, len(t.carry)+len(chunk))
		c = append(c, t.carry...)
		c = append(c, chunk...)
		combined = c
	}
	segs := bytes.Split(combined, []byte{'\n'})
	var out []Entry
	for _, seg := range segs[:len(segs)-1] {
		line := append([]byte(nil), seg...)
		if n := len(line); n > 0 && line[n-1] == '\r' {
			line = line[:n-1]
		}
		if len(line) == 0 {
			continue
		}
		if e, err := ParseEntry(line); err == nil {
			out = append(out, e)
		} else {
			// Not valid JSON: still surface the raw line so the UI can
			// show it verbatim.
			out = append(out, Entry{Raw: line, Status: -1})
		}
	}
	last := segs[len(segs)-1]
	if len(last) == 0 {
		t.carry = nil
	} else {
		t.carry = append([]byte(nil), last...)
	}
	t.buffer.Append(out...)
	return out
}
