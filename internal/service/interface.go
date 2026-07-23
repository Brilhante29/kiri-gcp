// Package service provides the interfaces and utilities shared by all GCP
// service emulations.
package service

import "net/http"

// Service is the common interface implemented by every emulated GCP service.
type Service interface {
	// Name returns the short service name, e.g. "storage", "pubsub", "billing".
	Name() string

	// RegisterRoutes registers the service's REST routes on the router.
	RegisterRoutes(r Router)
}

// Router registers HTTP handlers. It is a thin abstraction over the standard
// library http.ServeMux so services never import the concrete server package.
//
// Method-and-pattern routing follows Go 1.22 semantics: the pattern may include
// path wildcards ("{bucket}") and a trailing multi-segment wildcard
// ("{name...}"), retrievable in handlers via r.PathValue.
type Router interface {
	// Handle registers handler for the given HTTP method and path pattern.
	Handle(method, pattern string, handler http.HandlerFunc)
}

// FidelityClass represents how faithfully a module is implemented.
type FidelityClass int

const (
	FidelityUnset FidelityClass = 0
	FidelityD     FidelityClass = 1 // D — lifecycle, metadata, async ops, projected costs
	FidelityC     FidelityClass = 2 // C — SDK works, resources persist, deterministic responses
	FidelityB     FidelityClass = 3 // B — full control plane + core data plane operations
	FidelityA     FidelityClass = 4 // A — API, behavior, integrations, failures, persistence, cost
)

// ImplState tracks the implementation maturity of a module.
type ImplState int

const (
	StateDeclared           ImplState = 0 // Only registered
	StateGenerated          ImplState = 1 // Contract stubs generated
	StateContractPassing    ImplState = 2 // Contract tests pass
	StateBehavioral         ImplState = 3 // Behavior implemented
	StateIntegrated         ImplState = 4 // Cross-service integrations
	StateDifferentialVerify ImplState = 5 // Verified against real GCP
)

// Label returns a human-readable label for the fidelity class.
func (f FidelityClass) Label() string {
	switch f {
	case FidelityA:
		return "A"
	case FidelityB:
		return "B"
	case FidelityC:
		return "C"
	case FidelityD:
		return "D"
	default:
		return "—"
	}
}

// Label returns a human-readable label for the implementation state.
func (s ImplState) Label() string {
	switch s {
	case StateDeclared:
		return "Declared"
	case StateGenerated:
		return "Generated"
	case StateContractPassing:
		return "Contract"
	case StateBehavioral:
		return "Behavioral"
	case StateIntegrated:
		return "Integrated"
	case StateDifferentialVerify:
		return "DiffVerify"
	default:
		return "—"
	}
}

// Meta describes a service for the README service catalog. Meta() is the single
// source of truth consumed by cmd/readme-gen and verified in tests.
type Meta struct {
	// Display is the human-readable name, e.g. "Cloud Storage".
	Display string
	// Category is the catalog section, e.g. "Storage".
	Category string
	// Description is a one-line summary shown next to the service.
	Description string
	// Fidelity is the implementation class (A=highest, D=lowest).
	Fidelity FidelityClass
	// State is the implementation maturity.
	State ImplState
}

// Describer is implemented by services that expose catalog metadata. Every
// registered service must implement it.
type Describer interface {
	Meta() Meta
}
