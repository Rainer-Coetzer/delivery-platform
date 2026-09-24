// Package proxy builds a simple reverse proxy per backend service. The
// gateway is the only thing Flutter clients talk to; every other service
// stays on the Docker network.
package proxy

import (
	"net/http"
	"net/http/httputil"
	"net/url"
)

// New returns a handler that forwards every request it receives to target,
// preserving the path and query string.
func New(target string) (http.Handler, error) {
	u, err := url.Parse(target)
	if err != nil {
		return nil, err
	}
	rp := httputil.NewSingleHostReverseProxy(u)
	orig := rp.Director
	rp.Director = func(r *http.Request) {
		orig(r)
		r.Host = u.Host
	}
	return rp, nil
}
