package backendapp

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	agentruntime "github.com/kandev/kandev/internal/agent/runtime"
	agentctlclient "github.com/kandev/kandev/internal/agent/runtime/agentctl"
	"github.com/kandev/kandev/internal/agent/runtime/agentctl/launcher"
	"github.com/kandev/kandev/internal/common/config"
	"github.com/kandev/kandev/internal/common/logger"
	"github.com/kandev/kandev/internal/common/ownershipperiod"
	"github.com/kandev/kandev/internal/secrets"
	"go.uber.org/zap"
)

// agentctlLauncherResult holds the outputs of provideAgentctlLauncher.
type agentctlLauncherResult struct {
	cleanup    func() error
	binaryPath string
	// recoveryDeadlineStart is the AC-EXECUTORS-SURVIVAL-003.7 recovery
	// deadline's clock start (the instant this launch first contacted a
	// recorded control endpoint). Zero when this launch never contacted one
	// (adoption disabled, no record, or a fresh spawn) -- the lifecycle
	// manager falls back to its own Start-time default in that case.
	recoveryDeadlineStart time.Time
	// inheritedRecordScope is what this launch found at the recorded control
	// endpoint. It governs how recovery classifies recovery-inventory records
	// left by an earlier launch, which cannot be judged against a server this
	// backend started itself.
	inheritedRecordScope agentruntime.InheritedRecordScope
}

// provideAgentctlLauncher starts or adopts the agentctl control server for
// standalone runtime. agentctl is a core service that always runs - it's used
// by the Standalone runtime for agent execution on the host machine.
//
// When the agent-survival capability is enabled (Layer 6.1 of
// agent-survival-across-restart), a surviving control server from a prior
// launch is tried first via agentruntime.AttemptAdoptControlServer; only when
// that fails or there is no record does this spawn a fresh one, same as
// when the capability is disabled. Either way, cfg.Agent.Standalone* is
// updated to point at whichever server this launch ends up using.
func provideAgentctlLauncher(
	ctx context.Context,
	cfg *config.Config,
	log *logger.Logger,
	availability *agentctlclient.Availability,
	endpoint *agentctlclient.Endpoint,
	store agentruntime.AdoptionRecordStore,
	secretStore secrets.SecretStore,
) (*agentctlLauncherResult, error) {
	result, scope := resolveSurvivingAgentctl(ctx, cfg, log, endpoint, store, secretStore)
	if result != nil {
		availability.MarkAvailable()
		return result, nil
	}
	fresh, err := spawnFreshAgentctl(ctx, cfg, log, availability, endpoint, store, secretStore)
	if fresh != nil {
		// The server this launch just started never launched the instances a
		// prior launch recorded, so recovery needs to know what was found at
		// the recorded endpoint rather than judging them against it.
		fresh.inheritedRecordScope = scope
	}
	return fresh, err
}

// resolveSurvivingAgentctl applies the agent-survival capability gate to a
// control server a prior launch left running, and returns non-nil only when
// this launch took that server over. With the capability disabled nothing is
// ever adopted, but a server an earlier survival-enabled launch detached is
// still running with no backend attached to it, so it is stopped here rather
// than left to its own unowned-shutdown timer. Either way a nil return means
// the caller spawns fresh.
func resolveSurvivingAgentctl(
	ctx context.Context,
	cfg *config.Config,
	log *logger.Logger,
	endpoint *agentctlclient.Endpoint,
	store agentruntime.AdoptionRecordStore,
	secretStore secrets.SecretStore,
) (*agentctlLauncherResult, agentruntime.InheritedRecordScope) {
	if !cfg.Features.AgentSurvival {
		agentruntime.ReclaimUnneededControlServer(ctx, store, secretStore, controlClientFactory(log), cfg.ResolvedHomeDir(),
			cfg.Agentctl.RecoveryReadTimeout, cfg.Agentctl.RecoveryReadRetries, log)
		return nil, agentruntime.InheritedRecordScopeNoServer
	}
	return adoptSurvivingAgentctl(ctx, cfg, log, endpoint, store, secretStore)
}

