// Package procctl mirrors the process-management half of tui-panel/
// server-panel.py (git pull, go build, launch/stop, and the shared
// .server_panel.json PID state file) so the admin web panel can offer the
// same Restart/Update actions the TUI already has, without requiring an
// operator to SSH in and use the TUI for that specifically.
//
// Restart/Update never restart the admin binary themselves (the process
// this code typically runs inside, when called from cmd/telesrv-admin) --
// self-restarting mid-HTTP-request is a materially different, riskier
// problem (dropped response, no clean signal to the caller that it actually
// completed) than the TUI's case, where a human is watching an interactive
// session and re-exec is transparent. Instead they set
// State.PendingAdminRestart and let the *next* owpengram-server process
// pick it up via HandlePendingAdminRestart once it's confirmed serving
// (cmd/telesrv/main.go's OnServing hook) -- that process is unrelated to
// whatever admin panel is currently running, so it can safely kill the old
// admin PID and launch a new one with none of the self-restart risk.
package procctl

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const stateFileName = ".server_panel.json"

// Manager operates on one repo checkout (Root), the same layout
// tui-panel/server-panel.py expects: bin/, logs/, .env, .env.example, and
// .server_panel.json at the root.
type Manager struct {
	Root string
}

func NewManager(root string) *Manager {
	return &Manager{Root: root}
}

func (m *Manager) serverExe() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(m.Root, "bin", "owpengram-server.exe")
	}
	return filepath.Join(m.Root, "bin", "owpengram-server")
}

func (m *Manager) adminExe() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(m.Root, "bin", "owpengram-admin-panel.exe")
	}
	return filepath.Join(m.Root, "bin", "owpengram-admin-panel")
}

func (m *Manager) serverLog() string { return filepath.Join(m.Root, "logs", "owpengram-server.log") }
func (m *Manager) adminLog() string {
	return filepath.Join(m.Root, "logs", "owpengram-admin-panel.log")
}

// --- state file (shared with tui-panel/server-panel.py) --------------------

type State struct {
	ServerPID     int    `json:"server_pid"`
	AdminPID      int    `json:"admin_pid"`
	DockerProject string `json:"docker_project"`
	DockerPrefix  string `json:"docker_prefix"`
	// PendingAdminRestart is how Restart/Update ask the *next*
	// owpengram-server process to bounce the admin panel for them, instead
	// of the admin panel trying to restart itself mid-HTTP-request (see the
	// package doc). Set here, consumed by HandlePendingAdminRestart at the
	// new owpengram-server's startup. server-panel.py doesn't know this key
	// exists -- its own save_state() overwrites the file with only its 4
	// original fields, so a Stop/Start/Restart/Update run from the TUI in
	// the narrow window before the flag is consumed will silently drop it.
	// Rare, and the only consequence is the admin panel not restarting that
	// one time -- not worth coordinating two processes' writes over.
	PendingAdminRestart bool `json:"pending_admin_restart,omitempty"`
}

func (m *Manager) loadState() State {
	var st State
	data, err := os.ReadFile(filepath.Join(m.Root, stateFileName))
	if err != nil {
		return st
	}
	_ = json.Unmarshal(data, &st)
	if st.DockerProject == "" {
		st.DockerProject = "owpengram"
	}
	if st.DockerPrefix == "" {
		st.DockerPrefix = "owpengram"
	}
	return st
}

func (m *Manager) saveState(st State) error {
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(m.Root, stateFileName), data, 0o644)
}

// Status reports whether the server/admin PIDs recorded in the shared state
// file are still alive.
type Status struct {
	ServerPID   int
	ServerAlive bool
	AdminPID    int
	AdminAlive  bool
}

func (m *Manager) Status() Status {
	st := m.loadState()
	return Status{
		ServerPID:   st.ServerPID,
		ServerAlive: pidAlive(st.ServerPID),
		AdminPID:    st.AdminPID,
		AdminAlive:  pidAlive(st.AdminPID),
	}
}

