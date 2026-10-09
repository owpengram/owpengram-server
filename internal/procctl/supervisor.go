package procctl

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"telesrv/internal/config"
	"telesrv/internal/embeddedpg"
)

// stopRequestFileName is how `owpengram-ctl stop` asks a running supervisor to
// shut down. A file rather than a signal because Windows has no signal that
// lets a process clean up after itself (the embedded PostgreSQL must be
// stopped gracefully, not killed).
const stopRequestFileName = ".ctl_stop"

const (
	// A process that stayed up this long before exiting was not crash-looping,
	// so it is started again at once instead of backing off.
	healthyRunTime = 10 * time.Second
	maxBackoff     = 30 * time.Second
	// stopGrace is how long a child gets to exit after a polite request before
	// it is killed.
	stopGrace = 15 * time.Second
)

// EnvValue reads one key from .env ("" when absent).
func (m *Manager) EnvValue(key string) string {
	values, err := m.readEnvFile()
	if err != nil {
		return ""
	}
	return values[key]
}

// StorageConfigured reports whether the first-run wizard's storage step has
// been done: .env names where PostgreSQL lives. An .env written by an older
// version (it carries a DSN or a TELESRV_EDITION) counts, so an existing
// install starts straight away instead of asking again.
func (m *Manager) StorageConfigured() bool {
	values, err := m.readEnvFile()
	if err != nil {
		return false
	}
	return values["TELESRV_POSTGRES_MODE"] != "" || values["TELESRV_EDITION"] != "" || values["TELESRV_POSTGRES_DSN"] != ""
}

// stateMu serializes the read-modify-write of the PID file between the
// supervisor's goroutines.
var stateMu sync.Mutex

func (m *Manager) updateState(f func(*State)) {
	stateMu.Lock()
	defer stateMu.Unlock()
	st := m.loadState()
	f(&st)
	_ = m.saveState(st)
}

