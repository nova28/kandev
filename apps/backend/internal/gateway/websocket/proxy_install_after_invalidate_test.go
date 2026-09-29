package websocket

import (
	"net/http"
	"net/http/httputil"
	"testing"
)

// TestVscodeInstallProxyDiscardsResultAfterInvalidateDuringResolve covers
// RV3-1: a resolveProxy build that started before InvalidateProxy ran for its
// session must not land in the cache once it finishes, even though the build
// itself succeeded. Before this fix, installProxy unconditionally cached
// whatever resolveProxy built, so a stale-generation retirement racing an
// in-flight resolve could resurrect a proxy pointed at a dead agentctl right
// after invalidation cleared it -- and that entry would never self-heal,
// since a stale target's response is a normal HTTP status, not a transport
// error, so ReverseProxy.ErrorHandler never fires to evict it.
func TestVscodeInstallProxyDiscardsResultAfterInvalidateDuringResolve(t *testing.T) {
	log, _ := observedTerminalLogger(t)
	handler := NewVscodeProxyHandler(newProxyTestManager(t, log), log)

	epoch := handler.beginResolve("sess-race")

	// Simulate a relaunch (or an unrelated request's upstream failure)
	// invalidating this session while the build above is still in flight.
	handler.InvalidateProxy("sess-race")

	built := &httputil.ReverseProxy{}
	proxy, installed := handler.installProxy("sess-race", built, "http://dead:1", epoch)
	if installed {
		t.Fatal("installProxy() installed = true, want false for a build resolved after invalidation")
	}
	if proxy != nil {
		t.Fatalf("installProxy() proxy = %v, want nil when discarded", proxy)
	}

	if _, cached := handler.cachedProxy("sess-race"); cached {
		t.Fatal("cachedProxy() found an entry, want none: the stale build must never reach the cache")
	}
}