// --- process control ---------------------------------------------------

func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	if runtime.GOOS == "windows" {
		cmd := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid))
		hideWindow(cmd)
		out, err := cmd.Output()
		if err != nil {
			return false
		}
		return strings.Contains(string(out), strconv.Itoa(pid))
	}
	cmd := exec.Command("kill", "-0", strconv.Itoa(pid))
	hideWindow(cmd)
	return cmd.Run() == nil
}

// killPID mirrors kill_pid() in server-panel.py, MINUS its "/T" tree-kill on
// Windows -- deliberately different here, not an oversight. server-panel.py
// is always the common parent of both the server and admin processes, so
// tree-killing one PID never touches the other. This package's callers are
// not: HandlePendingAdminRestart runs *inside* the freshly launched
// owpengram-server, which was itself spawned as a child of the *old*
// owpengram-admin-panel process by the Restart/Update call that got it
// here. Killing that old admin PID with "/T" would tree-kill its entire
// descendant chain -- including this very owpengram-server process, since
// it's a child of the PID being killed. Windows' taskkill walks that chain
// by recorded parent-PID regardless of any process-group flags on launch,
// so the only reliable fix is to never tree-kill here: exact-PID kill only,
// since every process this package launches is started directly via
// exec.Command (no intermediate shell wrapper), so there's no wrapper-spawned
// grandchild "/T" would need to catch anyway.
func killPID(pid int) {
	if pid <= 0 {
		return
	}
	if runtime.GOOS == "windows" {
		cmd := exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/F")
		hideWindow(cmd)
		_ = cmd.Run()
		return
	}
	term := exec.Command("kill", "-TERM", strconv.Itoa(pid))
	hideWindow(term)
	_ = term.Run()
	time.Sleep(time.Second)
	kill := exec.Command("kill", "-KILL", strconv.Itoa(pid))
	hideWindow(kill)
	_ = kill.Run()
}

// launch starts exePath detached, cwd=Root, stdout/stderr appended to
// logPath, and returns its PID. Unlike the Python TUI this does not set a
// new session/process group (that needs OS-specific SysProcAttr) -- started
// via Start() (not Run()), the child outlives this function's return either
// way, which is all a request/response HTTP handler needs.
func (m *Manager) launch(exePath, logPath string) (int, error) {
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return 0, fmt.Errorf("mkdir logs: %w", err)
	}
	logf, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, fmt.Errorf("open log: %w", err)
	}
	defer logf.Close()
	cmd := exec.Command(exePath)
	cmd.Dir = m.Root
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.Stdin = nil
	hideWindow(cmd)
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("start %s: %w", exePath, err)
	}
	go func() { _ = cmd.Wait() }() // reap so it doesn't linger as a zombie
	return cmd.Process.Pid, nil
}

// --- Docker infrastructure (Postgres/Redis/MinIO) -------------------------

const (
	postgresWaitTimeout  = 60 * time.Second
	postgresWaitInterval = 2 * time.Second
)