// RequestStop asks a running supervisor to shut down and waits for it to be
// gone. It reports false when none was running.
func (m *Manager) RequestStop(timeout time.Duration) (bool, error) {
	st := m.loadState()
	if !pidAlive(st.CtlPID) {
		return false, nil
	}
	if err := os.WriteFile(filepath.Join(m.Root, stopRequestFileName), nil, 0o644); err != nil {
		return true, err
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !pidAlive(st.CtlPID) {
			return true, nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return true, fmt.Errorf("owpengram-ctl (pid %d) did not stop within %s", st.CtlPID, timeout)
}

// Run is the whole program: it blocks until ctx is cancelled (or a stop is
// requested), keeping owpengram-admin-panel and owpengram-server running, and
// stops everything on the way out. logf receives one line per event. onReady,
// when set, is called once owpengram-server is actually listening (the last
// thing to come up -- migrations and the one-time media seed run first), so
// the caller can print the addresses then rather than while things are still
// starting.
func (m *Manager) Run(ctx context.Context, logf func(format string, args ...any), onReady ...func()) error {
	st := m.loadState()
	if st.CtlPID != os.Getpid() && pidAlive(st.CtlPID) {
		return fmt.Errorf("owpengram-ctl is already running (pid %d) -- stop it first with `owpengram-ctl stop`", st.CtlPID)
	}
	// Left over from a version that started both detached, or from a ctl that
	// was killed: they would hold the ports the new ones need.
	for _, pid := range []int{st.ServerPID, st.AdminPID} {
		if pid > 0 && pidAlive(pid) {
			logf("stopping a leftover process (pid %d) from an earlier run", pid)
			killPID(pid)
		}
	}
	_ = os.Remove(filepath.Join(m.Root, stopRequestFileName))
	m.updateState(func(s *State) { s.CtlPID, s.ServerPID, s.AdminPID = os.Getpid(), 0, 0 })
	defer m.updateState(func(s *State) { s.CtlPID, s.ServerPID, s.AdminPID = 0, 0, 0 })

	logf("building owpengram-server and owpengram-admin-panel...")
	if log, err := m.buildBoth(ctx); err != nil {
		return fmt.Errorf("build failed: %w\n%s", err, log)
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Report readiness once the server is listening (the web client is served
	// on that same listener, so one dial covers both).
	if len(onReady) > 0 && onReady[0] != nil {
		go func() {
			addr := m.serverListenAddr()
			for runCtx.Err() == nil {
				if addr != "" && listening(addr) {
					onReady[0]()
					return
				}
				sleepCtx(runCtx, 500*time.Millisecond)
			}
		}()
	}

	pg := &postgresOwner{m: m, logf: logf}
	defer pg.stop()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		m.supervise(runCtx, logf, service{
			name: "owpengram-admin-panel", exe: m.adminExe(), logPath: m.adminLog(),
			setPID: func(pid int) { m.updateState(func(s *State) { s.AdminPID = pid }) },
		})
	}()
	go func() {
		defer wg.Done()
		m.supervise(runCtx, logf, service{
			name: "owpengram-server", exe: m.serverExe(), logPath: m.serverLog(),
			setPID: func(pid int) { m.updateState(func(s *State) { s.ServerPID = pid }) },
			gate: func(ctx context.Context) (bool, string) {
				if !m.StorageConfigured() {
					return false, "waiting for the first-run setup to choose where data is stored"
				}
				if err := pg.ensure(ctx); err != nil {
					return false, "PostgreSQL is not ready: " + err.Error()
				}
				return true, ""
			},
		})
	}()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case <-ticker.C:
			if _, err := os.Stat(filepath.Join(m.Root, stopRequestFileName)); err == nil {
				_ = os.Remove(filepath.Join(m.Root, stopRequestFileName))
				logf("stop requested")
				break loop
			}
		}
	}
	cancel()
	wg.Wait()
	return nil
}

// serverListenAddr returns TELESRV_LISTEN (the listener that carries MTProto
// and, on its HTTP side, the embedded web client) as a loopback dial target,
// or "" when it cannot be determined.
func (m *Manager) serverListenAddr() string {
	addr := m.EnvValue("TELESRV_LISTEN")
	if addr == "" {
		addr = "0.0.0.0:2398" // config default
	}
	return BrowsableHostPort(addr)
}

// listening reports whether a TCP listener answers on addr (host:port).
func listening(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// service is one child the supervisor keeps running.
type service struct {
	name, exe, logPath string
	setPID             func(int)
	// gate, when set, must report true before each (re)start; otherwise the
	// reason is logged and it is asked again shortly.
	gate func(ctx context.Context) (ok bool, why string)
}

// supervise runs svc until ctx is cancelled, starting it again whenever it
// exits.
func (m *Manager) supervise(ctx context.Context, logf func(string, ...any), svc service) {
	failures := 0
	lastWhy := ""
	for ctx.Err() == nil {
		if svc.gate != nil {
			ok, why := svc.gate(ctx)
			if !ok {
				if why != lastWhy {
					logf("%s: %s", svc.name, why)
					lastWhy = why
				}
				sleepCtx(ctx, 2*time.Second)
				continue
			}
			lastWhy = ""
		}

		started := time.Now()
		err := m.runChild(ctx, logf, svc)
		if ctx.Err() != nil {
			return
		}
		if time.Since(started) >= healthyRunTime {
			failures = 0
		} else {
			failures++
		}
		delay := time.Second
		if failures > 0 {
			delay = time.Duration(1<<min(failures, 5)) * time.Second
			if delay > maxBackoff {
				delay = maxBackoff
			}
		}
		if err != nil {
			logf("%s stopped: %v -- starting it again in %s", svc.name, err, delay)
		} else {
			logf("%s stopped -- starting it again in %s", svc.name, delay)
		}
		sleepCtx(ctx, delay)
	}
}

func sleepCtx(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// runChild starts the process and blocks until it exits or ctx is cancelled,
// in which case it is asked to stop (and killed if it will not).
func (m *Manager) runChild(ctx context.Context, logf func(string, ...any), svc service) error {
	if err := os.MkdirAll(filepath.Dir(svc.logPath), 0o755); err != nil {
		return fmt.Errorf("mkdir logs: %w", err)
	}
	// A release archive may have been packaged on an OS without an executable
	// bit (NTFS), so set it before exec.
	_ = os.Chmod(svc.exe, 0o755)
	logf2, err := os.OpenFile(svc.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open log: %w", err)
	}
	defer logf2.Close()

	cmd := exec.Command(svc.exe)
	cmd.Dir = m.Root
	cmd.Stdout = logf2
	cmd.Stderr = logf2
	hideWindow(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", svc.exe, err)
	}
	logf("%s started (pid %d)", svc.name, cmd.Process.Pid)
	svc.setPID(cmd.Process.Pid)
	defer svc.setPID(0)

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
			_ = cmd.Process.Kill()
		}
		select {
		case <-done:
		case <-time.After(stopGrace):
			_ = cmd.Process.Kill()
			<-done
		}
		return ctx.Err()
	}
}

// postgresOwner holds the embedded PostgreSQL for the supervisor's lifetime.
// It is started before owpengram-server and survives that process restarting.
type postgresOwner struct {
	m    *Manager
	logf func(string, ...any)

	mu      sync.Mutex
	srv     *embeddedpg.Server
	logFile *os.File // the server writes to it until it is stopped
}

// ensure brings PostgreSQL to the state .env asks for: running when the mode
// is embedded, left alone (and stopped if it was ours) when it is external.
func (p *postgresOwner) ensure(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	fileValues, _ := p.m.readEnvFile()
	// The process environment wins over .env, the same as in config.Load, so
	// the processes started here and this agree on the port and the folder.
	value := func(key string) string {
		if v := os.Getenv(key); v != "" {
			return v
		}
		return fileValues[key]
	}
	mode := config.ResolvePostgresMode(value("TELESRV_POSTGRES_MODE"), value("TELESRV_EDITION"), value("TELESRV_POSTGRES_DSN"))
	if mode != config.PostgresModeEmbedded {
		p.stopLocked()
		return nil
	}
	if p.srv != nil {
		return nil
	}

	dir := value("TELESRV_EMBEDDED_POSTGRES_DIR")
	if dir == "" {
		dir = "data/postgres"
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(p.m.Root, dir)
	}
	port := embeddedpg.DefaultPort
	if v := value("TELESRV_EMBEDDED_POSTGRES_PORT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 || n > 65535 {
			return fmt.Errorf("TELESRV_EMBEDDED_POSTGRES_PORT %q is not a port", v)
		}
		port = n
	}

	if err := os.MkdirAll(filepath.Join(p.m.Root, "logs"), 0o755); err != nil {
		return err
	}
	logFile, err := os.OpenFile(filepath.Join(p.m.Root, "logs", "postgres.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}

	p.logf("starting the embedded PostgreSQL on port %d (data in %s)", port, dir)
	srv, err := embeddedpg.Start(dir, port, logFile)
	if err != nil {
		logFile.Close()
		return err
	}
	p.srv, p.logFile = srv, logFile
	p.logf("embedded PostgreSQL is ready")
	return nil
}

func (p *postgresOwner) stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopLocked()
}

func (p *postgresOwner) stopLocked() {
	if p.srv == nil {
		return
	}
	if err := p.srv.Stop(); err != nil {
		p.logf("stopping the embedded PostgreSQL: %v", err)
	}
	p.srv = nil
	if p.logFile != nil {
		p.logFile.Close()
		p.logFile = nil
	}
}
