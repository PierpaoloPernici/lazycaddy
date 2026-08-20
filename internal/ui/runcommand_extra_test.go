package ui

import "testing"

func TestRunCommand_All(t *testing.T) {
	m := &Model{}
	ids := []commandID{
		commandMoveSelection, commandToggleBranch, commandExpandAll, commandCollapseAll,
		commandMatcherNext, commandReviewInline, commandValidate, commandDiff, commandSave,
		commandReload, commandLogs, commandRuntime, commandTLS, commandLogFollow, commandLogFilter,
		commandLogClearFilter, commandLogPause, commandLogDetail, commandEdit, commandFullEdit,
		commandAdd, commandNew, commandReorder, commandEditForm, commandDelete, commandBackups,
		commandErrors, commandCopy, commandSelectText, commandSearch, commandHelp, commandQuit, commandPalette,
	}
	for _, id := range ids {
		_, _ = m.runCommand(id)
	}
}

func TestCommandDefinitions_Coverage(t *testing.T) {
	defs := commandDefinitions()
	if len(defs) < 30 {
		t.Errorf("expected at least 30 commands, got %d", len(defs))
	}
	// Ensure each command has at least one key
	for _, c := range defs {
		if len(c.Keys) == 0 {
			t.Errorf("command %s has no keys", c.ID)
		}
	}
}