// refusedAdoptionScope maps a refused adoption to what recovery may conclude
// about records an earlier launch left behind. A refusal that never reached
// the server -- nothing answered at the recorded endpoint, or no endpoint was
// recorded -- means no control server holds those instances, so they can be
// probed and repaired. Every refusal that did reach a reachable server leaves
// that server running, and an instance may still be alive on it, so nothing
// may be concluded from its absence from a server this launch started.
func refusedAdoptionScope(outcome agentruntime.AdoptionOutcome) agentruntime.InheritedRecordScope {
	if outcome.ContactedAt.IsZero() {
		return agentruntime.InheritedRecordScopeNoServer
	}
	return agentruntime.InheritedRecordScopeForeignServer
}

// adoptSurvivingAgentctl attempts to adopt a control server recorded by a
// prior launch. Returns nil when adoption did not complete for any reason
// (design 02's "Startup" steps 4-6): the caller then falls back to spawning
// a fresh server exactly as it would with the capability disabled.
func adoptSurvivingAgentctl(
	ctx context.Context,
	cfg *config.Config,
	log *logger.Logger,
	endpoint *agentctlclient.Endpoint,
	store agentruntime.AdoptionRecordStore,
	secretStore secrets.SecretStore,
) (*agentctlLauncherResult, agentruntime.InheritedRecordScope) {
	outcome := agentruntime.AttemptAdoptControlServer(ctx, store, secretStore, controlClientFactory(log),
		cfg.ResolvedHomeDir(), agentruntime.RequiredSurvivalCapabilities,
		cfg.Agentctl.RecoveryReadTimeout, cfg.Agentctl.RecoveryReadRetries, log)
	if !outcome.Adopted {
		log.Info("control server adoption did not complete; spawning a fresh one",
			zap.String("reason", string(outcome.Reason)))
		return nil, refusedAdoptionScope(outcome)
	}

	host, port, err := splitEndpoint(outcome.Endpoint)
	if err != nil {
		log.Warn("adopted control server endpoint is malformed; spawning fresh instead", zap.Error(err))
		return nil, refusedAdoptionScope(outcome)
	}

	log.Info("adopted a surviving control server", zap.String("endpoint", outcome.Endpoint))
	cfg.Agent.StandaloneHost = host
	cfg.Agent.StandalonePort = port
	cfg.Agent.StandaloneAuthToken = outcome.Credential
	// This process never spawned the adopted server, so it has no PID to
	// record here. Standalone liveness for an adopted record is judged by
	// enumeration against the adopted server (design 02, "Persistence"),
	// not by a stale PID probe -- a later layer of this feature.
	cfg.Agent.StandalonePID = 0
	endpoint.Publish(host, port, outcome.Credential, 0)

	// AC-EXECUTORS-CONTROL-OWNERSHIP-003.8: the rotation AttemptAdoptControlServer
	// just performed already counted as the first renewal, but nothing renews
	// ownership again after that without this loop. The renewal cadence is
	// computed from the adopted server's OWN reported unowned period
	// (outcome.UnownedPeriod), never this launch's local config: the two can
	// disagree across a restart that changed agentctl.unownedPeriod, and
	// renewing on a stale/mismatched cadence lets the server's own reaper
	// fire while this backend still believes it owns it (Review round 3,
	// finding 5).
	renewer := startOwnershipRenewal(ctx, log, outcome.Endpoint, outcome.Credential,
		resolveAdoptedRenewalPeriod(outcome.UnownedPeriod))

	return &agentctlLauncherResult{
		// Survival is enabled and this server was adopted, not spawned: the
		// registered cleanup must not stop it (AC-EXECUTORS-SURVIVAL-001.1),
		// and there is no local process for this launcher to own regardless.
		// It must still stop the renewal loop this backend started, or the
		// goroutine leaks past backend shutdown.
		cleanup: func() error {
			if renewer != nil {
				renewer.Stop()
			}
			return nil
		},
		binaryPath:            launcher.FindAgentctlBinary(),
		recoveryDeadlineStart: outcome.ContactedAt,
		inheritedRecordScope:  agentruntime.InheritedRecordScopeAdopted,
	}, agentruntime.InheritedRecordScopeAdopted
}