// ensureDocker mirrors server-panel.py's START_STEPS "docker" + "postgres"
// steps -- `docker compose up -d` then wait for Postgres to answer
// pg_isready. Restart/Update run this every time, same as the TUI: it's a
// no-op when the containers are already up (compose up -d on a running
// stack just confirms state), but skipping it entirely was the actual bug
// report this addresses -- a Restart/Update landing while Postgres/Redis/
// MinIO are down (host reboot, containers manually stopped, etc.) would
// otherwise relaunch owpengram-server straight into a DB-connect failure
// with no clear signal why, instead of surfacing "Postgres not ready" here.
func (m *Manager) ensureDocker(ctx context.Context, st State) (string, error) {
	composeFile := filepath.Join(m.Root, "deploy", "docker-compose.yml")
	if _, err := os.Stat(composeFile); os.IsNotExist(err) {
		return "", nil
	}

	cmd := exec.CommandContext(ctx, "docker", "compose", "-f", composeFile, "up", "-d")
	cmd.Dir = m.Root
	cmd.Env = append(os.Environ(),
		"TELESRV_DOCKER_PROJECT="+st.DockerProject,
		"TELESRV_DOCKER_PREFIX="+st.DockerPrefix,
	)
	hideWindow(cmd)
	out, err := cmd.CombinedOutput()
	log := "$ docker compose up -d\n" + string(out)
	if err != nil {
		return log, fmt.Errorf("docker compose up failed: %w", err)
	}

	deadline := time.Now().Add(postgresWaitTimeout)
	for {
		pgCmd := exec.CommandContext(ctx, "docker", "exec", st.DockerPrefix+"-postgres", "pg_isready", "-U", "telesrv", "-d", "telesrv")
		hideWindow(pgCmd)
		if pgCmd.Run() == nil {
			return log + "\nPostgreSQL ready\n", nil
		}
		if time.Now().After(deadline) {
			return log, fmt.Errorf("PostgreSQL not ready after %s", postgresWaitTimeout)
		}
		select {
		case <-ctx.Done():
			return log, ctx.Err()
		case <-time.After(postgresWaitInterval):
		}
	}
}

// DockerService is one container's live status, as reported by
// `docker compose ps`. State is Docker's raw container state ("running",
// "exited", ...); Health is the healthcheck status ("healthy", "starting",
// "unhealthy") or "" for a container/image with no healthcheck defined --
// all three services in deploy/docker-compose.yml (postgres/redis/minio)
// declare one, so "" in practice means Docker hasn't reported yet.
type DockerService struct {
	Name   string `json:"name"`   // compose service name, e.g. "postgres"
	State  string `json:"state"`
	Health string `json:"health"`
}

// dockerComposePsRow mirrors the fields `docker compose ps --format json`
// emits (one JSON object per line, Compose v2's ndjson convention -- NOT a
// single JSON array).
type dockerComposePsRow struct {
	Service string `json:"Service"`
	State   string `json:"State"`
	Health  string `json:"Health"`
}

// DockerStatus reports the live state of every service in
// deploy/docker-compose.yml, for the admin panel's "Services" tab. Returns
// an empty slice (not an error) when the compose file doesn't exist, same
// convention as ensureDocker.
func (m *Manager) DockerStatus(ctx context.Context) ([]DockerService, error) {
	composeFile := filepath.Join(m.Root, "deploy", "docker-compose.yml")
	if _, err := os.Stat(composeFile); os.IsNotExist(err) {
		return nil, nil
	}
	st := m.loadState()
	cmd := exec.CommandContext(ctx, "docker", "compose", "-f", composeFile, "ps", "--all", "--format", "json")
	cmd.Dir = m.Root
	cmd.Env = append(os.Environ(),
		"TELESRV_DOCKER_PROJECT="+st.DockerProject,
		"TELESRV_DOCKER_PREFIX="+st.DockerPrefix,
	)
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("docker compose ps failed: %w", err)
	}
	var services []DockerService
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var row dockerComposePsRow
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			continue
		}
		services = append(services, DockerService{Name: row.Service, State: row.State, Health: row.Health})
	}
	return services, nil
}

