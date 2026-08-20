package runtime

import (
	"encoding/json"
)

// Upstream is one reverse_proxy upstream as observed in the loaded
// config. It is read-only runtime state: lazycaddy never pings the
// upstream itself and never infers health beyond what the config and
// the Admin API expose.
type Upstream struct {
	// Address is the dial address (e.g. "backend:8080" or "127.0.0.1:3000").
	Address string
	// Server identifies the http server that owns the upstream (the key
	// under apps.http.servers).
	Server string
	// RouteIndex is the index of the route inside the server that
	// contains the reverse_proxy handler; -1 when the upstream was found
	// outside the usual route shape.
	RouteIndex int
	// HealthCheck holds the active/passive thresholds configured for the
	// upstream's parent handler, when configured. A nil check means the
	// Caddyfile used no explicit health_check block and the defaults
	// apply.
	HealthCheck *HealthCheck
	// Live holds the current runtime health when the Admin API exposes
	// it (GET /reverse_proxy/upstreams). Nil when the endpoint is not
	// available or the Caddy build does not expose it.
	Live *UpstreamLive
}

// UpstreamLive is the live runtime state for one upstream as exposed
// by the Admin API. All fields are observed, not inferred.
type UpstreamLive struct {
	Healthy   *bool `json:"healthy"`
	Fails     *int  `json:"fails"`
	Active    *int  `json:"active"`
	Available *bool `json:"available"`
}

// HealthCheck mirrors the configured health_check block for a
// reverse_proxy handler. Only the thresholds are surfaced — the panel
// labels the health as "observed runtime state" rather than a generic
// network probe result.
type HealthCheck struct {
	// Active holds active health-check settings when the handler enables
	// them (e.g. uri, interval, timeout, expect_status, max_fails).
	Active map[string]any `json:"active,omitempty"`
	// Passive holds passive (circuit-breaker) settings when enabled.
	Passive map[string]any `json:"passive,omitempty"`
	// Raw is the original JSON for display when the shape is not fully
	// understood; it is the same bytes as Active/Passive but kept for
	// the detail view.
	Raw json.RawMessage `json:"-"`
}

