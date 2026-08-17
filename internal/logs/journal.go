package logs

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Process is a streaming subprocess boundary: Stdout is drained by the
// caller until EOF, then Wait reports the exit status. Kill terminates the
// process and unblocks any pending read on Stdout. Implementations capture
// stderr internally so Wait can report it on failure.
type Process interface {
	Stdout() io.Reader
	Wait() error
	Kill() error
}

// ProcessFactory starts named subprocesses. Implementations must never
// route through a shell (no sh -c): the name and args are handed to the
// operating system directly. Tests substitute a fake factory so no real
// process is ever spawned.
type ProcessFactory interface {
	Start(ctx context.Context, name string, args ...string) (Process, error)
}

// Sentinel errors returned by JournalSource. They are wrapped by concrete
// errors; callers should branch with errors.Is.
var (
	// ErrBinaryMissing is returned when the journalctl binary cannot be
	// located (PATH lookup failure or a nonexistent absolute path).
	ErrBinaryMissing = errors.New("journalctl binary not available")
	// ErrMalformedOutput is returned when journalctl emits a complete
	// line that is not valid journal JSON.
	ErrMalformedOutput = errors.New("journalctl produced malformed output")
	// ErrJournalctlExit is wrapped by JournalExitError when journalctl
	// terminates with a non-zero exit code.
	ErrJournalctlExit = errors.New("journalctl exited non-zero")
	// ErrClosed is returned by Next after Close was called.
	ErrClosed = errors.New("journal source closed")
)

// JournalExitError records a non-zero journalctl exit along with a redacted
// excerpt of its stderr. It wraps ErrJournalctlExit.
type JournalExitError struct {
	// ExitCode is the process exit code reported by journalctl.
	ExitCode int
	// Stderr holds the redacted, length-bounded stderr excerpt.
	Stderr string
	// Unit is the systemd unit that was being followed (empty when
	// unknown).
	Unit string
}

// Error implements the error interface.
func (e *JournalExitError) Error() string {
	unit := e.Unit
	if unit == "" {
		unit = "<unknown>"
	}
	if msg := redactExcerpt(e.Stderr); msg != "" {
		return fmt.Sprintf("journalctl exited with code %d (unit %s): %s", e.ExitCode, unit, msg)
	}
	return fmt.Sprintf("journalctl exited with code %d (unit %s)", e.ExitCode, unit)
}

// Unwrap returns ErrJournalctlExit so errors.Is can identify the high-level
// failure mode without exposing the wrapper type.
func (e *JournalExitError) Unwrap() error { return ErrJournalctlExit }

// ExecProcessFactory starts real subprocesses through os/exec. It is the
// production ProcessFactory.
type ExecProcessFactory struct{}

// Start implements ProcessFactory. A failed start maps PATH lookup failures
// (exec.ErrNotFound) and missing absolute paths (os.ErrNotExist) to
// ErrBinaryMissing.
func (ExecProcessFactory) Start(ctx context.Context, name string, args ...string) (Process, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrBinaryMissing, name)
		}
		if cerr := ctx.Err(); cerr != nil {
			return nil, cerr
		}
		return nil, err
	}
	return &execProcess{cmd: cmd, stdout: stdout, stderr: &stderr}, nil
}

// execProcess is the production Process: an os/exec command whose stderr is
// captured into an in-memory buffer for error reporting.
type execProcess struct {
	cmd    *exec.Cmd
	stdout io.Reader
	stderr *bytes.Buffer
}

// Stdout implements Process.
func (p *execProcess) Stdout() io.Reader { return p.stdout }

// Wait implements Process, mapping a non-zero exit to a JournalExitError
// carrying the captured stderr.
func (p *execProcess) Wait() error {
	err := p.cmd.Wait()
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return &JournalExitError{
			ExitCode: exitErr.ExitCode(),
			Stderr:   p.stderr.String(),
		}
	}
	return err
}

// Kill implements Process.
func (p *execProcess) Kill() error {
	if p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Kill()
}

// JournalOptions configures a JournalSource.
type JournalOptions struct {
	// Unit is the systemd unit to read, for example "caddy.service".
	Unit string
	// MaxLines is the bounded in-memory history capacity (<= 0 -> 1000).
	// It is also the journalctl --lines=N bound on the initial history
	// query.
	MaxLines int
	// BinaryPath overrides the journalctl binary. Empty uses "journalctl"
	// from PATH. Tests use an impossible path to exercise the missing
	// binary error without spawning a real process.
	BinaryPath string
}

