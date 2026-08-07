package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/PierpaoloPernici/lazycaddy/internal/runtime"
)

// fakeAdmin is a programmable adminLoader that records the exact config
// bytes it was asked to post.
type fakeAdmin struct {
	got   []byte
	err   error
	calls int
}

func (f *fakeAdmin) LoadJSON(ctx context.Context, config []byte) error {
	f.calls++
	f.got = config
	return f.err
}

// fakeAdapter is a programmable caddyAdapter that records the path it was
// asked to adapt.
type fakeAdapter struct {
	path  string
	out   []byte
	err   error
	calls int
}

func (f *fakeAdapter) Adapt(ctx context.Context, path string) ([]byte, error) {
	f.calls++
	f.path = path
	return f.out, f.err
}

// TestReloader_Success verifies the happy path: conflict check passes,
// the adapter runs against the real path, its output is posted verbatim,
// and the result carries the endpoint and a fresh timestamp.
func TestReloader_Success(t *testing.T) {
	const endpoint = "http://localhost:2019"
	path := "/etc/caddy/Caddyfile"
	saved := []byte("example.test {\n\trespond ok\n}\n")
	adapted := []byte(`{"apps":{}}`)

	admin := &fakeAdmin{}
	adapter := &fakeAdapter{out: adapted}
	r := NewReloader(endpoint, admin, adapter, func(p string) ([]byte, error) { return saved, nil })

	res, err := r.Reload(context.Background(), path, saved)
	if err != nil {
		t.Fatalf("Reload: unexpected error: %v", err)
	}
	if res.Endpoint != endpoint {
		t.Errorf("Endpoint = %q, want %q", res.Endpoint, endpoint)
	}
	if res.LoadedAt.IsZero() {
		t.Error("LoadedAt is zero, want a timestamp")
	}
	if adapter.calls != 1 {
		t.Errorf("adapter.calls = %d, want 1", adapter.calls)
	}
	if adapter.path != path {
		t.Errorf("adapter.path = %q, want %q", adapter.path, path)
	}
	if admin.calls != 1 {
		t.Errorf("admin.calls = %d, want 1", admin.calls)
	}
	if !bytes.Equal(admin.got, adapted) {
		t.Errorf("admin got %q, want the adapted JSON %q", admin.got, adapted)
	}
}

// TestReloader_ConflictOnDisk verifies that an external edit between save
// and reload aborts with ErrConflict and nothing is adapted or posted.
func TestReloader_ConflictOnDisk(t *testing.T) {
	path := "/etc/caddy/Caddyfile"
	saved := []byte("original")
	current := []byte("edited externally")

	admin := &fakeAdmin{}
	adapter := &fakeAdapter{}
	r := NewReloader("http://localhost:2019", admin, adapter, func(p string) ([]byte, error) { return current, nil })

	_, err := r.Reload(context.Background(), path, saved)
	var reloadErr *ReloadError
	if !errors.As(err, &reloadErr) {
		t.Fatalf("err = %v, want *ReloadError", err)
	}
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	if adapter.calls != 0 {
		t.Errorf("adapter.calls = %d, want 0 (nothing should be adapted)", adapter.calls)
	}
	if admin.calls != 0 {
		t.Errorf("admin.calls = %d, want 0 (nothing should be sent)", admin.calls)
	}
}

// TestReloader_ConflictOnReadError verifies that a read failure is treated
// as a conflict: the file cannot be proven unchanged, so nothing is sent.
func TestReloader_ConflictOnReadError(t *testing.T) {
	path := "/etc/caddy/Caddyfile"
	saved := []byte("original")

	admin := &fakeAdmin{}
	adapter := &fakeAdapter{}
	r := NewReloader("http://localhost:2019", admin, adapter, func(p string) ([]byte, error) {
		return nil, errors.New("no such file")
	})

	_, err := r.Reload(context.Background(), path, saved)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	if adapter.calls != 0 || admin.calls != 0 {
		t.Errorf("expected no adapt/admin calls, got adapter=%d admin=%d", adapter.calls, admin.calls)
	}
}