// spawnFreshAgentctl is today's unconditional launch path. If the
// agent-survival capability is enabled, the freshly spawned server is also
// durably recorded as the installation's control server (failure-table row
// "Own server started after a refused or failed adoption"); recording
// failure is logged and non-fatal to startup, since the next restart simply
// finds no record and spawns fresh again.
//
// When features.agentctlRelaunch is enabled, an unexpected exit is handed to
// an agentctlSupervisor instead of latching the runtime unavailable outright
// (docs/specs/platform/system-design/agent-runtime-availability.md,
// "Relaunch supervisor"): the supervisor owns the current launcher/renewer
// pair from here on, so its Shutdown replaces this function's own stop
// sequence in the returned cleanup.
func spawnFreshAgentctl(
	ctx context.Context,
	cfg *config.Config,
	log *logger.Logger,
	availability *agentctlclient.Availability,
	endpoint *agentctlclient.Endpoint,
	store agentruntime.AdoptionRecordStore,
	secretStore secrets.SecretStore,
) (*agentctlLauncherResult, error) {
	var supervisor *agentctlSupervisor
	onUnexpectedExit := func() {
		availability.MarkUnavailable(agentctlclient.AvailabilityReasonAgentctlExited, time.Now())
	}
	if cfg.Features.AgentctlRelaunch {
		supervisor = buildAgentctlSupervisor(ctx, cfg, log, availability, endpoint, store, secretStore)
		onUnexpectedExit = supervisor.NextExitCallback()
	}

	l, cleanup, err := launcher.Provide(ctx, launcher.Config{
		Host:             cfg.Agent.StandaloneHost,
		Port:             cfg.Agent.StandalonePort,
		StartupConfig:    cfg.ManagedAgentctlStartupConfig(),
		OnUnexpectedExit: onUnexpectedExit,
	}, log)
	if err != nil {
		return nil, err
	}
	availability.MarkAvailable()
	// Update config with the actual port (may differ if fallback was used)
	if actualPort := l.Port(); actualPort != cfg.Agent.StandalonePort {
		log.Info("agentctl port changed from configured value",
			zap.Int("configured_port", cfg.Agent.StandalonePort),
			zap.Int("actual_port", actualPort))
		cfg.Agent.StandalonePort = actualPort
	}
	// Store the per-launch auth token so downstream clients can authenticate.
	cfg.Agent.StandaloneAuthToken = l.AuthToken()
	// Store the agentctl control-server PID so local/standalone executor rows can
	// carry a real host-local liveness handle (executors_running.local_pid).
	cfg.Agent.StandalonePID = l.Pid()
	endpoint.Publish(cfg.Agent.StandaloneHost, cfg.Agent.StandalonePort, l.AuthToken(), l.Pid())

	var renewer *agentruntime.OwnershipRenewer
	if cfg.Features.AgentSurvival {
		hostPort := net.JoinHostPort(cfg.Agent.StandaloneHost, strconv.Itoa(cfg.Agent.StandalonePort))
		client, err := controlClientFactory(log)(hostPort)
		if err != nil {
			log.Warn("failed to build control client for the freshly spawned agentctl; adoption record not written", zap.Error(err))
		} else if err := agentruntime.RecordFreshControlServer(ctx, store, secretStore, client, hostPort, l.AuthToken()); err != nil {
			log.Warn("failed to record freshly spawned control server", zap.Error(err))
		}
		// AC-EXECUTORS-CONTROL-OWNERSHIP-003.8: the bootstrap handshake this
		// launch just completed already counted as the first renewal, but
		// nothing renews ownership again after that without this loop. This
		// launch spawned the server moments ago from this same config, so
		// (unlike the adopted path above) the local resolution IS the value
		// the server itself enforces.
		period, _ := ownershipperiod.Resolve(cfg.Agentctl.UnownedPeriod, cfg.Agentctl.IdleTimeout)
		renewer = startOwnershipRenewal(ctx, log, hostPort, l.AuthToken(), period)
	}

	if supervisor != nil {
		var stopper ownershipStopper
		if renewer != nil {
			stopper = renewer
		}
		supervisor.BindInitialLauncher(l, stopper)
	}

	return &agentctlLauncherResult{
		cleanup: func() error {
			if supervisor != nil {
				supervisor.Shutdown()
				return nil
			}
			if renewer != nil {
				renewer.Stop()
			}
			return cleanup()
		},
		binaryPath: l.BinaryPath(),
	}, nil
}

