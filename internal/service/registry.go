package service

import (
	"sort"
	"sync"
)

// globalRegistry is the default registry populated by each service's init().
var globalRegistry = NewRegistry()

// Register adds a service to the global registry. Called from init() in each
// service package.
func Register(svc Service) {
	globalRegistry.Register(svc)
}

// Services returns every service registered in the global registry.
func Services() []Service {
	return globalRegistry.All()
}

// Registry manages service registration and discovery.
type Registry struct {
	mu       sync.RWMutex
	services map[string]Service
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{services: make(map[string]Service)}
}

// Register adds (or replaces) a service by name.
func (r *Registry) Register(svc Service) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.services[svc.Name()] = svc
}

// Get returns a service by name.
func (r *Registry) Get(name string) (Service, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	svc, ok := r.services[name]

	return svc, ok
}

// All returns every registered service.
func (r *Registry) All() []Service {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]Service, 0, len(r.services))
	for _, svc := range r.services {
		out = append(out, svc)
	}

	// Sort by state (descending) so Behavioral wins collisions over ContractPassing,
	// then by name (ascending) for determinism.
	sort.Slice(out, func(i, j int) bool {
		var si, sj ImplState
		if d, ok := out[i].(Describer); ok {
			si = d.Meta().State
		}
		if d, ok := out[j].(Describer); ok {
			sj = d.Meta().State
		}
		if si != sj {
			return si > sj
		}
		return out[i].Name() < out[j].Name()
	})

	return out
}

// Names returns the names of every registered service.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.services))
	for name := range r.services {
		names = append(names, name)
	}

	return names
}