// TestVscodeResolveProxyDiscardsResultInvalidatedDuringUpstreamStatusCall is
// the end-to-end version: invalidation happens for real, mid-flight, inside
// the upstream VS Code status round trip that resolveProxy's slow path
// blocks on, driven entirely through HandleVscodeProxy rather than by
// reaching into unexported fields.
func TestVscodeResolveProxyDiscardsResultInvalidatedDuringUpstreamStatusCall(t *testing.T) {
	log, _ := observedTerminalLogger(t)
	manager := newProxyTestManager(t, log)
	handler := NewVscodeProxyHandler(manager, log)

	var invalidateOnce bool
	upstream := newFakeAgentctl(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == vscodeStatusPath {
			if !invalidateOnce {
				invalidateOnce = true
				// Fires while resolveProxy is blocked waiting on this exact
				// response -- after beginResolve snapshotted the epoch, before
				// installProxy checks it.
				handler.InvalidateProxy("sess-race-e2e")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"running","port":8443,"url":"http://127.0.0.1:8443"}`))
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("code-server:" + r.URL.Path))
	})
	addExecutionForURL(t, manager, "sess-race-e2e", upstream.server.URL, "", log)
	gateway := serveGateway(t, newVscodeEngine(handler))

	got := gateway(http.MethodGet, "/vscode/sess-race-e2e/")
	if got.status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d for a build invalidated mid-resolve", got.status, http.StatusServiceUnavailable)
	}

	handler.mu.Lock()
	defer handler.mu.Unlock()
	if len(handler.proxies) != 0 {
		t.Fatalf("cached proxies = %d, want 0: the invalidated build must never install", len(handler.proxies))
	}
}

// TestPortProxyInstallProxyDiscardsResultAfterInvalidateDuringResolve is the
// PortProxyHandler counterpart, invalidated via invalidateProxy (the single
// cache-key path used by proxy.ErrorHandler).
func TestPortProxyInstallProxyDiscardsResultAfterInvalidateDuringResolve(t *testing.T) {
	log, _ := observedTerminalLogger(t)
	handler := NewPortProxyHandler(newProxyTestManager(t, log), log)

	cacheKey := "sess-race:3000"
	epoch := handler.beginResolve(cacheKey)

	handler.invalidateProxy(cacheKey)

	built := &httputil.ReverseProxy{}
	proxy, installed := handler.installProxy(cacheKey, built, "http://dead:1", epoch)
	if installed {
		t.Fatal("installProxy() installed = true, want false for a build resolved after invalidation")
	}
	if proxy != nil {
		t.Fatalf("installProxy() proxy = %v, want nil when discarded", proxy)
	}

	if _, cached := handler.cachedProxy(cacheKey); cached {
		t.Fatal("cachedProxy() found an entry, want none: the stale build must never reach the cache")
	}
}

// TestPortProxyInstallProxyDiscardsResultAfterSessionInvalidateDuringResolve
// covers the InvalidateSession path (wired to the execution-retired hook): a
// relaunch retiring the whole session must invalidate a build in flight for
// one of its ports, not just an already-cached one.
func TestPortProxyInstallProxyDiscardsResultAfterSessionInvalidateDuringResolve(t *testing.T) {
	log, _ := observedTerminalLogger(t)
	handler := NewPortProxyHandler(newProxyTestManager(t, log), log)

	cacheKey := "sess-race:4000"
	epoch := handler.beginResolve(cacheKey)

	handler.InvalidateSession("sess-race")

	built := &httputil.ReverseProxy{}
	proxy, installed := handler.installProxy(cacheKey, built, "http://dead:1", epoch)
	if installed {
		t.Fatal("installProxy() installed = true, want false for a build resolved after a session invalidation")
	}
	if proxy != nil {
		t.Fatalf("installProxy() proxy = %v, want nil when discarded", proxy)
	}
}

// TestVscodeInstallProxyToleratesConcurrentCacheMissBuildsWithoutInvalidation
// pins the pre-existing invariant the epoch check must not break: two
// resolveProxy calls racing on the same session with no invalidation between
// them both succeed, and whichever installs first wins rather than either one
// erroring out.
func TestVscodeInstallProxyToleratesConcurrentCacheMissBuildsWithoutInvalidation(t *testing.T) {
	log, _ := observedTerminalLogger(t)
	handler := NewVscodeProxyHandler(newProxyTestManager(t, log), log)

	epochA := handler.beginResolve("sess-concurrent")
	epochB := handler.beginResolve("sess-concurrent")

	firstBuilt := &httputil.ReverseProxy{Rewrite: func(*httputil.ProxyRequest) {}}
	first, installed := handler.installProxy("sess-concurrent", firstBuilt, "http://first:1", epochA)
	if !installed {
		t.Fatal("installProxy() installed = false for the first concurrent build, want true")
	}
	if first != firstBuilt {
		t.Fatal("installProxy() returned a different proxy for the first installer")
	}

	secondBuilt := &httputil.ReverseProxy{Rewrite: func(*httputil.ProxyRequest) {}}
	second, installed := handler.installProxy("sess-concurrent", secondBuilt, "http://second:1", epochB)
	if !installed {
		t.Fatal("installProxy() installed = false for the second concurrent build, want true")
	}
	if second != firstBuilt {
		t.Fatal("installProxy() for the losing concurrent build must return the winner's proxy, not its own")
	}

	entry, cached := handler.cachedProxy("sess-concurrent")
	if !cached {
		t.Fatal("cachedProxy() found no entry after two successful concurrent installs")
	}
	if entry.target != "http://first:1" {
		t.Fatalf("cached target = %q, want the first installer's target", entry.target)
	}
}

// TestPortProxyBeginResolveCreatesDurableKeyForLaterSessionInvalidate proves
// the durability beginResolve documents: InvalidateSession's prefix scan only
// bumps keys already present in h.epoch, so a cache key's very first resolve
// must write an explicit zero entry -- otherwise a session invalidation
// racing a session's first-ever resolve for that port would silently miss it.
func TestPortProxyBeginResolveCreatesDurableKeyForLaterSessionInvalidate(t *testing.T) {
	log, _ := observedTerminalLogger(t)
	handler := NewPortProxyHandler(newProxyTestManager(t, log), log)

	cacheKey := "sess-durable:5000"
	epoch := handler.beginResolve(cacheKey)
	if epoch != 0 {
		t.Fatalf("beginResolve() epoch = %d, want 0 for a never-invalidated key", epoch)
	}

	handler.mu.Lock()
	_, present := handler.epoch[cacheKey]
	handler.mu.Unlock()
	if !present {
		t.Fatal("beginResolve() left no entry in h.epoch; a same-prefix InvalidateSession could not find this key to bump it")
	}
}
