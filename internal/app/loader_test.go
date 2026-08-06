package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/PierpaoloPernici/lazycaddy/internal/config"
)

type fakeFS map[string]string

func (fs fakeFS) readFile(path string) ([]byte, error) {
	if src, ok := fs[path]; ok {
		return []byte(src), nil
	}
	return nil, fmt.Errorf("no such file: %s", path)
}

func TestLoadStateResolvesImports(t *testing.T) {
	fs := fakeFS{
		"config/Caddyfile":     "import sites/a.caddy\n",
		"config/sites/a.caddy": "a.example.test {\n\trespond ok\n}\n",
	}
	loader := NewLoader(config.Settings{ConfigPath: "config/Caddyfile", ReadOnly: true}, fs.readFile)
	state, err := loader.LoadState()
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state.Graph == nil {
		t.Fatal("Graph is nil")
	}
	if state.Graph.Err != nil {
		t.Fatalf("Graph.Err = %v", state.Graph.Err)
	}
	if len(state.Graph.Documents) != 2 {
		t.Fatalf("documents = %d, want 2 (root + imported)", len(state.Graph.Documents))
	}
	root, imported := state.Graph.Documents[0], state.Graph.Documents[1]
	if root.Path != "config/Caddyfile" || imported.Path != "config/sites/a.caddy" {
		t.Errorf("document paths = %q, %q", root.Path, imported.Path)
	}
	if len(imported.Nodes) != 1 || imported.Nodes[0].Name != "a.example.test" {
		t.Errorf("imported document nodes = %+v, want one site a.example.test", imported.Nodes)
	}
	if state.Settings.ConfigPath != "config/Caddyfile" || !state.Settings.ReadOnly {
		t.Errorf("settings not preserved: %+v", state.Settings)
	}
}

func TestLoadStateReadError(t *testing.T) {
	fs := fakeFS{}
	loader := NewLoader(config.Settings{ConfigPath: "missing/Caddyfile", ReadOnly: true}, fs.readFile)
	state, err := loader.LoadState()
	if err == nil || !strings.Contains(err.Error(), "missing/Caddyfile") {
		t.Fatalf("err = %v, want a read error naming the path", err)
	}
	if state == nil || state.Graph != nil {
		t.Errorf("state = %+v, want settings with nil graph on read failure", state)
	}
}

func TestLoadStateKeepsParseErrorInGraph(t *testing.T) {
	fs := fakeFS{"config/Caddyfile": "example.test {\n\treverse_proxy localhost:8080\n"}
	loader := NewLoader(config.Settings{ConfigPath: "config/Caddyfile", ReadOnly: true}, fs.readFile)
	state, err := loader.LoadState()
	if err != nil {
		t.Fatalf("read succeeded, LoadState must not return an error: %v", err)
	}
	if state.Graph == nil {
		t.Fatal("Graph is nil, want the partial graph for the raw view")
	}
	if state.Graph.Err == nil {
		t.Errorf("Graph.Err = nil, want the unclosed-block error")
	}
	if state.Graph.Root.Err == nil {
		t.Errorf("root document Err = nil, want the parse error kept on the document")
	}
	if len(state.Graph.Root.Nodes) == 0 {
		t.Errorf("root nodes empty, want the site node preserved for the tree")
	}
}