// buildAgentctlSupervisor constructs the relaunch supervisor for a fresh
// spawn. The record/renewal closures gate on cfg.Features.AgentSurvival
// themselves (newAgentctlSupervisor's contract: the caller decides, not the
// supervisor) and mirror the same RecordFreshControlServer/startOwnershipRenewal
// calls the initial spawn above makes, applied to each later relaunch
// attempt's own launcher instead of the one already bound.
func buildAgentctlSupervisor(
	ctx context.Context,
	cfg *config.Config,
	log *logger.Logger,
	availability *agentctlclient.Availability,
	endpoint *agentctlclient.Endpoint,
	store agentruntime.AdoptionRecordStore,
	secretStore secrets.SecretStore,
) *agentctlSupervisor {
	newLauncher := func(onUnexpectedExit func()) relaunchLauncher {
		return launcher.New(launcher.Config{
			Host:             cfg.Agent.StandaloneHost,
			Port:             cfg.Agent.StandalonePort,
			StartupConfig:    cfg.ManagedAgentctlStartupConfig(),
			OnUnexpectedExit: onUnexpectedExit,
		}, log)
	}
	recordFresh := func(recordCtx context.Context, host string, l relaunchLauncher) error {
		if !cfg.Features.AgentSurvival {
			return nil
		}
		hostPort := net.JoinHostPort(host, strconv.Itoa(l.Port()))
		client, err := controlClientFactory(log)(hostPort)
		if err != nil {
			return err
		}
		return agentruntime.RecordFreshControlServer(recordCtx, store, secretStore, client, hostPort, l.AuthToken())
	}
	startRenewal := func(renewCtx context.Context, host string, l relaunchLauncher) ownershipStopper {
		if !cfg.Features.AgentSurvival {
			return nil
		}
		hostPort := net.JoinHostPort(host, strconv.Itoa(l.Port()))
		period, _ := ownershipperiod.Resolve(cfg.Agentctl.UnownedPeriod, cfg.Agentctl.IdleTimeout)
		renewer := startOwnershipRenewal(renewCtx, log, hostPort, l.AuthToken(), period)
		if renewer == nil {
			return nil
		}
		return renewer
	}
	return newAgentctlSupervisor(ctx, log, availability, endpoint, cfg.Agent.StandaloneHost, newLauncher, recordFresh, startRenewal)
}

// startOwnershipRenewal builds a dedicated control client authenticated with
// the given credential and starts the periodic ownership-claim loop against
// it (AC-EXECUTORS-CONTROL-OWNERSHIP-003.2/.8), computing the renewal
// interval from the given already-resolved unowned period. The caller
// decides where that period comes from: this launch's own local config is
// only correct for a server this launch just spawned itself -- an adopted
// server may have been spawned by a prior launch with different config, so
// its renewal period must come from what that server itself reports (see
// resolveAdoptedRenewalPeriod). Returns nil (logged, not fatal) when the
// endpoint can't be parsed; startup already succeeded by this point, so
// refusing to run the surviving server over a renewal-loop failure would
// trade a smaller problem for a bigger one.
func startOwnershipRenewal(
	ctx context.Context,
	log *logger.Logger,
	endpoint string,
	credential string,
	period time.Duration,
) *agentruntime.OwnershipRenewer {
	host, port, err := splitEndpoint(endpoint)
	if err != nil {
		log.Warn("failed to start ownership renewal loop; endpoint malformed", zap.Error(err))
		return nil
	}
	client := agentctlclient.NewControlClient(host, port, log)
	client.SetAuthToken(credential)
	renewer := agentruntime.NewOwnershipRenewer(client, ownershipperiod.RenewalInterval(period), log)
	renewer.Start(ctx)
	return renewer
}

