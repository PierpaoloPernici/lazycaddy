package logs

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakePipe is a thread-safe buffered pipe: writes never block (bytes are
// buffered), reads block until data, EOF or a close-with-error arrives.
// Closing with an error unblocks a blocked read so a killed process cannot
// deadlock the supervisor.
type fakePipe struct {
	mu     sync.Mutex
	cond   *sync.Cond
	buf    []byte
	closed bool
	err    error
}

func newFakePipe() *fakePipe {
	p := &fakePipe{}
	p.cond = sync.NewCond(&p.mu)
	return p
}

func (p *fakePipe) Read(dst []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for len(p.buf) == 0 && !p.closed {
		p.cond.Wait()
	}
	if len(p.buf) == 0 {
		if p.err != nil {
			return 0, p.err
		}
		return 0, io.EOF
	}
	n := copy(dst, p.buf)
	p.buf = p.buf[n:]
	return n, nil
}

func (p *fakePipe) write(s string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.buf = append(p.buf, s...)
	p.cond.Broadcast()
}

func (p *fakePipe) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.closed {
		p.closed = true
		p.cond.Broadcast()
	}
}

func (p *fakePipe) closeWithError(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.closed = true
	p.err = err
	p.cond.Broadcast()
}

// fakeProcess is a deterministic Process fake. The test controls the output
// written to Stdout, the error returned by Wait, and observes Kill calls.
type fakeProcess struct {
	stdout   *fakePipe
	mu       sync.Mutex
	waitErr  error
	killed   bool
	killedCh chan struct{}
}

func newFakeProcess(waitErr error) *fakeProcess {
	return &fakeProcess{stdout: newFakePipe(), waitErr: waitErr, killedCh: make(chan struct{})}
}

func (p *fakeProcess) Stdout() io.Reader { return p.stdout }

func (p *fakeProcess) Wait() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.killed {
		return errors.New("process killed")
	}
	return p.waitErr
}

func (p *fakeProcess) Kill() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.killed {
		return nil
	}
	p.killed = true
	close(p.killedCh)
	p.stdout.closeWithError(io.ErrClosedPipe)
	return nil
}

// writeLines queues complete journal lines (each terminated by '\n').
func (p *fakeProcess) writeLines(lines ...string) {
	var sb strings.Builder
	for _, l := range lines {
		sb.WriteString(l)
		sb.WriteByte('\n')
	}
	p.stdout.write(sb.String())
}

// writePartial queues bytes without a trailing newline (a trailing
// incomplete line at process exit).
func (p *fakeProcess) writePartial(s string) { p.stdout.write(s) }

// finish signals EOF on the fake's stdout, letting the supervisor finish
// streaming and call Wait.
func (p *fakeProcess) finish() { p.stdout.close() }

// fakeProcessFactory queues fakeProcess values and records every Start
// invocation. onEmpty controls what Start returns once the queue is empty
// (the follow loop keeps restarting until Close, so tests usually supply a
// quiet clean-exit process).
type fakeProcessFactory struct {
	mu      sync.Mutex
	procs   []*fakeProcess
	starts  [][]string
	onEmpty func() (Process, error)
}

func (f *fakeProcessFactory) Start(ctx context.Context, name string, args ...string) (Process, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts = append(f.starts, append([]string(nil), args...))
	if len(f.procs) == 0 {
		if f.onEmpty != nil {
			return f.onEmpty()
		}
		return nil, errors.New("fake factory: no process configured")
	}
	p := f.procs[0]
	f.procs = f.procs[1:]
	return p, nil
}

func (f *fakeProcessFactory) args() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]string, len(f.starts))
	for i, a := range f.starts {
		out[i] = append([]string(nil), a...)
	}
	return out
}

// cleanEmptyProcess is a quiet clean-exit process used when the fake factory
// runs out of queued processes, so restart loops never emit entries.
func cleanEmptyProcess() (Process, error) { return emptyProcess{}, nil }

type emptyProcess struct{}

func (emptyProcess) Stdout() io.Reader { return strings.NewReader("") }
func (emptyProcess) Wait() error       { return nil }
func (emptyProcess) Kill() error       { return nil }

