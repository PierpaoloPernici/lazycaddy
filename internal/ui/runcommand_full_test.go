package ui

import (
	"context"
	"testing"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/backup"
	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
	"github.com/PierpaoloPernici/lazycaddy/internal/logs"
)

type fakeRollback2 struct{}

func (f *fakeRollback2) ListBackups(srcPath string, docs []*caddyfile.Document) ([]backup.Entry, error) {
	return nil, nil
}
func (f *fakeRollback2) ReadBackup(entry backup.Entry) ([]byte, error) { return nil, nil }
func (f *fakeRollback2) ReadCurrent(path string) ([]byte, error)       { return nil, nil }
func (f *fakeRollback2) Rollback(ctx context.Context, path string, original []byte, backupPath string, docs []*caddyfile.Document) (app.RollbackResult, error) {
	return app.RollbackResult{}, nil
}

func TestRunCommand_FullCoverage(t *testing.T) {
	// Setup a writable state with a site and a directive
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond hello\n\tlog {\n\t\toutput file /tmp/log\n\t}\n}\n",
	}))
	formatter := &fakeFormatter{formatted: []byte("example.test {\n\trespond hello\n}\n")}
	saver := &fakeSaver{}
	reloader := &fakeReloader{}
	clip := &fakeClipboard{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver, reloader, nil, nil, nil, nil, nil, &fakeRollback2{}, nil, clip)
	m = resize(m, 120, 30)
	// Select the log directive for m form
	m.cursor = 2
	// Run all commands with a properly set up model
	ids := []commandID{
		commandMoveSelection, commandToggleBranch, commandExpandAll, commandCollapseAll,
		commandMatcherNext, commandSearch, commandHelp,
		commandValidate, commandReviewInline, commandDiff, commandSave,
		commandEdit, commandFullEdit, commandAdd, commandNew, commandReorder, commandEditForm, commandDelete,
		commandCopy, commandSelectText,
		commandReload, commandRuntime, commandTLS, commandLogs, commandBackups, commandErrors,
		commandLogFollow, commandLogFilter, commandLogClearFilter, commandLogPause, commandLogDetail,
		commandQuit, commandPalette,
	}
	for _, id := range ids {
		_, _ = m.runCommand(id)
	}
	// Also test with showLogs true for log commands
	m.showLogs = true
	m.logSource = app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return nil },
	}
	_, _ = m.runCommand(commandLogFollow)
	_, _ = m.runCommand(commandLogFilter)
	_, _ = m.runCommand(commandLogClearFilter)
	_, _ = m.runCommand(commandLogPause)
	_, _ = m.runCommand(commandLogDetail)
}
