package app

import "context"

// Clipboard copies exact bytes to the operator's clipboard. Implementations
// may use a terminal protocol or a local platform command; callers should not
// assume that the clipboard is available on every host.
type Clipboard interface {
	Copy(ctx context.Context, content []byte) error
}

// ClipboardFunc adapts a function to the Clipboard interface.
type ClipboardFunc func(context.Context, []byte) error

// Copy implements Clipboard.
func (f ClipboardFunc) Copy(ctx context.Context, content []byte) error {
	return f(ctx, content)
}
