package websocket

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/kandev/kandev/internal/agent/runtime/lifecycle"
	"github.com/kandev/kandev/internal/common/logger"
)

// proxyEntry caches both the reverse proxy and its target URL so we can
// detect when the upstream has changed (e.g. VS Code restarted on a new port).
type proxyEntry struct {
	proxy  *httputil.ReverseProxy
	target string // "host:port"
}

// VscodeProxyHandler reverse-proxies HTTP and WebSocket traffic to code-server
// running inside an agentctl instance.
type VscodeProxyHandler struct {
	lifecycleMgr *lifecycle.Manager
	logger       *logger.Logger

	// Cache proxies per session to avoid re-creation
	mu      sync.Mutex
	proxies map[string]*proxyEntry
	// epoch is bumped by InvalidateProxy for a session. resolveProxy's slow
	// path snapshots it before doing any unlocked work and installProxy
	// refuses to cache a result built against a stale snapshot -- see
	// installProxy.
	epoch map[string]uint64
}

// NewVscodeProxyHandler creates a new VS Code proxy handler.
func NewVscodeProxyHandler(lifecycleMgr *lifecycle.Manager, log *logger.Logger) *VscodeProxyHandler {
	return &VscodeProxyHandler{
		lifecycleMgr: lifecycleMgr,
		logger:       log.WithFields(zap.String("component", "vscode-proxy")),
		proxies:      make(map[string]*proxyEntry),
		epoch:        make(map[string]uint64),
	}
}

// HandleVscodeProxy handles all HTTP/WS requests to /vscode/:sessionId/*path.
// It resolves the session to find the agentctl host and VS Code port,
// then reverse-proxies the request to code-server.
func (h *VscodeProxyHandler) HandleVscodeProxy(c *gin.Context) {
	sessionID := c.Param("sessionId")
	if sessionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "sessionId is required"})
		return
	}

	// Per-user scoping (opt-in auth): requireConnectionAuth only proves the
	// caller is authenticated, not that they own this session. This proxy
	// resolves the execution by a bare in-memory lookup (and a per-session
	// cache), so authorize here before anything is served.
	if err := h.lifecycleMgr.CheckSessionExecAccess(c.Request.Context(), sessionID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}

	proxy, err := h.resolveProxy(c, sessionID)
	if err != nil {
		return // error already written to response
	}

	// Strip the /vscode/:sessionId prefix from the request path
	originalPath := c.Request.URL.Path
	prefix := "/vscode/" + sessionID
	newPath := strings.TrimPrefix(originalPath, prefix)
	if newPath == "" || newPath == "/" {
		// Keep a trailing slash for compatibility with older agentctl instances
		// that only register /api/v1/vscode/proxy/*path.
		c.Request.URL.Path = "/api/v1/vscode/proxy/"
	} else {
		c.Request.URL.Path = "/api/v1/vscode/proxy" + newPath
	}
	c.Request.URL.RawPath = ""
	h.logger.Debug("rewrote vscode proxy path",
		zap.String("session_id", sessionID),
		zap.String("original_path", originalPath),
		zap.String("rewritten_path", c.Request.URL.Path),
		zap.String("method", c.Request.Method))
	h.logger.Debug("forwarding vscode proxy request to agentctl",
		zap.String("session_id", sessionID),
		zap.String("upstream_path", c.Request.URL.Path),
		zap.String("method", c.Request.Method))

	// ReverseProxy panics with http.ErrAbortHandler when the client disconnects
	// mid-stream (e.g. closing the VS Code panel). Recover silently to avoid
	// scary stack traces in the logs from Gin's recovery middleware.
	defer func() {
		if r := recover(); r != nil {
			if r == http.ErrAbortHandler {
				h.logger.Debug("vscode proxy: client disconnected", zap.String("session_id", sessionID))
				return
			}
			panic(r) // re-panic for unexpected errors
		}
	}()

	proxy.ServeHTTP(c.Writer, c.Request)
}