// ParseUpstreams extracts reverse_proxy upstreams from a loaded Caddy
// JSON config (the body returned by GET /config/). It walks
// apps.http.servers.*.routes recursively (including subroutes) and
// collects every handler whose handler == "reverse_proxy" or
// "reverse_proxy/..." and returns its upstreams slice. The function is
// tolerant: missing apps, servers or routes yield an empty slice, never
// an error, unless the JSON itself is malformed. Dynamic upstreams
// (those without a dial, e.g. from an SRV or A/AAAA lookup) are skipped
// in the static list and surface only through the live endpoint.
func ParseUpstreams(cfg []byte) ([]Upstream, error) {
	if len(cfg) == 0 {
		return nil, nil
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(cfg, &root); err != nil {
		return nil, err
	}
	appsRaw, ok := root["apps"]
	if !ok {
		return nil, nil
	}
	var apps map[string]json.RawMessage
	if err := json.Unmarshal(appsRaw, &apps); err != nil {
		return nil, nil
	}
	httpRaw, ok := apps["http"]
	if !ok {
		return nil, nil
	}
	var httpApp struct {
		Servers map[string]json.RawMessage `json:"servers"`
	}
	if err := json.Unmarshal(httpRaw, &httpApp); err != nil {
		return nil, nil
	}
	var upstreams []Upstream
	for serverName, srvRaw := range httpApp.Servers {
		var srv struct {
			Routes []json.RawMessage `json:"routes"`
		}
		if err := json.Unmarshal(srvRaw, &srv); err != nil {
			continue
		}
		collectUpstreams(serverName, srv.Routes, 0, &upstreams)
	}
	return upstreams, nil
}

func collectUpstreams(serverName string, routes []json.RawMessage, depth int, out *[]Upstream) {
	for ri, routeRaw := range routes {
		var route struct {
			Handle []json.RawMessage `json:"handle"`
			Group  string            `json:"group"`
		}
		if err := json.Unmarshal(routeRaw, &route); err != nil {
			continue
		}
		for _, hRaw := range route.Handle {
			var h struct {
				Handler     string            `json:"handler"`
				Upstreams   []upstreamEntry   `json:"upstreams"`
				HealthCheck json.RawMessage   `json:"health_checks"`
				Routes      []json.RawMessage `json:"routes"`
			}
			if err := json.Unmarshal(hRaw, &h); err != nil {
				continue
			}
			// Recurse into subroutes (e.g. subroute handler) before handling
			// the current handler's own upstreams.
			if len(h.Routes) > 0 {
				collectUpstreams(serverName, h.Routes, depth+1, out)
			}
			if h.Handler != "reverse_proxy" && h.Handler != "reverse_proxy/1" {
				if len(h.Handler) < 13 || h.Handler[:13] != "reverse_proxy" {
					continue
				}
			}
			var hc *HealthCheck
			if len(h.HealthCheck) > 0 && string(h.HealthCheck) != "null" {
				var tmp HealthCheck
				if err := json.Unmarshal(h.HealthCheck, &tmp); err == nil {
					tmp.Raw = h.HealthCheck
					hc = &tmp
				} else {
					hc = &HealthCheck{Raw: h.HealthCheck}
				}
			}
			for _, u := range h.Upstreams {
				if u.Dial == "" {
					continue
				}
				*out = append(*out, Upstream{
					Address:     u.Dial,
					Server:      serverName,
					RouteIndex:  ri,
					HealthCheck: hc,
				})
			}
		}
	}
}

// EnrichUpstreamsWithLive merges the live endpoint payload (from GET
// /reverse_proxy/upstreams) into the static list. The live payload is
// expected to be a JSON object or array that contains address-keyed
// entries with healthy/fails/active/available fields. When the payload
// cannot be understood it is ignored and the static list is returned
// unchanged so the dashboard stays available.
func EnrichUpstreamsWithLive(static []Upstream, liveBody []byte) []Upstream {
	if len(liveBody) == 0 {
		return static
	}
	liveMap := make(map[string]UpstreamLive)
	var arr []map[string]any
	if err := json.Unmarshal(liveBody, &arr); err == nil {
		for _, m := range arr {
			if addr, ok := m["address"].(string); ok && addr != "" {
				liveMap[addr] = liveFromMap(m)
			} else if addr, ok := m["dial"].(string); ok && addr != "" {
				liveMap[addr] = liveFromMap(m)
			}
		}
	} else {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(liveBody, &obj); err == nil {
			for _, v := range obj {
				var m map[string]any
				if err := json.Unmarshal(v, &m); err != nil {
					continue
				}
				if addr, ok := m["address"].(string); ok && addr != "" {
					liveMap[addr] = liveFromMap(m)
				} else if addr, ok := m["dial"].(string); ok && addr != "" {
					liveMap[addr] = liveFromMap(m)
				}
			}
		}
	}
	if len(liveMap) == 0 {
		return static
	}
	out := make([]Upstream, len(static))
	copy(out, static)
	seen := make(map[string]bool, len(static))
	for _, u := range static {
		seen[u.Address] = true
	}
	for i, u := range out {
		if live, ok := liveMap[u.Address]; ok {
			out[i].Live = &live
		}
	}
	for addr, live := range liveMap {
		if !seen[addr] {
			out = append(out, Upstream{Address: addr, Server: "dynamic", RouteIndex: -1, Live: &live})
		}
	}
	return out
}

func liveFromMap(m map[string]any) UpstreamLive {
	var l UpstreamLive
	if v, ok := m["healthy"].(bool); ok {
		l.Healthy = &v
	}
	if v, ok := m["fails"].(float64); ok {
		n := int(v)
		l.Fails = &n
	}
	if v, ok := m["active"].(float64); ok {
		n := int(v)
		l.Active = &n
	}
	if v, ok := m["available"].(bool); ok {
		l.Available = &v
	}
	if v, ok := m["num_requests"].(float64); ok && l.Active == nil {
		n := int(v)
		l.Active = &n
	}
	return l
}

type upstreamEntry struct {
	Dial string `json:"dial"`
}