// resolveAdoptedRenewalPeriod picks the unowned period an adopted server's
// ownership-renewal loop renews against: the value that server itself
// reported on /identity during adoption (outcome.UnownedPeriod), never this
// launch's local config -- the two can disagree across a restart that
// changed agentctl.unownedPeriod, and renewing on this launch's own
// (possibly longer) cadence lets the adopted server's own reaper fire while
// this backend still believes it owns it. Falls back to the shared floor
// only for a legacy pre-upgrade server that answered /identity without an
// unowned_period_ms field at all (reported as zero).
func resolveAdoptedRenewalPeriod(reported time.Duration) time.Duration {
	if reported <= 0 {
		return ownershipperiod.MinPeriod
	}
	return reported
}

// controlClientFactory builds the real agentruntime.AdoptionControlClientFactory
// used in production: it parses "host:port" and returns a live
// agentctlclient.ControlClient targeting it.
func controlClientFactory(log *logger.Logger) agentruntime.AdoptionControlClientFactory {
	return func(endpoint string) (agentruntime.AdoptionControlClient, error) {
		host, port, err := splitEndpoint(endpoint)
		if err != nil {
			return nil, err
		}
		return agentctlclient.NewControlClient(host, port, log), nil
	}
}

func splitEndpoint(endpoint string) (host string, port int, err error) {
	host, portStr, err := net.SplitHostPort(endpoint)
	if err != nil {
		return "", 0, fmt.Errorf("split control server endpoint %q: %w", endpoint, err)
	}
	port, err = strconv.Atoi(portStr)
	if err != nil {
		return "", 0, fmt.Errorf("parse control server port from endpoint %q: %w", endpoint, err)
	}
	return host, port, nil
}

// waitForAgentctlControlHealthy waits for the agentctl control server to be healthy.
// This is called during startup to ensure agentctl is ready before accepting requests.
func waitForAgentctlControlHealthy(ctx context.Context, cfg *config.Config, log *logger.Logger) {
	client := agentctlclient.NewControlClient(cfg.Agent.StandaloneHost, cfg.Agent.StandalonePort, log)
	healthCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	var lastErr error
	for {
		attemptCtx, attemptCancel := context.WithTimeout(healthCtx, 1*time.Second)
		err := client.Health(attemptCtx)
		attemptCancel()
		if err == nil {
			log.Info("agentctl control server is healthy")
			return
		}
		lastErr = err
		if healthCtx.Err() != nil {
			log.Warn("agentctl control server not ready; skipping resume wait", zap.Error(lastErr))
			return
		}
		<-ticker.C
	}
}

const (
	// relaunchGrace is the delay after an unexpected exit before the first
	// relaunch attempt, giving a transient cause (e.g. a port still being
	// released) a moment to clear.
	relaunchGrace = time.Second
	// relaunchMaxAttempts is the number of relaunch attempts made per outage
	// before the runtime is marked unavailable with reason relaunch_exhausted.
	relaunchMaxAttempts = 5
	// relaunchAttemptTimeout bounds a single attempt's Start call.
	relaunchAttemptTimeout = 10 * time.Second
	// relaunchRecordTimeout bounds the adoption-record write after a
	// successful attempt. Its failure is logged and non-fatal.
	relaunchRecordTimeout = 2 * time.Second
	// outageWindow and outageWindowLimit bound how many outage starts within
	// a rolling window are tolerated before the supervisor gives up without
	// attempting a relaunch at all (a crash loop, not a transient exit).
	outageWindow      = 10 * time.Minute
	outageWindowLimit = 3
)