// resolveProxy returns a cached proxy if the target hasn't changed, or creates
// a new one. Only performs the upstream status check when no cache exists or
// when the cached target is stale.
//
// The slow path (cache miss) deliberately resolves the execution and builds
// the proxy without holding h.mu: GetExecutionBySessionID can synchronously
// retire a generation-stale execution, whose retirement hook calls back into
// InvalidateProxy on this same handler. Holding h.mu across that call would
// self-deadlock -- the callback re-acquires the same, non-reentrant mutex on
// the same goroutine. h.mu is reacquired only to install the resolved entry.
//
// Because that work runs unlocked, InvalidateProxy can run for this exact
// session while a build is in flight (a relaunch, or another request hitting
// an upstream failure). A late install of that build's result would cache a
// proxy that may already target a dead agentctl -- and it would not
// self-heal, since a 401 or timeout from a merely stale target is a normal
// HTTP response, not a transport error, so ReverseProxy.ErrorHandler never
// runs to evict it. beginResolve snapshots the session's epoch before the
// unlocked work starts; installProxy discards the result if InvalidateProxy
// bumped the epoch in the meantime.
func (h *VscodeProxyHandler) resolveProxy(c *gin.Context, sessionID string) (*httputil.ReverseProxy, error) {
	if entry, cached := h.cachedProxy(sessionID); cached {
		h.logger.Debug("reusing cached vscode proxy",
			zap.String("session_id", sessionID),
			zap.String("target", entry.target))
		return entry.proxy, nil
	}

	epoch := h.beginResolve(sessionID)

	// Slow path: resolve upstream target
	execution, ok := h.lifecycleMgr.GetExecutionBySessionID(sessionID)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found or no active execution"})
		return nil, fmt.Errorf("session not found")
	}

	agentctlClient, releaseClient := execution.AcquireAgentCtlClient()
	defer releaseClient()
	if agentctlClient == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "agentctl client not available"})
		return nil, fmt.Errorf("agentctl client not available")
	}

	vscodeStatus, err := agentctlClient.VscodeStatus(c.Request.Context())
	if err != nil {
		h.logger.Error("failed to get vscode status", zap.Error(err), zap.String("session_id", sessionID))
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "failed to get vscode status"})
		return nil, err
	}
	h.logger.Debug("resolved vscode upstream status",
		zap.String("session_id", sessionID),
		zap.String("status", vscodeStatus.Status),
		zap.Int("port", vscodeStatus.Port),
		zap.String("url", vscodeStatus.URL),
		zap.String("error", vscodeStatus.Error),
		zap.String("message", vscodeStatus.Message))

	if vscodeStatus.Status != "running" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "code-server is not running"})
		return nil, fmt.Errorf("code-server not running")
	}

	baseURL := agentctlClient.BaseURL()
	target, err := url.Parse(baseURL)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "failed to resolve agentctl target"})
		return nil, err
	}

	authToken := agentctlClient.AuthToken()
	built := h.createProxy(sessionID, target, authToken)
	proxy, installed := h.installProxy(sessionID, built, baseURL, epoch)
	if !installed {
		h.logger.Info("discarding vscode proxy resolved after invalidation",
			zap.String("session_id", sessionID))
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "vscode session changed, retry"})
		return nil, fmt.Errorf("vscode proxy invalidated during resolve")
	}

	h.logger.Info("created vscode proxy for session",
		zap.String("session_id", sessionID),
		zap.String("execution_id", execution.ID),
		zap.Stringer("runtime", execution.RuntimeName),
		zap.String("target", baseURL))
	return proxy, nil
}

// cachedProxy returns the cached entry for sessionID, if any.
func (h *VscodeProxyHandler) cachedProxy(sessionID string) (*proxyEntry, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	entry, ok := h.proxies[sessionID]
	return entry, ok
}

// beginResolve snapshots sessionID's current epoch before resolveProxy starts
// its unlocked slow-path work. The snapshot is later checked by installProxy;
// see resolveProxy for why. It always records an explicit zero entry when
// none exists yet, since InvalidateProxy is the only writer that would
// otherwise create the key, and a session that has never resolved must still
// have a key for a same-session invalidation to find and bump.
func (h *VscodeProxyHandler) beginResolve(sessionID string) uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.epoch[sessionID]; !ok {
		h.epoch[sessionID] = 0
	}
	return h.epoch[sessionID]
}

// installProxy stores proxy as sessionID's cached entry, unless
// InvalidateProxy bumped sessionID's epoch since epoch was snapshotted by
// beginResolve -- meaning this build's result may already target a dead
// upstream, so it is discarded rather than cached. When the epoch still
// matches and another caller already installed first, that existing entry
// wins so two concurrent cache-miss callers (no invalidation in between)
// never clobber each other's entry.
func (h *VscodeProxyHandler) installProxy(sessionID string, proxy *httputil.ReverseProxy, baseURL string, epoch uint64) (*httputil.ReverseProxy, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.epoch[sessionID] != epoch {
		return nil, false
	}
	if entry, ok := h.proxies[sessionID]; ok {
		return entry.proxy, true
	}
	h.proxies[sessionID] = &proxyEntry{proxy: proxy, target: baseURL}
	return proxy, true
}

// createProxy builds a reverse proxy for the given target URL.
func (h *VscodeProxyHandler) createProxy(sessionID string, target *url.URL, authToken string) *httputil.ReverseProxy {
	proxy := httputil.NewSingleHostReverseProxy(target)

	// Allow WebSocket upgrades by preserving hop-by-hop headers
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		// Inject agentctl auth token
		if authToken != "" {
			req.Header.Set("Authorization", "Bearer "+authToken)
		}
		// Preserve WebSocket headers that SingleHostReverseProxy strips
		if req.Header.Get("Upgrade") != "" {
			req.Header.Set("Connection", "Upgrade")
		}
	}

	// Handle WebSocket connections
	proxy.ModifyResponse = func(resp *http.Response) error {
		h.logger.Debug("vscode upstream response",
			zap.String("session_id", sessionID),
			zap.Int("status", resp.StatusCode),
			zap.String("request_path", resp.Request.URL.Path))
		// Remove hop-by-hop restriction for WebSocket upgrades
		if resp.StatusCode == http.StatusSwitchingProtocols {
			resp.Header.Set("Connection", "Upgrade")
		}
		return nil
	}

	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		h.logger.Error("vscode proxy error",
			zap.String("session_id", sessionID),
			zap.String("request_path", r.URL.Path),
			zap.Error(err))
		// Remove cached proxy on error so it gets recreated
		h.InvalidateProxy(sessionID)
		http.Error(w, "VS Code proxy error", http.StatusBadGateway)
	}

	return proxy
}

// InvalidateProxy removes a cached proxy for a session (e.g., when VS Code
// stops or a relaunch retires its execution) and bumps its epoch, so a
// resolveProxy build already in flight for this session discards its result
// in installProxy instead of resurrecting a proxy that may target a dead
// upstream.
func (h *VscodeProxyHandler) InvalidateProxy(sessionID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.proxies, sessionID)
	h.epoch[sessionID]++
}
