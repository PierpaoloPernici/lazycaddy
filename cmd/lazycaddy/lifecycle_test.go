package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/logs"
)

// errSentinelRun is the sentinel Run failure injected into runTUI for the
// error-path test. bubbletea v1.3.10 has no message-to-error mechanism
// (Run only fails on TTY/panic/kill paths), so the injected run function is
// the deterministic way to exercise the error path; the success path below
// drives a real headless tea.Program.
var errSentinelRun = errors.New("sentinel TUI failure")

// stubModel is a minimal headless Bubble Tea model whose Init returns the
// given command.
type stubModel struct{ initCmd tea.Cmd }

func (m stubModel) Init() tea.Cmd                       { return m.initCmd }
func (m stubModel) Update(tea.Msg) (tea.Model, tea.Cmd) { return m, nil }
func (m stubModel) View() string                        { return "" }

// closingLogSource returns a LogSource whose Close is tracked through a
// counter, so tests can assert the shutdown path closes it exactly once.
func closingLogSource(closeErr error) (app.LogSource, *int) {
	closes := 0
	src := app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return nil },
		CloseFn: func() error {
			closes++
			return closeErr
		},
	}
	return src, &closes
}

// newQuitProgram returns a headless real tea.Program whose Init quits
// immediately, so Run returns nil without a terminal.
func newQuitProgram() *tea.Program {
	return tea.NewProgram(stubModel{initCmd: tea.Quit},
		tea.WithOutput(io.Discard),
		tea.WithInput(strings.NewReader("")),
	)
}

// TestRunTUI_ClosesLogSourceOnSuccess drives a real headless tea.Program to
// a clean quit and verifies the source is closed exactly once.
func TestRunTUI_ClosesLogSourceOnSuccess(t *testing.T) {
	src, closes := closingLogSource(nil)
	monitor, monitorCloses := closingMonitor()
	program := newQuitProgram()
	if err := runTUI(func() (tea.Model, error) { return program.Run() }, src, monitor); err != nil {
		t.Fatalf("runTUI: %v", err)
	}
	if *closes != 1 {
		t.Fatalf("log source closed %d times, want exactly 1", *closes)
	}
	if *monitorCloses != 1 {
		t.Fatalf("change monitor closed %d times, want exactly 1", *monitorCloses)
	}
}

// closingMonitor returns an app.ChangeMonitor whose Close increments the
// returned counter, modeling the fsnotify watcher release.
func closingMonitor() (app.ChangeMonitor, *int) {
	closes := new(int)
	return &countingMonitor{closes: closes}, closes
}

// countingMonitor is a minimal app.ChangeMonitor that only counts Close
// calls; Next blocks forever so a leaked watch goroutine would hang the
// test instead of silently passing.
type countingMonitor struct {
	closes *int
}

func (c *countingMonitor) Update([]app.ChangeTarget) error { return nil }
func (c *countingMonitor) Next(context.Context) (app.ExternalChange, error) {
	select {}
}
func (c *countingMonitor) Close() error {
	*c.closes++
	return nil
}

// TestRunTUI_ClosesLogSourceOnError verifies the shutdown path still closes
// the log source when Run fails. bubbletea v1.3.10 cannot make Run fail via
// a model message, so the helper's run function is injected with a sentinel
// error (documented fallback).
func TestRunTUI_ClosesLogSourceOnError(t *testing.T) {
	src, closes := closingLogSource(nil)
	monitor, monitorCloses := closingMonitor()
	err := runTUI(func() (tea.Model, error) { return nil, errSentinelRun }, src, monitor)
	if !errors.Is(err, errSentinelRun) {
		t.Fatalf("runTUI = %v, want the sentinel run error", err)
	}
	if *closes != 1 {
		t.Fatalf("log source closed %d times, want exactly 1", *closes)
	}
	if *monitorCloses != 1 {
		t.Fatalf("change monitor closed %d times, want exactly 1", *monitorCloses)
	}
}

// TestRunTUI_NilLogSource verifies runTUI works without a configured log
// source (the default; the log view is then disabled).
func TestRunTUI_NilLogSource(t *testing.T) {
	program := newQuitProgram()
	if err := runTUI(func() (tea.Model, error) { return program.Run() }, nil, nil); err != nil {
		t.Fatalf("runTUI: %v", err)
	}
}

// TestRunTUI_CloseErrorNeverMasksOrFails verifies the best-effort contract:
// a Close error must not mask a Run error nor turn a successful run into a
// failure.
func TestRunTUI_CloseErrorNeverMasksOrFails(t *testing.T) {
	closeErr := errors.New("close failed")

	// Run fails and Close fails: the run error is returned, not masked.
	src, _ := closingLogSource(closeErr)
	if err := runTUI(func() (tea.Model, error) { return nil, errSentinelRun }, src, nil); !errors.Is(err, errSentinelRun) {
		t.Fatalf("runTUI = %v, want the sentinel run error (Close must not mask it)", err)
	}

	// Run succeeds and Close fails: the run still succeeds.
	src2, _ := closingLogSource(closeErr)
	program := newQuitProgram()
	if err := runTUI(func() (tea.Model, error) { return program.Run() }, src2, nil); err != nil {
		t.Fatalf("runTUI with failing Close = %v, want nil (Close must not fail a successful run)", err)
	}
}