// eventually retries fn until it returns true or the deadline elapses.
func eventually(t *testing.T, fn func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// pollNext retries Next until it returns entries or an error (never a bare
// nil/nil), so assertions do not depend on supervisor timing.
func pollNext(t *testing.T, src *JournalSource) ([]Entry, error) {
	t.Helper()
	var last []Entry
	var lastErr error
	if !eventually(t, func() bool {
		got, err := src.Next(context.Background())
		if len(got) > 0 || err != nil {
			last, lastErr = got, err
			return true
		}
		return false
	}) {
		t.Fatal("Next produced neither entries nor an error within the deadline")
	}
	return last, lastErr
}

// drainEntries collects exactly want entries through repeated Next calls.
func drainEntries(t *testing.T, src *JournalSource, want int) []Entry {
	t.Helper()
	var all []Entry
	if !eventually(t, func() bool {
		got, err := src.Next(context.Background())
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		all = append(all, got...)
		return len(all) >= want
	}) {
		t.Fatalf("drained %d entries, want %d", len(all), want)
	}
	return all
}

func mkJournalLine(cursor, message string) string {
	return `{"__CURSOR":` + q(cursor) + `,"MESSAGE":` + q(message) + `}`
}

func q(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func assertArgContains(t *testing.T, args []string, want string) {
	t.Helper()
	for _, a := range args {
		if a == want {
			return
		}
	}
	t.Errorf("args %v do not contain %q", args, want)
}

func assertArgMissing(t *testing.T, args []string, prefix string) {
	t.Helper()
	for _, a := range args {
		if strings.HasPrefix(a, prefix) {
			t.Errorf("args %v unexpectedly contain %q", args, a)
		}
	}
}

// TestJournalSource_InitialHistory verifies that the history phase parses
// journal lines into entries that are also present in History().
func TestJournalSource_InitialHistory(t *testing.T) {
	history := newFakeProcess(nil)
	history.writeLines(
		mkJournalLine("s=1;i=1", "first line"),
		mkJournalLine("s=1;i=2", "second line"),
	)
	history.finish()
	factory := &fakeProcessFactory{procs: []*fakeProcess{history}, onEmpty: cleanEmptyProcess}
	src := NewJournalSourceWithFactory(JournalOptions{Unit: "caddy.service", MaxLines: 10}, factory)
	defer src.Close()

	entries, err := pollNext(t, src)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	assertRaw(t, entries, []string{"first line", "second line"})
	if got := src.Entries(); len(got) != 2 {
		t.Fatalf("History has %d entries, want 2", len(got))
	}
	assertRaw(t, src.Entries(), []string{"first line", "second line"})

	// The history command must not follow and must bound the request.
	args := factory.args()[0]
	assertArgMissing(t, args, "--follow")
	assertArgMissing(t, args, "--after-cursor=")
	assertArgContains(t, args, "--lines=10")
}

// TestJournalSource_FollowReusesCursor verifies that the follow phase
// resumes with --after-cursor=<last history cursor>.
func TestJournalSource_FollowReusesCursor(t *testing.T) {
	history := newFakeProcess(nil)
	history.writeLines(
		mkJournalLine("c1", "m1"),
		mkJournalLine("c2", "m2"),
	)
	history.finish()
	follow := newFakeProcess(nil)
	follow.writeLines(mkJournalLine("c3", "m3"))
	follow.finish()
	factory := &fakeProcessFactory{procs: []*fakeProcess{history, follow}, onEmpty: cleanEmptyProcess}
	src := NewJournalSourceWithFactory(JournalOptions{Unit: "caddy.service"}, factory)
	defer src.Close()

	if !eventually(t, func() bool { return len(factory.args()) >= 2 }) {
		t.Fatal("the follow process was never started")
	}
	args := factory.args()
	assertArgContains(t, args[0], "--unit=caddy.service")
	assertArgContains(t, args[0], "--output=json")
	assertArgContains(t, args[0], "--no-pager")
	assertArgContains(t, args[1], "--follow")
	assertArgContains(t, args[1], "--after-cursor=c2")
}

// TestJournalSource_FollowRestartContinuity verifies that a clean follow
// exit relaunches with the current cursor and that no entry is duplicated
// or lost across the history/follow/restart boundaries.
func TestJournalSource_FollowRestartContinuity(t *testing.T) {
	history := newFakeProcess(nil)
	history.writeLines(
		mkJournalLine("c1", "m1"),
		mkJournalLine("c2", "m2"),
	)
	history.finish()
	follow1 := newFakeProcess(nil)
	follow1.writeLines(
		mkJournalLine("c3", "m3"),
		mkJournalLine("c4", "m4"),
	)
	follow1.finish()
	follow2 := newFakeProcess(nil)
	follow2.writeLines(mkJournalLine("c5", "m5"))
	follow2.finish()
	factory := &fakeProcessFactory{procs: []*fakeProcess{history, follow1, follow2}, onEmpty: cleanEmptyProcess}
	src := NewJournalSourceWithFactory(JournalOptions{Unit: "caddy.service"}, factory)

	all := drainEntries(t, src, 5)
	src.Close()

	assertRaw(t, all, []string{"m1", "m2", "m3", "m4", "m5"})
	assertRaw(t, src.Entries(), []string{"m1", "m2", "m3", "m4", "m5"})

	args := factory.args()
	assertArgContains(t, args[1], "--after-cursor=c2")
	assertArgContains(t, args[2], "--after-cursor=c4")

	// Close prevents further restarts.
	time.Sleep(300 * time.Millisecond)
	if n := len(factory.args()); n > 3 {
		t.Fatalf("source restarted after Close: %d starts recorded", n)
	}
}

// TestJournalSource_CaddyJSONMessage verifies that a MESSAGE payload that
// is itself valid Caddy JSON is parsed with the existing Caddy model while
// the journal cursor is still tracked and curated metadata is attached.
func TestJournalSource_CaddyJSONMessage(t *testing.T) {
	caddyMsg := `{"level":"info","ts":1646861401.5241024,"logger":"http.log.access","msg":"handled request","request":{"method":"GET","host":"example.com","uri":"/"},"status":200}`
	history := newFakeProcess(nil)
	history.writeLines(`{"__CURSOR":"s=1;i=9","MESSAGE":` + q(caddyMsg) + `,"PRIORITY":"6","_PID":"4242"}`)
	history.finish()
	factory := &fakeProcessFactory{procs: []*fakeProcess{history}, onEmpty: cleanEmptyProcess}
	src := NewJournalSourceWithFactory(JournalOptions{Unit: "caddy.service"}, factory)
	defer src.Close()

	entries, err := pollNext(t, src)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("Next returned %d entries, want 1", len(entries))
	}
	e := entries[0]
	if !e.Parsed {
		t.Error("Parsed = false, want true for a Caddy JSON MESSAGE")
	}
	if e.Level != "info" || e.Logger != "http.log.access" || e.Msg != "handled request" {
		t.Errorf("got %q/%q/%q, want info/http.log.access/handled request", e.Level, e.Logger, e.Msg)
	}
	if e.Status != 200 || e.Method != "GET" || e.Host != "example.com" || e.URI != "/" {
		t.Errorf("got status=%d method=%q host=%q uri=%q, want 200/GET/example.com//", e.Status, e.Method, e.Host, e.URI)
	}
	if got, want := string(e.Raw), caddyMsg; got != want {
		t.Errorf("Raw = %q, want the MESSAGE bytes %q", got, want)
	}
	if e.Metadata["PRIORITY"] != "6" || e.Metadata["_PID"] != "4242" {
		t.Errorf("Metadata = %v, want PRIORITY=6 _PID=4242", e.Metadata)
	}
}

// TestJournalSource_NonJSONMessageMetadata verifies that a non-Caddy
// MESSAGE is preserved verbatim with Status -1 and that the curated journal
// metadata is exposed through Entry.Metadata.
func TestJournalSource_NonJSONMessageMetadata(t *testing.T) {
	history := newFakeProcess(nil)
	history.writeLines(`{"__CURSOR":"s=1;i=7","MESSAGE":"plain text from the unit","PRIORITY":"6","_PID":"4242","_SYSTEMD_UNIT":"caddy.service","SYSLOG_IDENTIFIER":"caddy","_COMM":"caddy","__REALTIME_TIMESTAMP":"1722039000000000"}`)
	history.finish()
	factory := &fakeProcessFactory{procs: []*fakeProcess{history}, onEmpty: cleanEmptyProcess}
	src := NewJournalSourceWithFactory(JournalOptions{Unit: "caddy.service"}, factory)
	defer src.Close()

	entries, err := pollNext(t, src)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	e := entries[0]
	if e.Parsed {
		t.Error("Parsed = true, want false for a non-JSON MESSAGE")
	}
	if e.Status != -1 {
		t.Errorf("Status = %d, want -1", e.Status)
	}
	if got, want := string(e.Raw), "plain text from the unit"; got != want {
		t.Errorf("Raw = %q, want %q", got, want)
	}
	wantMeta := map[string]string{
		"PRIORITY":             "6",
		"_PID":                 "4242",
		"_SYSTEMD_UNIT":        "caddy.service",
		"SYSLOG_IDENTIFIER":    "caddy",
		"_COMM":                "caddy",
		"__REALTIME_TIMESTAMP": "1722039000000000",
	}
	if len(e.Metadata) != len(wantMeta) {
		t.Fatalf("Metadata = %v, want %v", e.Metadata, wantMeta)
	}
	for k, v := range wantMeta {
		if e.Metadata[k] != v {
			t.Errorf("Metadata[%q] = %q, want %q", k, e.Metadata[k], v)
		}
	}
}

// TestJournalSource_MetadataNilWhenAbsent verifies Metadata stays nil when
// no curated journal field is present.
func TestJournalSource_MetadataNilWhenAbsent(t *testing.T) {
	history := newFakeProcess(nil)
	history.writeLines(`{"__CURSOR":"c1","MESSAGE":"no metadata here"}`)
	history.finish()
	factory := &fakeProcessFactory{procs: []*fakeProcess{history}, onEmpty: cleanEmptyProcess}
	src := NewJournalSourceWithFactory(JournalOptions{Unit: "caddy.service"}, factory)
	defer src.Close()

	entries, err := pollNext(t, src)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if e := entries[0]; e.Metadata != nil {
		t.Errorf("Metadata = %v, want nil", e.Metadata)
	}
}

// TestJournalSource_BoundedHistory verifies that when the fake journal
// returns more lines than MaxLines only the tail survives in History(), and
// that the history request bounds itself with --lines=<MaxLines>.
func TestJournalSource_BoundedHistory(t *testing.T) {
	history := newFakeProcess(nil)
	history.writeLines(
		mkJournalLine("c1", "m1"),
		mkJournalLine("c2", "m2"),
		mkJournalLine("c3", "m3"),
		mkJournalLine("c4", "m4"),
		mkJournalLine("c5", "m5"),
	)
	history.finish()
	factory := &fakeProcessFactory{procs: []*fakeProcess{history}, onEmpty: cleanEmptyProcess}
	src := NewJournalSourceWithFactory(JournalOptions{Unit: "caddy.service", MaxLines: 3}, factory)

	// Drain all five; the buffer keeps only the tail.
	drainEntries(t, src, 5)
	src.Close()

	assertRaw(t, src.Entries(), []string{"m3", "m4", "m5"})
	assertArgContains(t, factory.args()[0], "--lines=3")
}

// TestJournalSource_CancelAndClose verifies ctx.Err() is reported first,
// that Close kills the running process, prevents restarts and makes Next
// return ErrClosed, and that already-consumed history survives Close.
func TestJournalSource_CancelAndClose(t *testing.T) {
	history := newFakeProcess(nil)
	history.writeLines(mkJournalLine("c1", "m1"))
	history.finish()
	follow := newFakeProcess(nil) // stays open: the supervisor blocks reading it
	factory := &fakeProcessFactory{procs: []*fakeProcess{history, follow}, onEmpty: cleanEmptyProcess}
	src := NewJournalSourceWithFactory(JournalOptions{Unit: "caddy.service"}, factory)

	// A cancelled context is reported even while entries are pending.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := src.Next(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Next with cancelled ctx = %v, want context.Canceled", err)
	}

	// Consume the history entry before Close so it is provably in History.
	if got, err := pollNext(t, src); err != nil || len(got) != 1 {
		t.Fatalf("Next = %v/%v, want the history entry", got, err)
	}

	// Wait until the follow process is running, then Close must kill it.
	if !eventually(t, func() bool { return len(factory.args()) >= 2 }) {
		t.Fatal("the follow process was never started")
	}
	if err := src.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-follow.killedCh:
	default:
		t.Fatal("Close did not kill the running process")
	}

	if _, err := src.Next(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("Next after Close = %v, want ErrClosed", err)
	}
	assertRaw(t, src.Entries(), []string{"m1"})

	// No further restarts after Close.
	time.Sleep(300 * time.Millisecond)
	if n := len(factory.args()); n > 2 {
		t.Fatalf("source restarted after Close: %d starts recorded", n)
	}
}

