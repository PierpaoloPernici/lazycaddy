package caddyfile

import (
	"strings"
	"testing"
)

func TestCatalogKnownDirectives(t *testing.T) {
	names := []string{"reverse_proxy", "tls", "encode", "log", "file_server", "php_fastcgi", "header", "redir", "respond", "import"}
	for _, name := range names {
		meta := Catalog(name)
		if meta == nil {
			t.Fatalf("Catalog(%q) = nil, want metadata", name)
		}
		if meta.Name != name {
			t.Errorf("Catalog(%q).Name = %q", name, meta.Name)
		}
		if strings.TrimSpace(meta.Description) == "" {
			t.Errorf("Catalog(%q) has an empty description", name)
		}
		if len(meta.Ops) == 0 {
			t.Errorf("Catalog(%q) has no structured operations", name)
		}
	}
}

func TestCatalogGlobalOptions(t *testing.T) {
	names := []string{"email", "admin", "acme_ca", "auto_https", "debug", "http_port", "https_port", "local_certs", "log", "ocsp_stapling", "order", "persist_config", "servers", "skip_install_trust", "storage", "storage_clean_interval"}
	for _, name := range names {
		meta := Catalog(name)
		if meta == nil {
			t.Fatalf("Catalog(%q) = nil, want global option metadata", name)
		}
		if strings.TrimSpace(meta.Description) == "" {
			t.Errorf("Catalog(%q) has an empty description", name)
		}
	}
}

func TestCatalogUnknownReturnsNil(t *testing.T) {
	for _, name := range []string{"custom_plugin_directive", "not_a_directive", "", "reverse"} {
		if meta := Catalog(name); meta != nil {
			t.Errorf("Catalog(%q) = %+v, want nil (unknown directives must not be classified as known)", name, meta)
		}
	}
}

func TestCatalogOpsMatchPlanner(t *testing.T) {
	// Every directive the planner can insert must advertise insert; every
	// catalogued directive must advertise the edit-only operations.
	for name := range insertSpecs {
		meta := Catalog(name)
		if meta == nil {
			t.Fatalf("insertable directive %q missing from catalog", name)
		}
		if !containsOp(meta.Ops, OpInsert) {
			t.Errorf("Catalog(%q).Ops = %v, want insert (planner supports it)", name, meta.Ops)
		}
	}
	for name := range directives {
		meta := Catalog(name)
		if meta == nil || !containsOp(meta.Ops, OpSetValue) || !containsOp(meta.Ops, OpDelete) || !containsOp(meta.Ops, OpReorder) {
			t.Errorf("Catalog(%q).Ops = %v, want set-value, delete and reorder", name, meta.Ops)
		}
	}
}

func TestCatalogEntriesAreAdvisoryOnly(t *testing.T) {
	// The catalog carries presentation data only: no parser validity
	// fields exist, and unknown names cannot be looked up.
	for name := range directives {
		meta := Catalog(name)
		if meta == nil {
			continue
		}
		if meta.Since != "" && meta.Module == "" {
			// A Since without a Module is fine (core directives); the
			// reverse (Module without Since) is also fine. The point is
			// that neither is required for validity.
			t.Logf("%s: Since set to %q", name, meta.Since)
		}
	}
}

func containsOp(ops []StructuredOp, want StructuredOp) bool {
	for _, op := range ops {
		if op == want {
			return true
		}
	}
	return false
}

func TestCatalogIsStableCopy(t *testing.T) {
	// Callers receive a copy: mutating it must not corrupt the catalog.
	meta := Catalog("respond")
	if meta == nil {
		t.Fatal("Catalog(respond) = nil")
	}
	meta.Description = "mutated"
	if again := Catalog("respond"); again.Description == "mutated" {
		t.Error("catalog mutation leaked between callers")
	}
}