// relaunchBackoff[attempt] is the delay before the attempt after the given
// attempt number fails. Index 0 and relaunchMaxAttempts are unused: attempt
// 1 fires after relaunchGrace with no extra backoff, and no delay follows a
// failed attempt 5, since the loop ends there.
var relaunchBackoff = [relaunchMaxAttempts + 1]time.Duration{
	0,
	1 * time.Second,
	2 * time.Second,
	4 * time.Second,
	8 * time.Second,
	0,
}

// relaunchLauncher is the narrow surface the supervisor drives an agentctl
// launcher through, satisfied by *launcher.Launcher. Narrowed so tests can
// exercise the supervisor's relaunch loop against a fake.
type relaunchLauncher interface {
	Start(ctx context.Context) error
	ForceStop()
	Stop(ctx context.Context) error
	Port() int
	Pid() int
	AuthToken() string
}

// ownershipStopper is the narrow surface the supervisor stops an ownership
// renewal loop through, satisfied by *agentruntime.OwnershipRenewer.
type ownershipStopper interface {
	Stop()
}

type attemptResult int

const (
	attemptFailed attemptResult = iota
	attemptSucceeded
	attemptAbortedByShutdown
)

// agentctlSupervisor relaunches the standalone agentctl control server after
// an unexpected exit, per docs/specs/platform/system-design/agent-runtime-availability.md
// ("Relaunch supervisor"). It owns the single mutex guarding the outage
// window, the current launcher/renewer pair, and shutdown state; publishing
// state (endpoint identity, availability) happens under that same mutex, and
// no goroutine may wait on a launcher while holding it -- see NotifyExit and
// attemptRelaunch for the resulting lock discipline.
type agentctlSupervisor struct {
	log          *logger.Logger
	availability *agentctlclient.Availability
	endpoint     *agentctlclient.Endpoint
	host         string
	newLauncher  func(onUnexpectedExit func()) relaunchLauncher
	recordFresh  func(ctx context.Context, host string, l relaunchLauncher) error
	startRenewal func(ctx context.Context, host string, l relaunchLauncher) ownershipStopper

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu           sync.Mutex
	shuttingDown bool
	windowStarts []time.Time
	current      relaunchLauncher
	renewer      ownershipStopper
}

// newAgentctlSupervisor constructs a supervisor bound to the given endpoint
// and availability trackers. newLauncher builds a fresh, unstarted launcher
// wired with the given exit callback; recordFresh persists a freshly
// relaunched control server's adoption record; startRenewal starts a new
// ownership-renewal loop against it. Both are no-ops when the agent-survival
// capability is disabled -- the caller decides that, not this type.
func newAgentctlSupervisor(
	ctx context.Context,
	log *logger.Logger,
	availability *agentctlclient.Availability,
	endpoint *agentctlclient.Endpoint,
	host string,
	newLauncher func(onUnexpectedExit func()) relaunchLauncher,
	recordFresh func(ctx context.Context, host string, l relaunchLauncher) error,
	startRenewal func(ctx context.Context, host string, l relaunchLauncher) ownershipStopper,
) *agentctlSupervisor {
	supervisorCtx, cancel := context.WithCancel(ctx)
	return &agentctlSupervisor{
		log:          log,
		availability: availability,
		endpoint:     endpoint,
		host:         host,
		newLauncher:  newLauncher,
		recordFresh:  recordFresh,
		startRenewal: startRenewal,
		ctx:          supervisorCtx,
		cancel:       cancel,
	}
}

// BindInitialLauncher registers the launcher the caller already started and
// published as the supervisor's current launcher, so a later unexpected
// exit of that same process reaches NotifyExit as a match. Call this after
// Endpoint.Publish, once the generation this launcher owns is current.
func (s *agentctlSupervisor) BindInitialLauncher(l relaunchLauncher, renewer ownershipStopper) {
	s.mu.Lock()
	s.current = l
	s.renewer = renewer
	s.mu.Unlock()
}

