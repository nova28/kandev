package client

import (
	"sync"
	"sync/atomic"
)

// EndpointIdentity is the immutable identity of the standalone agentctl
// control server this backend currently owns: the host, port, and bearer
// token every consumer must use to reach it, plus the PID for host-local
// liveness records. Generation starts at 1 for the first published identity
// and increments by one on every later Publish (a relaunch).
type EndpointIdentity struct {
	Generation uint64
	Host       string
	Port       int
	Token      string
	PID        int
}

// EndpointSource resolves the current standalone agentctl endpoint. A
// consumer built from a source reads host, port, and token per request
// instead of capturing them at construction, so a relaunch's new identity
// takes effect without reconstructing the consumer.
type EndpointSource interface {
	Current() EndpointIdentity
}

// Endpoint is the single owner of the standalone agentctl control server's
// current identity for this backend process. Only the backend startup path
// and the relaunch supervisor call Publish.
type Endpoint struct {
	current atomic.Pointer[EndpointIdentity]

	// mu serializes Publish and Subscribe so an observer registered while a
	// publish is in flight sees a consistent view: either it is called by
	// that publish (registered before it started notifying) or it is not,
	// and then Subscribe's own catch-up call delivers the identity that
	// publish just committed.
	mu        sync.Mutex
	observers []func(EndpointIdentity)
}

// NewEndpoint creates an Endpoint with no published identity (Generation 0).
func NewEndpoint() *Endpoint {
	e := &Endpoint{}
	e.current.Store(&EndpointIdentity{})
	return e
}

// Current returns the identity currently in effect. Before the first
// Publish this is the zero value (Generation 0).
func (e *Endpoint) Current() EndpointIdentity {
	return *e.current.Load()
}

// Publish stores a new identity with Generation set to one more than the
// previously published generation, then calls every subscribed observer, in
// registration order, with the new identity. Observers run synchronously
// under the internal mutex and must not block, call Publish, or call
// Subscribe.
func (e *Endpoint) Publish(host string, port int, token string, pid int) EndpointIdentity {
	e.mu.Lock()
	defer e.mu.Unlock()

	next := EndpointIdentity{
		Generation: e.current.Load().Generation + 1,
		Host:       host,
		Port:       port,
		Token:      token,
		PID:        pid,
	}
	e.current.Store(&next)
	for _, observe := range e.observers {
		observe(next)
	}
	return next
}

// Subscribe registers an observer called synchronously after each future
// Publish, in registration order. If a generation has already been
// published, the observer is also called once, immediately, with Current()
// before Subscribe returns, so it never misses the identity that was
// current when it registered and never sees generations out of order.
func (e *Endpoint) Subscribe(observe func(EndpointIdentity)) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.observers = append(e.observers, observe)
	if current := e.current.Load(); current.Generation > 0 {
		observe(*current)
	}
}
