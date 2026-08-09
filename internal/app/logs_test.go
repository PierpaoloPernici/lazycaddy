package app

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PierpaoloPernici/lazycaddy/internal/logs"
)

// TestNewLogSource_WrapsTailer verifies the adapter end-to-end against a
// real temp file (no network, no Caddy): entries written to the file are
// surfaced through LogSource.Next and the bounded history through
// LogSource.History.
func TestNewLogSource_WrapsTailer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "access.log")
	if err := os.WriteFile(path, []byte(`{"level":"info","msg":"first"}
{"level":"error","msg":"second"}
`), 0o644); err != nil {
		t.Fatalf("write log file: %v", err)
	}

	src := NewLogSource(logs.NewTailer(logs.Options{Path: path}))
	entries, err := src.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("Next returned %d entries, want 2", len(entries))
	}
	if entries[0].Msg != "first" || entries[1].Msg != "second" {
		t.Errorf("entries = %+v, want msgs first/second", entries)
	}
	if entries[0].Level != "info" || entries[1].Level != "error" {
		t.Errorf("levels = %q/%q, want info/error", entries[0].Level, entries[1].Level)
	}
	// History reflects the same tail.
	hist := src.History()
	if len(hist) != 2 {
		t.Fatalf("History returned %d entries, want 2", len(hist))
	}
	// A follow-up poll finds nothing new (no error, no entries).
	again, err := src.Next(context.Background())
	if err != nil {
		t.Fatalf("second Next: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("second Next returned %d entries, want 0", len(again))
	}
}

// TestNewLogSource_MissingFileKeepsPolling verifies that a log file that
// does not exist yet is not a failure: Next returns no entries and no
// error, so the UI keeps polling until the file appears.
func TestNewLogSource_MissingFileKeepsPolling(t *testing.T) {
	dir := t.TempDir()
	src := NewLogSource(logs.NewTailer(logs.Options{Path: filepath.Join(dir, "missing.log")}))
	entries, err := src.Next(context.Background())
	if err != nil {
		t.Fatalf("Next for a missing file: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("Next returned %d entries for a missing file, want 0", len(entries))
	}
}

// appFakeProcess and appFakeFactory are the minimal Process/ProcessFactory
// fakes needed to drive a real JournalSource without spawning processes.
type appFakeProcess struct{ out *strings.Reader }

func (p *appFakeProcess) Stdout() io.Reader { return p.out }
func (p *appFakeProcess) Wait() error       { return nil }
func (p *appFakeProcess) Kill() error       { return nil }

// appBlockingProcess is a fake process whose stdout blocks until Kill
// closes the read side, so a close test can deterministically observe the
// kill while the supervisor is streaming.
type appBlockingProcess struct {
	pr     *io.PipeReader
	pw     *io.PipeWriter
	killed atomic.Bool
}

func newAppBlockingProcess() *appBlockingProcess {
	pr, pw := io.Pipe()
	return &appBlockingProcess{pr: pr, pw: pw}
}

func (p *appBlockingProcess) Stdout() io.Reader { return p.pr }
func (p *appBlockingProcess) Wait() error       { return nil }
func (p *appBlockingProcess) Kill() error {
	p.killed.Store(true)
	p.pr.CloseWithError(io.ErrClosedPipe)
	return nil
}

type appFakeFactory struct {
	mu      sync.Mutex
	procs   []logs.Process
	started []logs.Process
}

func (f *appFakeFactory) Start(ctx context.Context, name string, args ...string) (logs.Process, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.procs) == 0 {
		return appEmptyProcess{}, nil
	}
	p := f.procs[0]
	f.procs = f.procs[1:]
	f.started = append(f.started, p)
	return p, nil
}

func (f *appFakeFactory) startCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.started)
}

type appEmptyProcess struct{}

func (appEmptyProcess) Stdout() io.Reader { return strings.NewReader("") }
func (appEmptyProcess) Wait() error       { return nil }
func (appEmptyProcess) Kill() error       { return nil }

// eventuallyApp retries fn until it returns true or the deadline elapses.
func eventuallyApp(t *testing.T, fn func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return false
}