// NextExitCallback returns the generation-capturing OnUnexpectedExit closure
// for a launcher this caller is about to construct and start: the generation
// that launcher will own if it is published is endpoint.Current().Generation
// + 1 at the moment of construction, which is what NotifyExit later compares
// against. The caller must construct the launcher before any other publish
// can occur (true for both the initial boot launcher and every relaunch
// attempt, since only the supervisor's own single-flight loop publishes).
func (s *agentctlSupervisor) NextExitCallback() func() {
	generation := s.endpoint.Current().Generation + 1
	return func() { s.NotifyExit(generation) }
}

// NotifyExit handles an unexpected exit reported by a launcher's
// OnUnexpectedExit callback, called synchronously from that launcher's own
// exit-monitoring goroutine before it closes its exited channel. generation
// is the endpoint generation the exiting launcher owns if published; a
// mismatch means either a stale callback from an already-replaced launcher,
// or an attempt that exited before it was ever published (see
// attemptRelaunch's own exited flag for that second case) -- both are no-ops
// here. Shutdown makes every call a no-op, published or not (AC-002.9).
func (s *agentctlSupervisor) NotifyExit(generation uint64) {
	s.mu.Lock()
	if s.shuttingDown || generation != s.endpoint.Current().Generation {
		s.mu.Unlock()
		return
	}

	t0 := time.Now()
	s.pruneWindow(t0)
	if len(s.windowStarts) >= outageWindowLimit {
		s.log.Error("agentctl relaunch exhausted", zap.String("reason", "outage_window_limit"))
		s.availability.MarkUnavailable(agentctlclient.AvailabilityReasonRelaunchExhausted, t0)
		s.mu.Unlock()
		return
	}
	s.windowStarts = append(s.windowStarts, t0)
	s.availability.MarkRecovering(t0)
	// wg.Add must happen before Shutdown's wg.Wait can observe it, per
	// sync.WaitGroup's contract; doing it while still holding mu orders it
	// against Shutdown's own mu-protected shuttingDown write. Otherwise a
	// concurrent Shutdown could see a zero counter, return from Wait, and
	// finish tearing down before this outage's loop goroutine ever starts.
	s.wg.Add(1)
	s.mu.Unlock()

	s.log.Warn("agentctl exited unexpectedly")
	go s.runRelaunchLoop(t0)
}

// pruneWindow drops outage starts at or beyond outageWindow before t0. Must
// be called with mu held.
func (s *agentctlSupervisor) pruneWindow(t0 time.Time) {
	kept := s.windowStarts[:0]
	for _, start := range s.windowStarts {
		if t0.Sub(start) < outageWindow {
			kept = append(kept, start)
		}
	}
	s.windowStarts = kept
}

// runRelaunchLoop drives relaunch attempts for one outage until one
// succeeds, is aborted by shutdown, or all relaunchMaxAttempts attempts have
// failed. It owns exactly the goroutine NotifyExit started for this outage.
func (s *agentctlSupervisor) runRelaunchLoop(t0 time.Time) {
	defer s.wg.Done()

	s.mu.Lock()
	oldRenewer := s.renewer
	s.renewer = nil
	s.mu.Unlock()
	if oldRenewer != nil {
		oldRenewer.Stop()
	}

	graceTimer := time.NewTimer(relaunchGrace)
	select {
	case <-graceTimer.C:
	case <-s.ctx.Done():
		graceTimer.Stop()
		return
	}

	for attempt := 1; attempt <= relaunchMaxAttempts; attempt++ {
		switch s.attemptRelaunch(attempt) {
		case attemptSucceeded, attemptAbortedByShutdown:
			return
		case attemptFailed:
			if attempt == relaunchMaxAttempts {
				s.exhaustRelaunch(t0)
				return
			}
			if !s.waitBeforeNextAttempt(attempt) {
				return
			}
		}
	}
}

// waitBeforeNextAttempt waits the backoff delay for the attempt that just
// failed, or returns false immediately if the supervisor context ends first.
func (s *agentctlSupervisor) waitBeforeNextAttempt(failedAttempt int) bool {
	timer := time.NewTimer(relaunchBackoff[failedAttempt])
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-s.ctx.Done():
		return false
	}
}

