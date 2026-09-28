package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// TestControlClient_FromEndpoint_UsesCurrentHostPortToken verifies a client
// built from an EndpointSource sends requests to the published host/port with
// the published bearer token, rather than any value captured at construction
// (there is nothing to capture -- NewControlClientFromEndpoint takes no
// host/port/token at all).
func TestControlClient_FromEndpoint_UsesCurrentHostPortToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	host, port := splitTestServerHostPort(t, srv)
	ep := NewEndpoint()
	ep.Publish(host, port, "token-1", 111)

	c := NewControlClientFromEndpoint(ep, newTestLogger())
	if err := c.Health(context.Background()); err != nil {
		t.Fatalf("Health: %v", err)
	}
	if gotAuth != "Bearer token-1" {
		t.Errorf("Authorization = %q, want Bearer token-1", gotAuth)
	}
	if c.AuthToken() != "token-1" {
		t.Errorf("AuthToken() = %q, want token-1", c.AuthToken())
	}
}

// TestControlClient_FromEndpoint_SendsNewTokenToNewPortAfterPublish covers
// work order 01's acceptance condition: after a second Publish, the
// standalone control client sends the new token to the new port on its very
// next request, with no reconstruction.
func TestControlClient_FromEndpoint_SendsNewTokenToNewPortAfterPublish(t *testing.T) {
	var mu sync.Mutex
	var gotAuth1, gotAuth2 []string
	newHandler := func(dst *[]string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			*dst = append(*dst, r.Header.Get("Authorization"))
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
		}
	}

	srv1 := httptest.NewServer(newHandler(&gotAuth1))
	t.Cleanup(srv1.Close)
	srv2 := httptest.NewServer(newHandler(&gotAuth2))
	t.Cleanup(srv2.Close)

	host1, port1 := splitTestServerHostPort(t, srv1)
	host2, port2 := splitTestServerHostPort(t, srv2)

	ep := NewEndpoint()
	ep.Publish(host1, port1, "token-1", 111)

	c := NewControlClientFromEndpoint(ep, newTestLogger())
	if err := c.Health(context.Background()); err != nil {
		t.Fatalf("Health before relaunch: %v", err)
	}

	ep.Publish(host2, port2, "token-2", 222)

	if err := c.Health(context.Background()); err != nil {
		t.Fatalf("Health after relaunch: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(gotAuth1) != 1 || gotAuth1[0] != "Bearer token-1" {
		t.Fatalf("server1 saw %v, want exactly one Bearer token-1 request", gotAuth1)
	}
	if len(gotAuth2) != 1 {
		t.Fatalf("server2 saw %d requests, want exactly 1 (the post-relaunch request)", len(gotAuth2))
	}
	if gotAuth2[0] != "Bearer token-2" {
		t.Errorf("post-relaunch Authorization = %q, want Bearer token-2", gotAuth2[0])
	}
}

// TestControlClient_FromEndpoint_CurrentNeverMixesGenerationsUnderRace runs
// concurrent requests against a client built from an endpoint while another
// goroutine keeps publishing new identities, so -race can catch a torn read
// of the endpoint's current identity from inside resolveBaseURL/AuthToken.
func TestControlClient_FromEndpoint_CurrentNeverMixesGenerationsUnderRace(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	host, port := splitTestServerHostPort(t, srv)
	ep := NewEndpoint()
	ep.Publish(host, port, "token-0", 0)

	c := NewControlClientFromEndpoint(ep, newTestLogger())

	const publishers = 4
	const readers = 8
	const iterations = 50

	var wg sync.WaitGroup
	wg.Add(publishers + readers)

	for p := 0; p < publishers; p++ {
		go func(p int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				ep.Publish(host, port, "token", p*1000+i)
			}
		}(p)
	}

	for r := 0; r < readers; r++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				if err := c.Health(context.Background()); err != nil {
					t.Errorf("Health: %v", err)
					return
				}
			}
		}()
	}

	wg.Wait()
}
