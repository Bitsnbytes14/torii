package gateway

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// NewBookingProxy returns a reverse proxy to the booking service that
// strips the gateway's "/api" prefix so booking's own routes (/events,
// /bookings) don't need to know about it. The upstream is a single static
// URL for now — service discovery / multiple backends is control-plane
// territory (Phase 3), not this phase.
func NewBookingProxy(target *url.URL) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.URL.Path = strings.TrimPrefix(req.URL.Path, "/api")
			req.Host = target.Host
		},
	}
}