// TestJournalSource_RepeatedClose verifies Close is idempotent: a second
// call returns nil promptly, never panics, and does not kill again.
func TestJournalSource_RepeatedClose(t *testing.T) {
	history := newFakeProcess(nil)
	history.writeLines(mkJournalLine("c1", "m1"))
	history.finish()
	follow := newFakeProcess(nil) // stays open: the supervisor blocks reading it
	factory := &fakeProcessFactory{procs: []*fakeProcess{history, follow}, onEmpty: cleanEmptyProcess}
	src := NewJournalSourceWithFactory(JournalOptions{Unit: "caddy.service"}, factory)

	if !eventually(t, func() bool { return len(factory.args()) >= 2 }) {
		t.Fatal("the follow process was never started")
	}
	if err := src.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := src.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// TestJournalSource_FollowUnexpectedExit verifies that a non-zero follow
// exit surfaces a wrapped error (with exit code, stderr excerpt and unit)
// once and then resumes following from the current cursor without
// duplicates.
func TestJournalSource_FollowUnexpectedExit(t *testing.T) {
	history := newFakeProcess(nil)
	history.writeLines(mkJournalLine("c1", "m1"))
	history.finish()
	follow1 := newFakeProcess(&JournalExitError{ExitCode: 1, Stderr: "Failed to search journal: Operation not permitted"})
	follow1.finish()
	follow2 := newFakeProcess(nil)
	follow2.writeLines(mkJournalLine("c2", "m2"))
	follow2.finish()
	factory := &fakeProcessFactory{procs: []*fakeProcess{history, follow1, follow2}, onEmpty: cleanEmptyProcess}
	src := NewJournalSourceWithFactory(JournalOptions{Unit: "caddy.service"}, factory)
	defer src.Close()

	var collected []Entry
	var exitErr error
	if !eventually(t, func() bool {
		got, err := src.Next(context.Background())
		collected = append(collected, got...)
		if err != nil {
			exitErr = err
			return true
		}
		return false
	}) {
		t.Fatal("no error surfaced for the unexpected follow exit")
	}
	if !errors.Is(exitErr, ErrJournalctlExit) {
		t.Fatalf("error = %v, want ErrJournalctlExit", exitErr)
	}
	msg := exitErr.Error()
	if !strings.Contains(msg, "caddy.service") || !strings.Contains(msg, "Operation not permitted") {
		t.Errorf("error message %q lacks unit or stderr context", msg)
	}

	// The source resumed following: the new entry arrives without any
	// duplicate from the failed follow.
	if !eventually(t, func() bool {
		got, err := src.Next(context.Background())
		collected = append(collected, got...)
		if err != nil {
			return false // transient errors after the first are ignored
		}
		return len(collected) >= 2 && string(collected[len(collected)-1].Raw) == "m2"
	}) {
		t.Fatalf("entries after restart = %v, want [m1, m2]", rawStrings(collected))
	}
	// The restarted follow reused the last cursor seen (m1's cursor); the
	// failed follow emitted nothing, so there is no duplicate.
	args := factory.args()
	if len(args) < 3 {
		t.Fatalf("recorded %d starts, want at least 3", len(args))
	}
	assertArgContains(t, args[2], "--after-cursor=c1")
}

// TestJournalSource_MissingBinary verifies that an impossible journalctl
// path surfaces the ErrBinaryMissing sentinel without spawning a process.
func TestJournalSource_MissingBinary(t *testing.T) {
	src := NewJournalSource(JournalOptions{Unit: "caddy.service", BinaryPath: "/nonexistent/journalctl"})
	defer src.Close()

	_, err := pollNext(t, src)
	if !errors.Is(err, ErrBinaryMissing) {
		t.Fatalf("Next = %v, want ErrBinaryMissing", err)
	}
}

// TestJournalSource_HistoryNonZeroExit verifies that a non-zero history
// exit (unavailable unit or missing journal permissions) is a terminal
// error carrying the unit name and a redacted stderr excerpt.
func TestJournalSource_HistoryNonZeroExit(t *testing.T) {
	tests := []struct {
		name        string
		stderr      string
		wantExcerpt string
		forbidden   string
	}{
		{
			name:        "permission denied",
			stderr:      "Failed to search journal: Operation not permitted",
			wantExcerpt: "Failed to search journal: Operation not permitted",
		},
		{
			name:        "unit not found",
			stderr:      "Unit caddy.service not found.",
			wantExcerpt: "Unit caddy.service not found.",
		},
		{
			name:        "secret redacted",
			stderr:      "config password=hello123 rejected",
			wantExcerpt: "password=<redacted>",
			forbidden:   "hello123",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			history := newFakeProcess(&JournalExitError{ExitCode: 1, Stderr: tt.stderr})
			history.finish()
			factory := &fakeProcessFactory{procs: []*fakeProcess{history}}
			src := NewJournalSourceWithFactory(JournalOptions{Unit: "caddy.service"}, factory)
			defer src.Close()

			_, err := pollNext(t, src)
			if !errors.Is(err, ErrJournalctlExit) {
				t.Fatalf("Next = %v, want ErrJournalctlExit", err)
			}
			msg := err.Error()
			if !strings.Contains(msg, "caddy.service") {
				t.Errorf("error %q does not name the unit", msg)
			}
			if !strings.Contains(msg, tt.wantExcerpt) {
				t.Errorf("error %q does not include the stderr excerpt %q", msg, tt.wantExcerpt)
			}
			if tt.forbidden != "" && strings.Contains(msg, tt.forbidden) {
				t.Errorf("error %q leaked a secret-shaped value", msg)
			}
			// A terminal history error must not start a follow phase.
			if got := len(factory.args()); got != 1 {
				t.Errorf("%d process starts recorded, want 1 (no follow after terminal history error)", got)
			}
		})
	}
}

