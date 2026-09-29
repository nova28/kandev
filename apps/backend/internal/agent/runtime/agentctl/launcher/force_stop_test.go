package launcher

import (
	"os/exec"
	"syscall"
	"testing"
	"time"

	commonconfig "github.com/kandev/kandev/internal/common/config"
	"github.com/kandev/kandev/internal/common/logger"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// TestForceStopKillsProcessUnderAgentSurvivalWithoutFiringCallback pins the
// relaunch supervisor's use of ForceStop: an unpublished attempt must never
// outlive itself, even when agent survival is engaged for the launch, and
// the forced stop must not be mistaken for an unexpected exit -- the
// supervisor's own exit closure handles a failed attempt, so a second,
// launcher-level callback would double-count it.
func TestForceStopKillsProcessUnderAgentSurvivalWithoutFiringCallback(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start long-lived test process: %v", err)
	}
	pgid := cmd.Process.Pid
	t.Cleanup(func() { _ = syscall.Kill(-pgid, syscall.SIGKILL) })

	exited := make(chan struct{})
	callback := make(chan struct{}, 1)
	l := &Launcher{
		cmd:              cmd,
		exited:           exited,
		reaped:           make(chan struct{}),
		logger:           newLauncherTestLogger(t),
		onUnexpectedExit: func() { callback <- struct{}{} },
		startupConfig:    commonconfig.AgentctlStartupConfig{Configured: true, AgentSurvivalEnabled: true},
	}
	go l.monitorExit()

	done := make(chan struct{})
	go func() {
		l.ForceStop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ForceStop did not return")
	}

	if err := cmd.Process.Signal(syscall.Signal(0)); err == nil {
		t.Fatal("process is still alive after ForceStop, even though agent survival was engaged")
	}
	select {
	case <-callback:
		t.Fatal("OnUnexpectedExit fired for a ForceStop; the supervisor's own exit closure owns attempt failure")
	default:
	}
}

// TestForceStopReturnsAtOnceWhenNoProcessStarted covers the port/nonce
// failure case in Start: no process was ever spawned, so there is no
// monitorExit goroutine to close exited, and ForceStop must not wait on it.
func TestForceStopReturnsAtOnceWhenNoProcessStarted(t *testing.T) {
	l := &Launcher{exited: make(chan struct{}), logger: newLauncherTestLogger(t)}

	done := make(chan struct{})
	go func() {
		l.ForceStop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ForceStop blocked with no process started")
	}
}

// TestForceStopIsIdempotentAndSendsNoSignalAfterExit covers both remaining
// AC-002.5 clauses at once, since they share one code path: once exited is
// closed (Start's own failure cleanup already reaped the process, or a prior
// ForceStop call did), a later ForceStop call finds the exited case ready and
// skips forceKill entirely -- it never touches a PID the OS may have reused.
func TestForceStopIsIdempotentAndSendsNoSignalAfterExit(t *testing.T) {
	binary, err := exec.LookPath("true")
	if err != nil {
		t.Skipf("no 'true' binary on PATH: %v", err)
	}
	cmd := exec.Command(binary)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start short-lived test process: %v", err)
	}

	l := &Launcher{cmd: cmd, exited: make(chan struct{}), reaped: make(chan struct{}), logger: newLauncherTestLogger(t)}
	go l.monitorExit()

	select {
	case <-l.exited:
	case <-time.After(5 * time.Second):
		t.Fatal("process never exited")
	}

	for attempt := 0; attempt < 2; attempt++ {
		done := make(chan struct{})
		go func() {
			l.ForceStop()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("ForceStop call %d blocked after the process had already exited", attempt+1)
		}
	}
}

// TestForceStopBlocksUntilOnUnexpectedExitCallbackReleases pins the hazard
// behind RV1-4: monitorExit calls onUnexpectedExit before closing exited, and
// ForceStop waits on exited. If the process exits on its own (stopping still
// false) and onUnexpectedExit blocks -- e.g. the relaunch supervisor's exit
// closure waiting on its own mutex -- a concurrent ForceStop call (the
// supervisor cleaning up an abandoned attempt) must block for exactly as
// long as that callback does, not longer and not less; it must not return
// early and leave the process unreaped, and it must not deadlock forever once
// the callback finishes.
func TestForceStopBlocksUntilOnUnexpectedExitCallbackReleases(t *testing.T) {
	binary, err := exec.LookPath("true")
	if err != nil {
		t.Skipf("no 'true' binary on PATH: %v", err)
	}
	cmd := exec.Command(binary)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start short-lived test process: %v", err)
	}

	callbackStarted := make(chan struct{})
	release := make(chan struct{})
	l := &Launcher{
		cmd:    cmd,
		exited: make(chan struct{}),
		reaped: make(chan struct{}),
		logger: newLauncherTestLogger(t),
		onUnexpectedExit: func() {
			close(callbackStarted)
			<-release
		},
	}
	go l.monitorExit()

	select {
	case <-callbackStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("OnUnexpectedExit never started; the process may not have exited on its own")
	}

	done := make(chan struct{})
	go func() {
		l.ForceStop()
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("ForceStop returned while OnUnexpectedExit was still blocked on the outside mutex")
	case <-time.After(200 * time.Millisecond):
	}

	close(release)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ForceStop did not return after OnUnexpectedExit was released")
	}
}

// TestForceStopSkipsSignalOncePidIsReapedEvenWhileCallbackRuns pins RV2-8:
// cmd.Wait() reaps the PID (making it eligible for OS reuse) before
// onUnexpectedExit runs, and onUnexpectedExit can run for an arbitrary
// duration before exited closes. A concurrent ForceStop call must treat the
// PID as already reaped -- and skip signalling it -- for that entire
// callback-execution window, not only after exited closes. Gating on exited
// (the pre-fix behavior) would send a signal to a PID that may already
// belong to an unrelated process.
func TestForceStopSkipsSignalOncePidIsReapedEvenWhileCallbackRuns(t *testing.T) {
	binary, err := exec.LookPath("true")
	if err != nil {
		t.Skipf("no 'true' binary on PATH: %v", err)
	}
	cmd := exec.Command(binary)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start short-lived test process: %v", err)
	}

	core, observed := observer.New(zapcore.DebugLevel)
	log, err := logger.NewFromZap(zap.New(core))
	if err != nil {
		t.Fatalf("NewFromZap: %v", err)
	}

	callbackStarted := make(chan struct{})
	release := make(chan struct{})
	l := &Launcher{
		cmd:    cmd,
		exited: make(chan struct{}),
		reaped: make(chan struct{}),
		logger: log,
		onUnexpectedExit: func() {
			close(callbackStarted)
			<-release
		},
	}
	go l.monitorExit()

	select {
	case <-callbackStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("OnUnexpectedExit never started; the process may not have exited on its own")
	}

	done := make(chan struct{})
	go func() {
		l.ForceStop()
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("ForceStop returned while OnUnexpectedExit was still blocked on the outside mutex")
	case <-time.After(200 * time.Millisecond):
	}

	if launcherLogsContain(observed, "agentctl subprocess SIGKILL requested") {
		t.Fatal("ForceStop signalled the PID while it was already reaped and the exit callback was still running")
	}

	close(release)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ForceStop did not return after OnUnexpectedExit was released")
	}
}
