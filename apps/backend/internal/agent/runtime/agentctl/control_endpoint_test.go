package client

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
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
// of the endpoint's current identity from inside pinIdentity/AuthToken.
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

// TestControlClient_FromEndpoint_RequestDuringRecoveringFailsFastNotQueued
// covers the first half of AC-PLATFORM-AGENT-RUNTIME-AVAILABILITY-002.11: a
// request sent while the endpoint still points at the exited process --
// during `recovering`, before any replacement has been published as current
// -- fails with its existing error surface instead of hanging, silently
// retrying, or queuing until a replacement appears.
func TestControlClient_FromEndpoint_RequestDuringRecoveringFailsFastNotQueued(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	host, port := splitTestServerHostPort(t, srv)
	srv.Close() // simulate the process having already exited

	ep := NewEndpoint()
	ep.Publish(host, port, "token-1", 111)

	c := NewControlClientFromEndpoint(ep, newTestLogger())

	start := time.Now()
	err := c.Health(context.Background())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Health = nil error, want a connection failure while the endpoint points at the exited process")
	}
	// The client's own http.Client.Timeout is 30s; a failure that took anywhere
	// near that would indicate an added retry loop rather than the single
	// dial-and-fail this AC requires.
	if elapsed > 5*time.Second {
		t.Fatalf("Health took %v to fail, want a fast single-attempt failure (no automatic retry/queueing)", elapsed)
	}
}

// TestControlClient_FromEndpoint_RequestAfterReplacementPublishedReachesNewServer
// covers the second half of AC-PLATFORM-AGENT-RUNTIME-AVAILABILITY-002.11: a
// request sent after the replacement becomes current reaches the
// replacement, even though nothing here clears any `recovering` UI state --
// the client has no notion of that notice, only of the endpoint's current
// identity.
func TestControlClient_FromEndpoint_RequestAfterReplacementPublishedReachesNewServer(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	deadHost, deadPort := splitTestServerHostPort(t, dead)
	dead.Close()

	var replacementHits int
	replacement := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		replacementHits++
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(replacement.Close)
	replacementHost, replacementPort := splitTestServerHostPort(t, replacement)

	ep := NewEndpoint()
	ep.Publish(deadHost, deadPort, "token-1", 111)

	c := NewControlClientFromEndpoint(ep, newTestLogger())

	if err := c.Health(context.Background()); err == nil {
		t.Fatal("Health against the exited process = nil error, want a connection failure")
	}

	ep.Publish(replacementHost, replacementPort, "token-2", 222)

	if err := c.Health(context.Background()); err != nil {
		t.Fatalf("Health after the replacement was published: %v", err)
	}
	if replacementHits != 1 {
		t.Fatalf("replacement server saw %d requests, want exactly 1", replacementHits)
	}
}

// TestControlClient_FromEndpoint_PinIdentityKeepsHostPortAndTokenFromSameRead
// covers RV2-5: pinIdentity must capture host/port and the bearer token from
// a single EndpointSource.Current() read, so a relaunch landing between
// "resolve the base URL" and "send the request" can never pair one
// generation's host:port with a different generation's token. It calls
// pinIdentity directly (this test is in the same package) to capture the
// identity for a request against server1/token-1, then publishes a relaunch
// to server2/token-2 before the request is actually dispatched, and asserts
// server1 still received the pinned token-1 -- proving the token used at
// dispatch time came from the same read as the URL, not a second,
// independent Current() call made later inside RoundTrip.
func TestControlClient_FromEndpoint_PinIdentityKeepsHostPortAndTokenFromSameRead(t *testing.T) {
	var gotAuth1 []string
	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth1 = append(gotAuth1, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv1.Close)

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv2.Close)

	host1, port1 := splitTestServerHostPort(t, srv1)
	host2, port2 := splitTestServerHostPort(t, srv2)

	ep := NewEndpoint()
	ep.Publish(host1, port1, "token-1", 111)

	c := NewControlClientFromEndpoint(ep, newTestLogger())

	ctx, baseURL := c.pinIdentity(context.Background())
	if want := fmt.Sprintf("http://%s:%d", host1, port1); baseURL != want {
		t.Fatalf("pinIdentity baseURL = %q, want %q", baseURL, want)
	}

	// Simulate a relaunch landing after the identity is pinned but before the
	// request built from it is dispatched.
	ep.Publish(host2, port2, "token-2", 222)

	req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/health", nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()

	if len(gotAuth1) != 1 || gotAuth1[0] != "Bearer token-1" {
		t.Fatalf("server1 saw %v, want exactly one Bearer token-1 request (the pinned identity's own token)", gotAuth1)
	}
}