// JournalSource is a read-only systemd journal log source. It runs a
// bounded `journalctl --lines=N` history query and then follows the unit
// with `journalctl --follow --after-cursor=<last>`, restarting the follow
// process when it exits so the journal cursor never regresses (no
// duplicates, no gaps). The process lifetime is owned by the source: a
// supervisor goroutine runs until Close is called, independent of the
// per-call context passed to Next (the UI polls Next with a fresh
// context.Background() on every tick).
//
// The source is strictly read-only and never invokes a shell: journalctl is
// started directly with explicit arguments.
type JournalSource struct {
	unit    string
	binPath string
	buffer  *Buffer
	factory ProcessFactory

	// entries carries parsed entries from the supervisor to Next; the
	// bounded capacity provides backpressure against a slow consumer.
	entries chan Entry
	stop    chan struct{}
	done    chan struct{}

	baseCtx    context.Context
	cancelBase context.CancelFunc

	// lastCursor is owned by the supervisor goroutine: it is updated while
	// streaming every line that carries a cursor and read when building the
	// follow command, so it never needs external synchronization.
	lastCursor string

	mu         sync.Mutex
	err        error // terminal error, returned by every subsequent Next
	pendingErr error // transient error, surfaced by Next once
	process    Process
	closed     bool

	closeOnce    sync.Once
	bufMu        sync.Mutex
	restartDelay time.Duration
}

// NewJournalSource returns a JournalSource for opts using the real exec
// process factory. The supervisor starts immediately and runs until Close.
func NewJournalSource(opts JournalOptions) *JournalSource {
	return NewJournalSourceWithFactory(opts, ExecProcessFactory{})
}

// NewJournalSourceWithFactory returns a JournalSource using factory to
// start processes. Tests substitute a fake factory so no real process is
// ever spawned.
func NewJournalSourceWithFactory(opts JournalOptions, factory ProcessFactory) *JournalSource {
	if factory == nil {
		factory = ExecProcessFactory{}
	}
	binPath := opts.BinaryPath
	if binPath == "" {
		binPath = "journalctl"
	}
	baseCtx, cancelBase := context.WithCancel(context.Background())
	j := &JournalSource{
		unit:         opts.Unit,
		binPath:      binPath,
		buffer:       NewBuffer(opts.MaxLines),
		factory:      factory,
		entries:      make(chan Entry, 1024),
		stop:         make(chan struct{}),
		done:         make(chan struct{}),
		baseCtx:      baseCtx,
		cancelBase:   cancelBase,
		restartDelay: 250 * time.Millisecond,
	}
	go j.supervise()
	return j
}

// Next drains the latest streamed entries. It checks ctx.Err() FIRST so a
// cancelled context returns context.Canceled even when entries are pending.
// A nil error with nil entries means "no new lines". Terminal errors (for
// example a missing journalctl binary) are returned on every subsequent
// call so the UI keeps reporting the failure.
func (j *JournalSource) Next(ctx context.Context) ([]Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	j.mu.Lock()
	termErr := j.err
	closed := j.closed
	j.mu.Unlock()
	if closed {
		return nil, ErrClosed
	}

	var out []Entry
drain:
	for {
		select {
		case e := <-j.entries:
			out = append(out, e)
		default:
			break drain
		}
	}
	if len(out) > 0 {
		j.bufMu.Lock()
		j.buffer.Append(out...)
		j.bufMu.Unlock()
		return out, nil
	}

	j.mu.Lock()
	pending := j.pendingErr
	j.pendingErr = nil
	j.mu.Unlock()
	if termErr != nil {
		return nil, termErr
	}
	if pending != nil {
		return nil, pending
	}
	return nil, nil
}

// Entries returns a copy of the consumed bounded history.
func (j *JournalSource) Entries() []Entry {
	j.bufMu.Lock()
	defer j.bufMu.Unlock()
	return j.buffer.Entries()
}

// Close kills the current process, stops the supervisor and prevents any
// further restarts. Safe to call more than once; it blocks until the
// supervisor has exited.
func (j *JournalSource) Close() error {
	j.closeOnce.Do(func() {
		j.mu.Lock()
		j.closed = true
		p := j.process
		j.mu.Unlock()
		j.cancelBase()
		close(j.stop)
		if p != nil {
			p.Kill()
		}
	})
	<-j.done
	return nil
}