// TestReloader_AdaptError verifies that an adapt failure is wrapped as-is
// (no sentinel) and the admin client is never reached.
func TestReloader_AdaptError(t *testing.T) {
	path := "/etc/caddy/Caddyfile"
	saved := []byte("original")
	boom := errors.New("adapt failed: line 3")

	admin := &fakeAdmin{}
	adapter := &fakeAdapter{err: boom}
	r := NewReloader("http://localhost:2019", admin, adapter, func(p string) ([]byte, error) { return saved, nil })

	_, err := r.Reload(context.Background(), path, saved)
	var reloadErr *ReloadError
	if !errors.As(err, &reloadErr) {
		t.Fatalf("err = %v, want *ReloadError", err)
	}
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the adapt error wrapped", err)
	}
	if errors.Is(err, ErrConflict) || errors.Is(err, ErrAdminRejected) {
		t.Errorf("adapt failures must not be mapped to sentinels, got %v", err)
	}
	if admin.calls != 0 {
		t.Errorf("admin.calls = %d, want 0 (adapt failed before sending)", admin.calls)
	}
}

// TestReloader_AdminUnreachable verifies that the runtime unreachable
// sentinel maps to the app-level ErrAdminUnreachable.
func TestReloader_AdminUnreachable(t *testing.T) {
	path := "/etc/caddy/Caddyfile"
	saved := []byte("original")

	admin := &fakeAdmin{err: fmt.Errorf("%w: connect refused", runtime.ErrAdminUnreachable)}
	adapter := &fakeAdapter{out: []byte(`{"apps":{}}`)}
	r := NewReloader("http://localhost:2019", admin, adapter, func(p string) ([]byte, error) { return saved, nil })

	_, err := r.Reload(context.Background(), path, saved)
	if !errors.Is(err, ErrAdminUnreachable) {
		t.Fatalf("err = %v, want ErrAdminUnreachable", err)
	}
}

// TestReloader_AdminTimeout verifies that the runtime timeout sentinel
// maps to the app-level ErrAdminTimeout.
func TestReloader_AdminTimeout(t *testing.T) {
	path := "/etc/caddy/Caddyfile"
	saved := []byte("original")

	admin := &fakeAdmin{err: fmt.Errorf("%w: read timeout", runtime.ErrAdminTimeout)}
	adapter := &fakeAdapter{out: []byte(`{"apps":{}}`)}
	r := NewReloader("http://localhost:2019", admin, adapter, func(p string) ([]byte, error) { return saved, nil })

	_, err := r.Reload(context.Background(), path, saved)
	if !errors.Is(err, ErrAdminTimeout) {
		t.Fatalf("err = %v, want ErrAdminTimeout", err)
	}
}

// TestReloader_AdminRejected verifies that an unrecognised admin error
// maps to the app-level ErrAdminRejected.
func TestReloader_AdminRejected(t *testing.T) {
	path := "/etc/caddy/Caddyfile"
	saved := []byte("original")

	admin := &fakeAdmin{err: errors.New("admin API rejected the configuration: 400 bad config")}
	adapter := &fakeAdapter{out: []byte(`{"apps":{}}`)}
	r := NewReloader("http://localhost:2019", admin, adapter, func(p string) ([]byte, error) { return saved, nil })

	_, err := r.Reload(context.Background(), path, saved)
	if !errors.Is(err, ErrAdminRejected) {
		t.Fatalf("err = %v, want ErrAdminRejected", err)
	}
}

// TestReloader_Cancelled verifies that a cancelled reload passes
// context.Canceled through and is never misclassified as a rejected
// configuration.
func TestReloader_Cancelled(t *testing.T) {
	path := "/etc/caddy/Caddyfile"
	saved := []byte("original")

	admin := &fakeAdmin{err: context.Canceled}
	adapter := &fakeAdapter{out: []byte(`{"apps":{}}`)}
	r := NewReloader("http://localhost:2019", admin, adapter, func(p string) ([]byte, error) { return saved, nil })

	_, err := r.Reload(context.Background(), path, saved)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if errors.Is(err, ErrAdminRejected) {
		t.Errorf("cancellation must not be classified as ErrAdminRejected, got %v", err)
	}
}
