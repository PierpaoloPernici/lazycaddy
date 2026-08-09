package app

import (
	"errors"
	"fmt"
	"testing"

	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
	"github.com/PierpaoloPernici/lazycaddy/internal/config"
	"github.com/PierpaoloPernici/lazycaddy/internal/watch"
)

// The production monitor must satisfy the ChangeMonitor interface
// structurally (type aliases keep the signatures identical), so wiring it
// in main.go needs no adapter.
var _ ChangeMonitor = (*watch.Monitor)(nil)

func TestChangeTargetsMapsGraphDocuments(t *testing.T) {
	readFile := func(path string) ([]byte, error) {
		switch path {
		case "/etc/caddy/Caddyfile":
			return []byte("example.test {\n\timport common.conf\n}\n"), nil
		case "/etc/caddy/common.conf":
			return []byte("# common\n"), nil
		}
		return nil, fmt.Errorf("no such file: %s", path)
	}
	loader := NewLoader(config.Settings{ConfigPath: "/etc/caddy/Caddyfile", ReadOnly: true}, readFile)
	state, err := loader.LoadState()
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state.Graph.Err != nil {
		t.Fatalf("graph error: %v", state.Graph.Err)
	}

	targets := ChangeTargets(state.Graph.Documents)
	if len(targets) != 2 {
		t.Fatalf("targets = %d, want 2 (root + imported)", len(targets))
	}
	if targets[0].Path != "/etc/caddy/Caddyfile" {
		t.Errorf("targets[0].Path = %q, want the root document", targets[0].Path)
	}
	if targets[1].Path != "/etc/caddy/common.conf" {
		t.Errorf("targets[1].Path = %q, want the imported document", targets[1].Path)
	}
	if string(targets[0].Source) != "example.test {\n\timport common.conf\n}\n" {
		t.Errorf("targets[0].Source = %q, want the root source", targets[0].Source)
	}
	if string(targets[1].Source) != "# common\n" {
		t.Errorf("targets[1].Source = %q, want the imported source", targets[1].Source)
	}
}

func TestChangeTargetsSkipsNilDocuments(t *testing.T) {
	docs := []*caddyfile.Document{nil, {Path: "a", Source: []byte("x")}, nil}
	targets := ChangeTargets(docs)
	if len(targets) != 1 || targets[0].Path != "a" {
		t.Fatalf("targets = %+v, want only the non-nil document", targets)
	}
}

func TestChangeSentinelsAliasWatchSentinels(t *testing.T) {
	if !errors.Is(ErrChangeClosed, watch.ErrClosed) {
		t.Fatal("ErrChangeClosed must alias watch.ErrClosed")
	}
	if !errors.Is(ErrChangeBusy, watch.ErrBusy) {
		t.Fatal("ErrChangeBusy must alias watch.ErrBusy")
	}
}
