// Package proxy routes public HTTP(S) traffic to workspace containers.
package proxy

import (
	"context"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
)

type upstreamKey struct{}

// Router maps hostnames to "127.0.0.1:<port>" upstreams. Swapping a route is
// atomic: new requests go to the new upstream while in-flight requests finish
// on the old one.
type Router struct {
	mu     sync.RWMutex
	routes map[string]string
	rp     *httputil.ReverseProxy
}

func NewRouter() *Router {
	r := &Router{routes: map[string]string{}}
	r.rp = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			up := pr.In.Context().Value(upstreamKey{}).(string)
			pr.SetURL(&url.URL{Scheme: "http", Host: up})
			pr.Out.Host = pr.In.Host // SetURL rewrites Host; the app needs the public one
			pr.SetXForwarded()
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "502 bad gateway: the app is not responding", http.StatusBadGateway)
		},
	}
	return r
}

func normalize(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.ToLower(strings.TrimSuffix(host, "."))
}

func (r *Router) Set(host, upstream string) {
	r.mu.Lock()
	r.routes[normalize(host)] = upstream
	r.mu.Unlock()
}

func (r *Router) Delete(host string) {
	r.mu.Lock()
	delete(r.routes, normalize(host))
	r.mu.Unlock()
}

func (r *Router) Lookup(host string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	up, ok := r.routes[normalize(host)]
	return up, ok
}

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	up, ok := r.Lookup(req.Host)
	if !ok {
		http.Error(w, "404 page not found", http.StatusNotFound)
		return
	}
	r.rp.ServeHTTP(w, req.WithContext(context.WithValue(req.Context(), upstreamKey{}, up)))
}

// RedirectHandler sends plain HTTP requests to HTTPS.
func RedirectHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://"+normalize(r.Host)+r.URL.RequestURI(), http.StatusPermanentRedirect)
	})
}