// TestNewJournalLogSource_DelegatesNextAndHistory verifies that the journal
// adapter surfaces entries through LogSource.Next and the bounded history
// through LogSource.History, mirroring the tailer adapter.
func TestNewJournalLogSource_DelegatesNextAndHistory(t *testing.T) {
	const line = `{"__CURSOR":"s=1;i=1","MESSAGE":"journal line","PRIORITY":"6"}`
	history := &appFakeProcess{out: strings.NewReader(line + "\n")}
	factory := &appFakeFactory{procs: []logs.Process{history}}
	js := logs.NewJournalSourceWithFactory(logs.JournalOptions{Unit: "caddy.service"}, factory)
	defer js.Close()
	src := NewJournalLogSource(js)

	var entries []logs.Entry
	deadline := time.Now().Add(5 * time.Second)
	for len(entries) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no entries surfaced through the adapter within the deadline")
		}
		got, err := src.Next(context.Background())
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		entries = append(entries, got...)
		time.Sleep(2 * time.Millisecond)
	}
	if got, want := string(entries[0].Raw), "journal line"; got != want {
		t.Errorf("Raw = %q, want %q", got, want)
	}
	if entries[0].Metadata["PRIORITY"] != "6" {
		t.Errorf("Metadata = %v, want PRIORITY=6", entries[0].Metadata)
	}
	hist := src.History()
	if len(hist) == 0 || string(hist[0].Raw) != "journal line" {
		t.Errorf("History = %+v, want the journal entry", hist)
	}
}

// TestLogSourceFunc_CloseDelegates verifies that Close calls CloseFn and
// returns its error unchanged.
func TestLogSourceFunc_CloseDelegates(t *testing.T) {
	wantErr := errors.New("close boom")
	called := false
	src := LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return nil },
		CloseFn: func() error {
			called = true
			return wantErr
		},
	}
	if err := src.Close(); !errors.Is(err, wantErr) {
		t.Fatalf("Close = %v, want %v", err, wantErr)
	}
	if !called {
		t.Error("CloseFn was not called")
	}
}

// TestLogSourceFunc_CloseNoOp verifies that a LogSourceFunc without CloseFn
// closes as a no-op returning nil.
func TestLogSourceFunc_CloseNoOp(t *testing.T) {
	src := LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return nil },
	}
	if err := src.Close(); err != nil {
		t.Errorf("Close = %v, want nil (no CloseFn set)", err)
	}
}

// TestNewLogSource_CloseDelegatesToTailer verifies that the tailer adapter's
// Close delegates to the Tailer and is safe to call twice, and that the
// source remains usable afterwards (the Tailer re-opens on the next Next).
func TestNewLogSource_CloseDelegatesToTailer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	if err := os.WriteFile(path, []byte("first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tailer := logs.NewTailer(logs.Options{Path: path})
	src := NewLogSource(tailer)
	if _, err := src.Next(context.Background()); err != nil {
		t.Fatalf("Next: %v", err)
	}
	if err := src.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := src.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	// The delegated close closed the handle; the next Next re-opens the
	// file (Tailer semantics), so the adapter stays usable.
	if f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		_, _ = f.WriteString("second\n")
		_ = f.Close()
	}
	if _, err := src.Next(context.Background()); err != nil {
		t.Fatalf("Next after Close: %v", err)
	}
}

// TestNewJournalLogSource_Close verifies that the journal adapter's Close
// closes the underlying JournalSource: the running follow process is killed,
// Next afterwards returns the documented closed error, and a second Close is
// safe.
func TestNewJournalLogSource_Close(t *testing.T) {
	const line = `{"__CURSOR":"s=1;i=1","MESSAGE":"journal line"}`
	history := &appFakeProcess{out: strings.NewReader(line + "\n")}
	follow := newAppBlockingProcess()
	factory := &appFakeFactory{procs: []logs.Process{history, follow}}
	js := logs.NewJournalSourceWithFactory(logs.JournalOptions{Unit: "caddy.service"}, factory)
	src := NewJournalLogSource(js)

	// Wait until the follow process is running so Close has a process to
	// kill deterministically.
	if !eventuallyApp(t, func() bool { return factory.startCount() >= 2 }) {
		t.Fatal("the follow process was never started")
	}
	if err := src.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !follow.killed.Load() {
		t.Error("Close did not kill the running follow process")
	}
	if _, err := src.Next(context.Background()); !errors.Is(err, logs.ErrClosed) {
		t.Fatalf("Next after Close = %v, want logs.ErrClosed", err)
	}
	if err := src.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
