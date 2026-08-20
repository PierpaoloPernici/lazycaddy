package runtime

import (
	"testing"
)

func TestEnrichUpstreamsWithLive_Array(t *testing.T) {
	static := []Upstream{{Address: "a:80", Server: "srv0"}, {Address: "b:80", Server: "srv0"}}
	liveBody := []byte(`[{"address":"a:80","fails":3,"num_requests":5},{"address":"b:80","healthy":true,"available":true}]`)
	enriched := EnrichUpstreamsWithLive(static, liveBody)
	if len(enriched) != 2 {
		t.Fatalf("got %d, want 2", len(enriched))
	}
	if enriched[0].Live == nil || enriched[0].Live.Fails == nil || *enriched[0].Live.Fails != 3 {
		t.Errorf("a:80 live fails not enriched: %+v", enriched[0].Live)
	}
	if enriched[0].Live.Active == nil || *enriched[0].Live.Active != 5 {
		t.Errorf("a:80 active not from num_requests: %+v", enriched[0].Live)
	}
	if enriched[1].Live == nil || enriched[1].Live.Healthy == nil || !*enriched[1].Live.Healthy {
		t.Errorf("b:80 healthy not enriched")
	}
}

func TestEnrichUpstreamsWithLive_ObjectAndDial(t *testing.T) {
	static := []Upstream{{Address: "x:80"}}
	liveBody := []byte(`{"up1":{"address":"x:80","fails":1},"up2":{"dial":"y:80","active":2}}`)
	enriched := EnrichUpstreamsWithLive(static, liveBody)
	if len(enriched) != 2 {
		t.Fatalf("got %d, want 2 (one static enriched + one dynamic)", len(enriched))
	}
	foundY := false
	for _, u := range enriched {
		if u.Address == "y:80" && u.Server == "dynamic" && u.Live != nil && u.Live.Active != nil && *u.Live.Active == 2 {
			foundY = true
		}
	}
	if !foundY {
		t.Errorf("dynamic y:80 not added: %+v", enriched)
	}
}

func TestEnrichUpstreamsWithLive_EmptyStaticButLiveDynamic(t *testing.T) {
	static := []Upstream{}
	liveBody := []byte(`[{"address":"dyn:80","fails":1}]`)
	enriched := EnrichUpstreamsWithLive(static, liveBody)
	if len(enriched) != 1 || enriched[0].Address != "dyn:80" {
		t.Fatalf("empty static with live dynamic should add, got %+v", enriched)
	}
}

func TestEnrichUpstreamsWithLive_EmptyLive(t *testing.T) {
	static := []Upstream{{Address: "a:80"}}
	if got := EnrichUpstreamsWithLive(static, nil); len(got) != 1 {
		t.Errorf("empty live should return static")
	}
	if got := EnrichUpstreamsWithLive(static, []byte(`not json`)); len(got) != 1 {
		t.Errorf("invalid live json should return static")
	}
}

func TestLiveFromMap_AllFields(t *testing.T) {
	m := map[string]any{"healthy": true, "fails": float64(2), "active": float64(3), "available": false, "num_requests": float64(99)}
	l := liveFromMap(m)
	if l.Healthy == nil || !*l.Healthy {
		t.Error("healthy")
	}
	if l.Fails == nil || *l.Fails != 2 {
		t.Error("fails")
	}
	if l.Active == nil || *l.Active != 3 {
		t.Error("active should be 3, not num_requests")
	}
	if l.Available == nil || *l.Available {
		t.Error("available")
	}
	// num_requests fallback when active missing
	m2 := map[string]any{"num_requests": float64(7)}
	l2 := liveFromMap(m2)
	if l2.Active == nil || *l2.Active != 7 {
		t.Error("num_requests fallback")
	}
}

func TestParseUpstreams_SubrouteRecursive(t *testing.T) {
	cfg := []byte(`{"apps":{"http":{"servers":{"srv0":{"routes":[{"handle":[{"handler":"subroute","routes":[{"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"inner:80"}]}]}]}]}]}}}}}`)
	ups, err := ParseUpstreams(cfg)
	if err != nil {
		t.Fatalf("ParseUpstreams: %v", err)
	}
	if len(ups) != 1 || ups[0].Address != "inner:80" {
		t.Fatalf("recursive subroute not parsed: %+v", ups)
	}
}

func TestParseUpstreams_DynamicSkipped(t *testing.T) {
	cfg := []byte(`{"apps":{"http":{"servers":{"srv0":{"routes":[{"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":""}]}]}]}}}}}`)
	ups, _ := ParseUpstreams(cfg)
	if len(ups) != 0 {
		t.Errorf("empty dial should be skipped")
	}
}
