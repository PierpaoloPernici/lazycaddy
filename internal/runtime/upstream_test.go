package runtime

import (
	"testing"
)

func TestParseUpstreams_Empty(t *testing.T) {
	ups, err := ParseUpstreams(nil)
	if err != nil || len(ups) != 0 {
		t.Fatalf("empty = %v %v, want nil/empty", ups, err)
	}
	ups, err = ParseUpstreams([]byte(`{}`))
	if err != nil || len(ups) != 0 {
		t.Fatalf("empty object = %v %v", ups, err)
	}
}

func TestParseUpstreams_MalformedJSON(t *testing.T) {
	_, err := ParseUpstreams([]byte(`{invalid`))
	if err == nil {
		t.Error("expected error for malformed JSON")
	}
}

func TestParseUpstreams_NoHTTPApp(t *testing.T) {
	ups, err := ParseUpstreams([]byte(`{"apps":{"tls":{}}}`))
	if err != nil || len(ups) != 0 {
		t.Fatalf("no http = %v %v", ups, err)
	}
}

func TestParseUpstreams_MultipleServers(t *testing.T) {
	cfg := []byte(`{"apps":{"http":{"servers":{"srv0":{"routes":[{"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"a:80"},{"dial":"b:80"}]}]}]},"srv1":{"routes":[{"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"c:80"}]}]}]}}}}}`)
	ups, err := ParseUpstreams(cfg)
	if err != nil {
		t.Fatalf("ParseUpstreams: %v", err)
	}
	if len(ups) != 3 {
		t.Fatalf("got %d upstreams, want 3", len(ups))
	}
	addrs := map[string]bool{}
	for _, u := range ups {
		addrs[u.Address] = true
	}
	for _, want := range []string{"a:80", "b:80", "c:80"} {
		if !addrs[want] {
			t.Errorf("missing %s", want)
		}
	}
}

func TestParseUpstreams_HealthCheckParsing(t *testing.T) {
	cfg := []byte(`{"apps":{"http":{"servers":{"srv0":{"routes":[{"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"x:8080"}],"health_checks":{"active":{"uri":"/health","interval":"10s"},"passive":{"max_fails":3}}}]}]}}}}}`)
	ups, err := ParseUpstreams(cfg)
	if err != nil || len(ups) != 1 {
		t.Fatalf("ParseUpstreams: %v %d", err, len(ups))
	}
	if ups[0].HealthCheck == nil || ups[0].HealthCheck.Active["uri"] != "/health" {
		t.Errorf("health check not parsed: %+v", ups[0].HealthCheck)
	}
}

func TestParseUpstreams_IgnoresNonReverseProxy(t *testing.T) {
	cfg := []byte(`{"apps":{"http":{"servers":{"srv0":{"routes":[{"handle":[{"handler":"static_response","upstreams":[{"dial":"ignored"}]}]}]}}}}}`)
	ups, _ := ParseUpstreams(cfg)
	if len(ups) != 0 {
		t.Errorf("got %d, want 0", len(ups))
	}
}

func TestParseUpstreams_EmptyDialIgnored(t *testing.T) {
	cfg := []byte(`{"apps":{"http":{"servers":{"srv0":{"routes":[{"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":""}]}]}]}}}}}`)
	ups, _ := ParseUpstreams(cfg)
	if len(ups) != 0 {
		t.Errorf("empty dial should be ignored, got %d", len(ups))
	}
}
