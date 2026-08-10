package app

import "context"

// Browser opens an external web URL for the operator. The UI depends on this
// boundary rather than on a platform command or shell invocation.
type Browser interface {
	OpenURL(ctx context.Context, url string) error
}

// BrowserFunc adapts a function to the Browser interface.
type BrowserFunc func(context.Context, string) error

// OpenURL implements Browser.
func (f BrowserFunc) OpenURL(ctx context.Context, url string) error {
	return f(ctx, url)
}
