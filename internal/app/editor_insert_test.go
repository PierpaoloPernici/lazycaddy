package app

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"testing"
)

func TestEditorPrepareInsert_SeedsTemplateAtOffset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	src := "example.test {\n\trespond ok\n}\n"
	writeFile(t, path, src)

	e := newTestEditor(t)
	doc := sampleDoc(t, path, src)
	pos := len(src)
	session, err := e.PrepareInsert(context.Background(), doc, pos, "# \n")
	if err != nil {
		t.Fatalf("PrepareInsert: %v", err)
	}
	if session.Mode != EditNode {
		t.Errorf("Mode = %v, want EditNode", session.Mode)
	}
	if session.Range.Start != pos || session.Range.End != pos {
		t.Errorf("Range = %+v, want [%d:%d)", session.Range, pos, pos)
	}
	if got := readFileContent(t, session.TempFile); got != "# \n" {
		t.Errorf("temp file = %q, want the seeded comment template", got)
	}
	sidecar := session.SnapshotPath + ".range"
	wantRange := fmt.Sprintf("%d %d\n", pos, pos)
	if got := readFileContent(t, sidecar); got != wantRange {
		t.Errorf("sidecar = %q, want %q", got, wantRange)
	}
	if got := readFileContent(t, session.SnapshotPath); got != src {
		t.Errorf("snapshot = %q, want the full document", got)
	}
}

func TestEditorPrepareInsert_InvalidOffset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	src := "example.test {\n}\n"
	writeFile(t, path, src)
	e := newTestEditor(t)
	doc := sampleDoc(t, path, src)
	if _, err := e.PrepareInsert(context.Background(), doc, len(src)+1, "# \n"); err == nil {
		t.Fatal("PrepareInsert with an out-of-range offset must fail")
	}
	if _, err := e.PrepareInsert(context.Background(), doc, -1, "# \n"); err == nil {
		t.Fatal("PrepareInsert with a negative offset must fail")
	}
}

func TestEditorComplete_InsertResultComposes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	src := "example.test {\n\trespond ok\n}\n"
	writeFile(t, path, src)
	e := newTestEditor(t)
	doc := sampleDoc(t, path, src)
	session, err := e.PrepareInsert(context.Background(), doc, 0, "# \n")
	if err != nil {
		t.Fatalf("PrepareInsert: %v", err)
	}
	edited := "# top comment\n"
	writeFile(t, session.TempFile, edited)
	result, err := e.Complete(context.Background(), session, 0)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if result.Cancelled || !result.Changed {
		t.Fatalf("Cancelled=%v Changed=%v, want a valid changed insert", result.Cancelled, result.Changed)
	}
	want := "# top comment\nexample.test {\n\trespond ok\n}\n"
	if !bytes.Equal(result.Content, []byte(want)) {
		t.Errorf("Content = %q, want %q", result.Content, want)
	}
}

func TestEditorComplete_InsertEmptyCancels(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	src := "example.test {\n}\n"
	writeFile(t, path, src)
	e := newTestEditor(t)
	doc := sampleDoc(t, path, src)
	session, err := e.PrepareInsert(context.Background(), doc, 0, "# \n")
	if err != nil {
		t.Fatalf("PrepareInsert: %v", err)
	}
	writeFile(t, session.TempFile, "")
	result, err := e.Complete(context.Background(), session, 0)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if !result.Cancelled {
		t.Error("an empty insert result must cancel, mirroring EditNode")
	}
}