// TestJournalSource_MalformedOutput verifies that a complete journal line
// that is not valid JSON is a terminal ErrMalformedOutput.
func TestJournalSource_MalformedOutput(t *testing.T) {
	history := newFakeProcess(nil)
	history.writeLines("this is not json")
	history.finish()
	factory := &fakeProcessFactory{procs: []*fakeProcess{history}}
	src := NewJournalSourceWithFactory(JournalOptions{Unit: "caddy.service"}, factory)
	defer src.Close()

	_, err := pollNext(t, src)
	if !errors.Is(err, ErrMalformedOutput) {
		t.Fatalf("Next = %v, want ErrMalformedOutput", err)
	}
}

// TestJournalSource_TrailingPartialLineDropped verifies that an incomplete
// trailing line at process exit (no trailing newline) is dropped silently
// and never reported as malformed output.
func TestJournalSource_TrailingPartialLineDropped(t *testing.T) {
	history := newFakeProcess(nil)
	history.writeLines(mkJournalLine("c1", "complete line"))
	history.writePartial(`{"__CURSOR":"c2","MESSAGE":"incomplete`)
	history.finish()
	factory := &fakeProcessFactory{procs: []*fakeProcess{history}, onEmpty: cleanEmptyProcess}
	src := NewJournalSourceWithFactory(JournalOptions{Unit: "caddy.service"}, factory)
	defer src.Close()

	entries, err := pollNext(t, src)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	assertRaw(t, entries, []string{"complete line"})
}