// supervise is the single owner of the journalctl processes. It runs the
// history phase once, then loops over follow phases, restarting with the
// current cursor whenever the follow process exits without cancellation.
func (j *JournalSource) supervise() {
	defer close(j.done)

	// Phase 1: bounded history. A failure here (missing binary, permission
	// or malformed output) is terminal: there is no cursor baseline to
	// resume from.
	err := j.runHistory()
	if j.isStopped() {
		return
	}
	if err != nil {
		j.setErr(err)
		return
	}

	// Phase 2: follow, restarting on clean exit or unexpected termination.
	for {
		if j.isStopped() {
			return
		}
		waitErr := j.runFollow()
		if j.isStopped() {
			return
		}
		switch {
		case waitErr == nil:
			// Clean exit: restart from the current cursor.
		case errors.Is(waitErr, ErrBinaryMissing):
			j.setErr(waitErr)
			return
		case errors.Is(waitErr, ErrMalformedOutput):
			j.setErr(waitErr)
			return
		default:
			// Unexpected process termination: wrap the exit code and stderr
			// (distinct from a clean restart path) and resume following.
			j.setPendingErr(waitErr)
		}
		select {
		case <-j.stop:
			return
		case <-time.After(j.restartDelay):
		}
	}
}

// runHistory runs the initial bounded history query. Any failure other than
// a stop is terminal for the source.
func (j *JournalSource) runHistory() error {
	args := []string{
		"--unit=" + j.unit,
		"--output=json",
		"--no-pager",
		"--lines=" + strconv.Itoa(j.buffer.MaxLines()),
	}
	err := j.runAndStream(args)
	if err == nil || j.isStopped() {
		return err
	}
	if errors.Is(err, ErrBinaryMissing) {
		return err
	}
	// Any other history failure (most commonly a non-zero exit from an
	// unavailable unit or missing journal permissions) carries the unit
	// name for the error message.
	return j.withUnit(err)
}

// runFollow runs one follow phase. A clean exit returns nil so the caller
// restarts with the current cursor.
func (j *JournalSource) runFollow() error {
	args := []string{
		"--unit=" + j.unit,
		"--output=json",
		"--no-pager",
		"--follow",
	}
	if j.lastCursor != "" {
		args = append(args, "--after-cursor="+j.lastCursor)
	}
	err := j.runAndStream(args)
	if err == nil || j.isStopped() {
		return err
	}
	if errors.Is(err, ErrBinaryMissing) || errors.Is(err, ErrMalformedOutput) {
		return err
	}
	return j.withUnit(err)
}

// runAndStream starts journalctl with args, streams its stdout line by line
// into the entry channel and returns the process Wait() result. On a stream
// failure (for example malformed output) the process is killed before Wait
// is called, and the stream error takes precedence over the exit status.
func (j *JournalSource) runAndStream(args []string) error {
	p, err := j.factory.Start(j.baseCtx, j.binPath, args...)
	if err != nil {
		return mapStartError(err, j.binPath)
	}
	j.setProcess(p)
	if j.isStopped() {
		p.Kill()
		return errStopped
	}
	defer j.clearProcess(p)

	streamErr := j.stream(p)
	if streamErr != nil {
		p.Kill()
	}
	waitErr := p.Wait()
	if streamErr != nil {
		return streamErr
	}
	if waitErr != nil {
		var exitErr *JournalExitError
		if errors.As(waitErr, &exitErr) {
			return exitErr
		}
		return waitErr
	}
	return nil
}

