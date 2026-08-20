package app

import (
	"context"
	"testing"
)

func TestBrowserFunc_OpenURL(t *testing.T) {
	called := false
	f := BrowserFunc(func(ctx context.Context, url string) error {
		called = true
		if url != "https://example.com" {
			t.Errorf("url = %q, want https://example.com", url)
		}
		return nil
	})
	if err := f.OpenURL(context.Background(), "https://example.com"); err != nil || !called {
		t.Errorf("OpenURL failed")
	}
}
