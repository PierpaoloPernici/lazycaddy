package app

import (
	"context"
	"testing"

	appRuntime "github.com/PierpaoloPernici/lazycaddy/internal/runtime"
)

func TestClipboardFunc_Delegates(t *testing.T) {
	ctx := context.WithValue(context.Background(), struct{}{}, "value")
	want := []byte("exact bytes")
	var gotCtx context.Context
	var gotContent []byte

	fn := ClipboardFunc(func(got context.Context, content []byte) error {
		gotCtx = got
		gotContent = content
		return nil
	})
	if err := fn.Copy(ctx, want); err != nil {
		t.Fatalf("Copy: %v", err)
	}
	if gotCtx != ctx {
		t.Fatal("Copy did not forward the context")
	}
	if string(gotContent) != string(want) {
		t.Fatalf("content = %q, want %q", gotContent, want)
	}
}

func TestRuntimeStatusFunc_Delegates(t *testing.T) {
	want := appRuntime.Report{
		Status: appRuntime.StatusRunning,
		Capabilities: appRuntime.Capabilities{
			Binary:   true,
			Version:  "v2.11.4",
			Reload:   true,
			Writable: true,
		},
	}
	fn := RuntimeStatusFunc(func(context.Context) appRuntime.Report { return want })

	got := fn.Probe(context.Background())
	if got != want {
		t.Fatalf("Probe() = %+v, want %+v", got, want)
	}
}