// CheckUpdates fetches from the remote and reports how many commits the
// local branch is behind its upstream, WITHOUT pulling or building anything
// -- the Services tab's "Check updates" button uses this to decide whether
// to offer a real Update (GitPull + rebuild + restart) or tell the operator
// they're already current.
func (m *Manager) CheckUpdates(ctx context.Context) (int, error) {
	fetchCmd := exec.CommandContext(ctx, "git", "fetch")
	fetchCmd.Dir = m.Root
	hideWindow(fetchCmd)
	if out, err := fetchCmd.CombinedOutput(); err != nil {
		return 0, fmt.Errorf("git fetch failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	countCmd := exec.CommandContext(ctx, "git", "rev-list", "--count", "HEAD..@{upstream}")
	countCmd.Dir = m.Root
	hideWindow(countCmd)
	out, err := countCmd.Output()
	if err != nil {
		return 0, fmt.Errorf("git rev-list failed: %w", err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, fmt.Errorf("parse commit count: %w", err)
	}
	return n, nil
}

// --- build steps ---------------------------------------------------------

// GitPull runs `git pull --ff-only`, deliberately never a real merge -- see
// the identical reasoning in server-panel.py's git_pull().
func (m *Manager) GitPull(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "pull", "--ff-only")
	cmd.Dir = m.Root
	hideWindow(cmd)
	out, err := cmd.CombinedOutput()
	log := "$ git pull --ff-only\n" + string(out)
	return log, err
}

// buildServer builds only bin/owpengram-server.
func (m *Manager) buildServer(ctx context.Context) (string, error) {
	return m.goBuild(ctx, m.serverExe(), "./cmd/telesrv")
}

// buildBoth builds bin/owpengram-server and bin/owpengram-admin-panel, like
// server-panel.py's build(). Used by Update, which leaves a fresh admin
// binary on disk even though it doesn't self-restart into it (see package
// doc).
func (m *Manager) buildBoth(ctx context.Context) (string, error) {
	serverLog, err := m.buildServer(ctx)
	if err != nil {
		return serverLog, err
	}
	adminLog, err := m.goBuild(ctx, m.adminExe(), "./cmd/telesrv-admin")
	return serverLog + "\n" + adminLog, err
}

func (m *Manager) goBuild(ctx context.Context, outPath, pkg string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return "", fmt.Errorf("mkdir bin: %w", err)
	}
	cmd := exec.CommandContext(ctx, "go", "build", "-o", outPath, pkg)
	cmd.Dir = m.Root
	hideWindow(cmd)
	out, err := cmd.CombinedOutput()
	return fmt.Sprintf("$ go build -o %s %s\n%s", filepath.Base(outPath), pkg, string(out)), err
}

// --- high-level actions ----------------------------------------------------

// Restart rebuilds BOTH bin/owpengram-server and bin/owpengram-admin-panel
// from the current working tree (no git pull -- see Update for that) and
// relaunches owpengram-server, which then bounces the admin panel onto its
// freshly built binary. Never self-restarts the admin process handling this
// request directly -- see HandlePendingAdminRestart's doc comment for why
// that handoff happens from the newly launched server instead. Returns a
// combined build/relaunch log for the admin UI.
func (m *Manager) Restart(ctx context.Context) (string, error) {
	st := m.loadState()
	if pidAlive(st.ServerPID) {
		killPID(st.ServerPID)
	}
	dockerLog, err := m.ensureDocker(ctx, st)
	if err != nil {
		return dockerLog, err
	}
	buildLog, err := m.buildBoth(ctx)
	fullLog := dockerLog + "\n" + buildLog
	if err != nil {
		return fullLog, fmt.Errorf("build failed: %w", err)
	}
	pid, err := m.launch(m.serverExe(), m.serverLog())
	if err != nil {
		return fullLog, fmt.Errorf("launch failed: %w", err)
	}
	st.ServerPID = pid
	// Ask the process we just launched to bounce the admin panel for us
	// once it's up -- see HandlePendingAdminRestart's doc comment for why
	// that's the safe side of this handoff to do it from.
	st.PendingAdminRestart = true
	if err := m.saveState(st); err != nil {
		return fullLog, fmt.Errorf("save state: %w", err)
	}
	return fullLog + fmt.Sprintf("\nowpengram-server relaunched, pid=%d. Admin panel will restart shortly onto its freshly built binary.\n", pid), nil
}

// Update is Restart plus a `git pull --ff-only` first, so a fresh checkout
// gets built instead of whatever's already on disk.
func (m *Manager) Update(ctx context.Context) (string, error) {
	pullLog, err := m.GitPull(ctx)
	if err != nil {
		return pullLog, fmt.Errorf("git pull failed: %w", err)
	}
	st := m.loadState()
	if pidAlive(st.ServerPID) {
		killPID(st.ServerPID)
	}
	dockerLog, err := m.ensureDocker(ctx, st)
	fullLog := pullLog + "\n" + dockerLog
	if err != nil {
		return fullLog, err
	}
	buildLog, err := m.buildBoth(ctx)
	fullLog = fullLog + "\n" + buildLog
	if err != nil {
		return fullLog, fmt.Errorf("build failed: %w", err)
	}
	pid, err := m.launch(m.serverExe(), m.serverLog())
	if err != nil {
		return fullLog, fmt.Errorf("launch failed: %w", err)
	}
	st.ServerPID = pid
	st.PendingAdminRestart = true
	if err := m.saveState(st); err != nil {
		return fullLog, fmt.Errorf("save state: %w", err)
	}
	return fullLog + fmt.Sprintf("\nowpengram-server relaunched, pid=%d. Admin panel will restart shortly onto its freshly built binary.\n", pid), nil
}

// HandlePendingAdminRestart is called once by owpengram-server itself, right
// after it confirms it's up and serving (see cmd/telesrv/main.go's
// OnServing hook) -- never by the admin panel on itself. That ordering is
// the whole point: by the time this runs, the *new* owpengram-server
// process already exists and is unrelated to whatever admin panel process
// is currently running, so killing the old admin PID and launching a new
// one here carries none of the risk self-restarting mid-HTTP-request would
// (see the package doc). A no-op when no restart was requested.
func (m *Manager) HandlePendingAdminRestart(ctx context.Context) (bool, error) {
	st := m.loadState()
	if !st.PendingAdminRestart {
		return false, nil
	}
	if pidAlive(st.AdminPID) {
		killPID(st.AdminPID)
	}
	pid, err := m.launch(m.adminExe(), m.adminLog())
	if err != nil {
		return false, fmt.Errorf("launch admin panel: %w", err)
	}
	st.AdminPID = pid
	st.PendingAdminRestart = false
	if err := m.saveState(st); err != nil {
		return true, fmt.Errorf("save state: %w", err)
	}
	return true, nil
}

// --- .env.example / .env editing -------------------------------------------

var (
	activeFieldRe    = regexp.MustCompile(`^(TELESRV_[A-Z0-9_]+)=(.*)$`)
	commentedFieldRe = regexp.MustCompile(`^#\s*(TELESRV_[A-Z0-9_]+)=(.*)$`)
	sensitiveKeyRe   = regexp.MustCompile(`(PASSWORD|SECRET|_TOKEN|API_KEY)`)
	groupHeaderRe    = regexp.MustCompile(`^##\s*(.+?)\s*--\s*(.+)$`)
	sectionBreakRe   = regexp.MustCompile(`^#\s*={10,}\s*$`)
)

type EnvField struct {
	Key              string `json:"key"`
	DefaultValue     string `json:"default_value"`
	Description      string `json:"description"`
	EnabledByDefault bool   `json:"enabled_by_default"`
	Sensitive        bool   `json:"sensitive"`
	// Value is the field's current effective value: from .env when set,
	// otherwise DefaultValue (only when EnabledByDefault), else empty --
	// exactly current_env_values()'s semantics in server-panel.py.
	Value string `json:"value"`
}

type EnvGroup struct {
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Fields      []EnvField `json:"fields"`
}

// ReadEnvGroups parses .env.example into the same panel-visible groups
// server-panel.py's parse_env_template() does (identical header/format
// rules -- see that function's docstring), then fills in each field's
// current effective value from .env.
func (m *Manager) ReadEnvGroups() ([]EnvGroup, error) {
	tmplPath := filepath.Join(m.Root, ".env.example")
	tmplData, err := os.ReadFile(tmplPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read .env.example: %w", err)
	}
	envValues, err := m.readEnvFile()
	if err != nil {
		return nil, err
	}

	var groups []EnvGroup
	var current *EnvGroup
	var pending []string
	inCommentRun := false
	seen := map[string]bool{}

	appendField := func(key, defaultValue, description string, enabledByDefault bool) {
		if current == nil || seen[key] {
			return
		}
		seen[key] = true
		value, has := envValues[key]
		if !has {
			if enabledByDefault {
				value = defaultValue
			} else {
				value = ""
			}
		}
		current.Fields = append(current.Fields, EnvField{
			Key:              key,
			DefaultValue:     defaultValue,
			Description:      description,
			EnabledByDefault: enabledByDefault,
			Sensitive:        sensitiveKeyRe.MatchString(key),
			Value:            value,
		})
	}

	for _, raw := range strings.Split(string(tmplData), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			pending = nil
			inCommentRun = false
			continue
		}
		if h := groupHeaderRe.FindStringSubmatch(line); h != nil {
			groups = append(groups, EnvGroup{Title: strings.TrimSpace(h[1]), Description: strings.TrimSpace(h[2])})
			current = &groups[len(groups)-1]
			pending = nil
			inCommentRun = false
			continue
		}
		if sectionBreakRe.MatchString(line) {
			current = nil
			pending = nil
			inCommentRun = false
			continue
		}
		if a := activeFieldRe.FindStringSubmatch(line); a != nil {
			appendField(a[1], a[2], strings.Join(pending, " "), true)
			inCommentRun = false
			continue
		}
		if strings.HasPrefix(line, "#") {
			if c := commentedFieldRe.FindStringSubmatch(line); c != nil {
				appendField(c[1], c[2], strings.Join(pending, " "), false)
				inCommentRun = false
				continue
			}
			text := strings.TrimSpace(strings.TrimLeft(line, "#"))
			if inCommentRun {
				pending = append(pending, text)
			} else {
				pending = []string{text}
			}
			inCommentRun = true
			continue
		}
		inCommentRun = false
	}

	out := groups[:0]
	for _, g := range groups {
		if len(g.Fields) > 0 {
			out = append(out, g)
		}
	}
	return out, nil
}