// exhaustRelaunch marks the runtime unavailable after every relaunch attempt
// for this outage failed. Per the accepted spec gap R3-1, the shutdown/
// context recheck and the MarkUnavailable call happen under one uninterrupted
// mutex hold, so a shutdown racing the final attempt's failure never
// publishes a transition after shutdown began.
func (s *agentctlSupervisor) exhaustRelaunch(t0 time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shuttingDown || s.ctx.Err() != nil {
		return
	}
	s.log.Error("agentctl relaunch exhausted", zap.String("reason", "attempts_exhausted"))
	s.availability.MarkUnavailable(agentctlclient.AvailabilityReasonRelaunchExhausted, t0)
}

// attemptRelaunch runs one relaunch attempt: build a launcher, start it
// under a bounded timeout, and on success publish it as the current
// endpoint. See the package-level design note above for the lock discipline
// steps 3-5 rely on.
func (s *agentctlSupervisor) attemptRelaunch(attempt int) attemptResult {
	var exited atomic.Bool
	generation := s.endpoint.Current().Generation + 1
	l := s.newLauncher(func() {
		exited.Store(true)
		s.NotifyExit(generation)
	})

	attemptCtx, cancel := context.WithTimeout(s.ctx, relaunchAttemptTimeout)
	err := l.Start(attemptCtx)
	cancel()
	if err != nil {
		l.ForceStop()
		s.log.Warn("agentctl relaunch attempt failed", zap.Int("attempt", attempt), zap.Error(err))
		return attemptFailed
	}

	return s.publishAttempt(attempt, l, &exited)
}

// publishAttempt runs steps 3-5 of a successful attempt's Start call: the
// shutdown recheck, the bounded adoption-record write, the exited-before-
// published check, and (on survival) publishing the new endpoint identity
// and marking the runtime available. It holds mu for the whole of steps 3-5,
// which is what keeps shutdown from landing between publish and MarkAvailable
// (AC-002.9) -- see the BINDING BUILD NOTES on R3-1/R3-2 in the work order.
func (s *agentctlSupervisor) publishAttempt(attempt int, l relaunchLauncher, exited *atomic.Bool) attemptResult {
	s.mu.Lock()
	if s.shuttingDown || s.ctx.Err() != nil {
		s.mu.Unlock()
		l.ForceStop()
		return attemptAbortedByShutdown
	}

	recordCtx, recordCancel := context.WithTimeout(context.Background(), relaunchRecordTimeout)
	if err := s.recordFresh(recordCtx, s.host, l); err != nil {
		s.log.Warn("failed to record freshly relaunched control server", zap.Error(err))
	}
	recordCancel()

	if exited.Load() {
		s.mu.Unlock()
		l.ForceStop()
		s.log.Warn("agentctl exited during relaunch attempt before it was published", zap.Int("attempt", attempt))
		return attemptFailed
	}

	renewer := s.startRenewal(s.ctx, s.host, l)
	s.current = l
	s.renewer = renewer
	s.endpoint.Publish(s.host, l.Port(), l.AuthToken(), l.Pid())
	s.availability.MarkAvailable()
	s.mu.Unlock()

	s.log.Info("agentctl relaunched", zap.Int("attempt", attempt))
	return attemptSucceeded
}

// Shutdown stops the supervisor: no further relaunch attempt starts, the
// running loop (if any) is drained, and only then is the current launcher
// and renewer stopped, both outside the mutex so no goroutine ever waits on
// a launcher while holding it. Agent survival makes the launcher's own Stop
// a no-op, so the process is left running when that capability is engaged.
func (s *agentctlSupervisor) Shutdown() {
	s.mu.Lock()
	s.shuttingDown = true
	s.mu.Unlock()

	s.cancel()
	s.wg.Wait()

	s.mu.Lock()
	l := s.current
	renewer := s.renewer
	s.current = nil
	s.renewer = nil
	s.mu.Unlock()

	if renewer != nil {
		renewer.Stop()
	}
	if l != nil {
		stopCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := l.Stop(stopCtx); err != nil {
			s.log.Warn("failed to stop agentctl during shutdown", zap.Error(err))
		}
	}
}
