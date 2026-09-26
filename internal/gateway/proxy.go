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
// URL for now; multiple backends would be a control-plane concern.
func NewBookingProxy(target *url.URL) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.URL.Path = strings.TrimPrefix(req.URL.Path, "/api")
			req.Host = target.Host
			// The tenant key is a gateway credential; backends have no use for
			// it, and forwarding it only widens where it can leak (e.g. logs).
			req.Header.Del(apiKeyHeader)
		},
	}
}