func (m *Manager) readEnvFile() (map[string]string, error) {
	values := map[string]string{}
	data, err := os.ReadFile(filepath.Join(m.Root, ".env"))
	if os.IsNotExist(err) {
		return values, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read .env: %w", err)
	}
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx <= 0 {
			continue
		}
		values[line[:idx]] = strings.TrimSpace(line[idx+1:])
	}
	return values, nil
}

// WriteEnvValues rewrites .env from .env.example's exact text, substituting
// each known key's value in place -- see save_env()'s docstring in
// server-panel.py for why this (not a fresh key=value dump) is what
// preserves comments/layout. Only keys present in values are touched; a
// template-commented optional field is uncommented when given a non-empty
// value and left as-is when given an empty one.
func (m *Manager) WriteEnvValues(values map[string]string) error {
	tmplPath := filepath.Join(m.Root, ".env.example")
	tmplData, err := os.ReadFile(tmplPath)
	if err != nil {
		return fmt.Errorf("read .env.example: %w", err)
	}
	lines := strings.Split(string(tmplData), "\n")
	// Split() on a trailing "\n" leaves one empty trailing element; drop it
	// so the join below doesn't add a spurious blank line before the final
	// newline this function appends anyway.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	out := make([]string, 0, len(lines))
	seen := map[string]bool{}
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if a := activeFieldRe.FindStringSubmatch(line); a != nil {
			if v, ok := values[a[1]]; ok && !seen[a[1]] {
				seen[a[1]] = true
				out = append(out, a[1]+"="+v)
				continue
			}
		}
		if c := commentedFieldRe.FindStringSubmatch(line); c != nil {
			if v, ok := values[c[1]]; ok && !seen[c[1]] {
				seen[c[1]] = true
				if v != "" {
					out = append(out, c[1]+"="+v)
				} else {
					out = append(out, raw)
				}
				continue
			}
		}
		out = append(out, raw)
	}
	return os.WriteFile(filepath.Join(m.Root, ".env"), []byte(strings.Join(out, "\n")+"\n"), 0o644)
}
