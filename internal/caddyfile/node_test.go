package caddyfile

import "testing"

func TestKindString(t *testing.T) {
	tests := []struct {
		kind Kind
		want string
	}{
		{KindGlobalOptions, "global-options"},
		{KindSnippet, "snippet"},
		{KindSite, "site"},
		{KindNamedRoute, "named-route"},
		{KindDirective, "directive"},
		{Kind(99), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.kind.String(); got != tt.want {
			t.Errorf("Kind(%d).String() = %q, want %q", tt.kind, got, tt.want)
		}
	}
}

func TestNodeIsDirective(t *testing.T) {
	directive := Node{Kind: KindDirective, Name: "reverse_proxy"}
	if !directive.IsDirective("reverse_proxy") {
		t.Fatal("directive was not recognized by name")
	}
	if directive.IsDirective("file_server") {
		t.Fatal("directive matched a different name")
	}

	for _, kind := range []Kind{KindGlobalOptions, KindSnippet, KindSite, KindNamedRoute} {
		if (Node{Kind: kind, Name: "reverse_proxy"}).IsDirective("reverse_proxy") {
			t.Errorf("kind %q was incorrectly recognized as a directive", kind)
		}
	}
}

func TestWalkNodesVisitsParentsBeforeChildren(t *testing.T) {
	nodes := []Node{
		{
			Kind: KindSite,
			Name: "example.com",
			Children: []Node{
				{Kind: KindDirective, Name: "root"},
				{Kind: KindDirective, Name: "file_server"},
			},
		},
		{Kind: KindDirective, Name: "servers"},
	}

	var got []string
	walkNodes(nodes, func(n Node) { got = append(got, n.Name) })

	want := []string{"example.com", "root", "file_server", "servers"}
	if len(got) != len(want) {
		t.Fatalf("visited %v nodes, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("visited[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
