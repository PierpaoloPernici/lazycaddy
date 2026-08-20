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
// apps.http.servers.*.routes[].handle[] and collects every handler
// whose handler == "reverse_proxy" or "reverse_proxy/..." and returns
// its upstreams slice. The function is tolerant: missing apps, servers
// or routes yield an empty slice, never an error, unless the JSON
// itself is malformed.
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
		for ri, routeRaw := range srv.Routes {
			var route struct {
				Handle []json.RawMessage `json:"handle"`
			}
			if err := json.Unmarshal(routeRaw, &route); err != nil {
				continue
			}
			for _, hRaw := range route.Handle {
				var h struct {
					Handler     string          `json:"handler"`
					Upstreams   []upstreamEntry `json:"upstreams"`
					HealthCheck json.RawMessage `json:"health_checks"`
				}
				if err := json.Unmarshal(hRaw, &h); err != nil {
					continue
				}
				if h.Handler != "reverse_proxy" && h.Handler != "reverse_proxy/1" {
					// Caddy's adapted JSON sometimes prefixes the handler
					// with the module path; be permissive.
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
					upstreams = append(upstreams, Upstream{
						Address:     u.Dial,
						Server:      serverName,
						RouteIndex:  ri,
						HealthCheck: hc,
					})
				}
			}
		}
	}
	return upstreams, nil
}

type upstreamEntry struct {
	Dial string `json:"dial"`
}
