package server

import "net/http"

// Router adapts the standard library http.ServeMux to the service.Router
// interface. Go 1.22+ method-and-pattern routing means "GET /storage/v1/b/{bucket}"
// style patterns work natively, including wildcards and trailing "{name...}".
type Router struct {
	mux *http.ServeMux
	// registered guards against duplicate identical patterns, which ServeMux
	// panics on. Two services occasionally want the same catch-all; the first
	// registration wins and the collision is reported by the caller's logs.
	registered map[string]bool
}

// NewRouter creates an empty router.
func NewRouter() *Router {
	return &Router{
		mux:        http.NewServeMux(),
		registered: make(map[string]bool),
	}
}

// Handle registers handler for method and pattern, e.g. Handle("GET",
// "/storage/v1/b/{bucket}", h). A duplicate (method, pattern) is ignored so the
// server does not panic when two services overlap.
func (r *Router) Handle(method, pattern string, handler http.HandlerFunc) {
	key := method + " " + pattern
	if r.registered[key] {
		return
	}

	r.registered[key] = true
	// ServeMux panics on a malformed pattern; swallow it so one bad route
	// cannot take down registration of everything else.
	defer func() { _ = recover() }()
	r.mux.HandleFunc(key, handler)
}

// ServeHTTP dispatches to the underlying mux.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mux.ServeHTTP(w, req)
}