// stream reads journalctl stdout line by line, parsing each complete line
// and sending the resulting entry to the channel. A trailing incomplete
// line at EOF (no trailing newline) is dropped silently, never an error:
// journalctl was killed mid-line or the pipe closed without a final
// newline, so those bytes cannot form a complete journal object.
func (j *JournalSource) stream(p Process) error {
	r := bufio.NewReader(p.Stdout())
	for {
		seg, err := r.ReadBytes('\n')
		if len(seg) > 0 && err == nil {
			line := stripNewline(seg)
			if len(line) > 0 {
				if lerr := j.handleLine(line); lerr != nil {
					return lerr
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

// handleLine parses one complete journal line into an entry, tracks the
// cursor and hands the entry to the consumer channel. A complete line that
// is not valid journal JSON is malformed output.
func (j *JournalSource) handleLine(line []byte) error {
	line = stripTrailingCR(line)
	jl, err := parseJournalLine(line)
	if err != nil {
		return fmt.Errorf("%w: invalid journal JSON: %s: %v", ErrMalformedOutput, excerpt(line), err)
	}
	if jl.Cursor != "" {
		j.lastCursor = jl.Cursor
	}
	e := entryFromJournal(jl)
	select {
	case j.entries <- e:
		return nil
	case <-j.stop:
		return errStopped
	}
}

// entryFromJournal builds an Entry from a decoded journal line. When the
// MESSAGE payload is itself valid Caddy JSON it is parsed with ParseEntry so
// the existing Caddy model applies; otherwise the raw message is preserved
// verbatim. Curated journal metadata is attached in both cases.
func entryFromJournal(jl journalLine) Entry {
	msg := []byte(jl.Message)
	if e, err := ParseEntry(msg); err == nil {
		e.Raw = msg
		e.Metadata = jl.Metadata
		return e
	}
	return Entry{
		Raw:      msg,
		Status:   -1,
		Parsed:   false,
		Metadata: jl.Metadata,
	}
}

// journalMetadataKeys is the curated whitelist of journald fields copied
// into Entry.Metadata. The set is deliberately small: PRIORITY (severity),
// _PID, _SYSTEMD_UNIT (emitting unit), SYSLOG_IDENTIFIER and _COMM (program
// name), and __REALTIME_TIMESTAMP (microsecond wall clock).
var journalMetadataKeys = []string{
	"PRIORITY",
	"_PID",
	"_SYSTEMD_UNIT",
	"SYSLOG_IDENTIFIER",
	"_COMM",
	"__REALTIME_TIMESTAMP",
}

// journalLine is one decoded journal JSON object; only the cursor, the
// MESSAGE payload and the curated metadata fields are retained.
type journalLine struct {
	Cursor   string
	Message  string
	Metadata map[string]string
}

// parseJournalLine decodes one journalctl --output=json line. Journald
// emits one JSON object per line with string values.
func parseJournalLine(line []byte) (journalLine, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(line, &m); err != nil {
		return journalLine{}, err
	}
	jl := journalLine{
		Cursor:  rawJSONString(m["__CURSOR"]),
		Message: rawJSONString(m["MESSAGE"]),
	}
	for _, k := range journalMetadataKeys {
		if v, ok := m[k]; ok {
			if s := rawJSONString(v); s != "" {
				if jl.Metadata == nil {
					jl.Metadata = make(map[string]string)
				}
				jl.Metadata[k] = s
			}
		}
	}
	return jl, nil
}

// rawJSONString extracts a JSON string value, returning "" when the key is
// absent, null or not a string.
func rawJSONString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

// mapStartError normalizes a process start failure. PATH lookup failures
// and missing absolute paths map to ErrBinaryMissing.
func mapStartError(err error, name string) error {
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: %s", ErrBinaryMissing, name)
	}
	return err
}

// withUnit attaches the followed unit to a JournalExitError so error
// messages identify the target.
func (j *JournalSource) withUnit(err error) error {
	var ee *JournalExitError
	if errors.As(err, &ee) && ee.Unit == "" {
		cp := *ee
		cp.Unit = j.unit
		return &cp
	}
	return err
}

func (j *JournalSource) setErr(err error) {
	j.mu.Lock()
	j.err = err
	j.mu.Unlock()
}

func (j *JournalSource) setPendingErr(err error) {
	j.mu.Lock()
	j.pendingErr = err
	j.mu.Unlock()
}

func (j *JournalSource) setProcess(p Process) {
	j.mu.Lock()
	j.process = p
	j.mu.Unlock()
}

func (j *JournalSource) clearProcess(p Process) {
	j.mu.Lock()
	if j.process == p {
		j.process = nil
	}
	j.mu.Unlock()
}

func (j *JournalSource) isStopped() bool {
	select {
	case <-j.stop:
		return true
	default:
		return false
	}
}

// errStopped is returned internally when the supervisor must stop; it is
// never surfaced by Next (Close returns ErrClosed instead).
var errStopped = errors.New("journal source stopped")

// journalSecretRe masks secret-shaped KEY=VALUE tokens before stderr is
// surfaced, mirroring the validator's conservative redactor.
var journalSecretRe = regexp.MustCompile(`(?i)(\b(?:password|passwd|secret|token|api[_-]?key|private[_-]?key|access[_-]?key|client[_-]?secret|authorization)\s*=\s*)("[^"]*"|'[^']*'|[^\s"',;=]+)`)

// redactExcerpt returns a trimmed, secret-redacted, length-bounded excerpt
// of journalctl stderr for error reporting.
func redactExcerpt(stderr string) string {
	s := journalSecretRe.ReplaceAllString(stderr, "${1}<redacted>")
	s = strings.TrimSpace(s)
	if len(s) > 512 {
		s = s[:512] + "…"
	}
	return s
}

// excerpt bounds a snippet of a malformed line for error context.
func excerpt(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 120 {
		s = s[:120] + "…"
	}
	return s
}

func stripNewline(seg []byte) []byte {
	if n := len(seg); n > 0 && seg[n-1] == '\n' {
		return seg[:n-1]
	}
	return seg
}
