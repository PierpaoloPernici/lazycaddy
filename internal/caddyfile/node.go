package caddyfile

// Kind classifies a parsed source node.
type Kind int

const (
	// KindGlobalOptions is the top-level global options block: `{ ... }`.
	KindGlobalOptions Kind = iota
	// KindSnippet is a reusable snippet: `(name) { ... }`.
	KindSnippet
	// KindSite is a site block: `<addresses> { ... }`, or the single
	// brace-less site that spans the rest of the file.
	KindSite
	// KindNamedRoute is a reusable named route: `&(name) { ... }`.
	KindNamedRoute
	// KindDirective is a directive line, with or without a nested block.
	// Unknown or plugin directives are also KindDirective and stay opaque:
	// their raw source is preserved and never reinterpreted.
	KindDirective
)

func (k Kind) String() string {
	switch k {
	case KindGlobalOptions:
		return "global-options"
	case KindSnippet:
		return "snippet"
	case KindSite:
		return "site"
	case KindNamedRoute:
		return "named-route"
	case KindDirective:
		return "directive"
	default:
		return "unknown"
	}
}

// Node is one structural unit of a Caddyfile document. It always carries the
// exact byte range it occupies in the original source, so a patch can target
// it without touching unrelated bytes.
type Node struct {
	Kind Kind
	// Name is the snippet name for KindSnippet, the address header for
	// KindSite, or the directive name for KindDirective.
	Name string
	// Args is the raw argument text for KindDirective (everything after the
	// directive name, trimmed). Empty for other kinds.
	Args string
	// Range locates the node in the original source, including leading
	// indentation and the trailing newline of the node's last line.
	Range SourceRange
	// Children are the directives nested inside a block node.
	Children []Node
}

// IsDirective reports whether n is a directive with the given name.
func (n Node) IsDirective(name string) bool {
	return n.Kind == KindDirective && n.Name == name
}
