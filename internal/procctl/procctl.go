// Package procctl runs owpengram as one foreground program. owpengram-ctl
// (cmd/owpengram-ctl) calls Manager.Run, which starts the admin panel at once
// and owpengram-server as soon as the first-run wizard has chosen where its
// data lives, starts the embedded PostgreSQL when that was chosen, and starts
// whichever of them stops again. Everything stops with it.
//
// The admin panel cannot restart anything itself, and does not have to: the
// supervisor is the parent, so "restart the server" is just killing its
// process (RestartServer), and "restart the admin panel" is that process
// exiting on its own after it has answered the request.
//
// The shared .server_panel.json records the PIDs so the admin panel and
// `owpengram-ctl status/stop` can find the processes without being their parent.
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
	// CtlPID is the owpengram-ctl supervisor that owns the other two.
	CtlPID    int `json:"ctl_pid,omitempty"`
	ServerPID int `json:"server_pid"`
	AdminPID  int `json:"admin_pid"`
}

func (m *Manager) loadState() State {
	var st State
	data, err := os.ReadFile(filepath.Join(m.Root, stateFileName))
	if err == nil {
		_ = json.Unmarshal(data, &st)
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
	CtlPID      int
	CtlAlive    bool
	ServerPID   int
	ServerAlive bool
	AdminPID    int
	AdminAlive  bool
}

func (m *Manager) Status() Status {
	st := m.loadState()
	return Status{
		CtlPID:      st.CtlPID,
		CtlAlive:    pidAlive(st.CtlPID),
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

// killPID stops exactly one process -- never its children, so a process
// that is not the supervisor's own child cannot take the supervisor down
// with it. On Unix a TERM first, then a KILL after a second.
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

// startupMarker is the first line owpengram-server logs on every launch
// (cmd/telesrv/main.go), before config/migrations/media seed/OnServing.
// logs/owpengram-server.log is opened in append mode and never rotated or
// truncated across restarts (see launch()), so a freshly launched process's
// own lines sit after its predecessor's in the same file -- this is what
// tells them apart.
const startupMarker = "telesrv starting"

// StartupLogTail returns the current owpengram-server run's log lines, from
// its own startup marker onward -- so a restart's progress view shows only
// what the process now starting has done, not leftover lines from whatever
// ran in this file before it. Nil (not an error) when the log doesn't exist
// yet, which is normal in the instant right after launch() creates it.
func (m *Manager) StartupLogTail() ([]string, error) {
	data, err := os.ReadFile(m.serverLog())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(lines[i], startupMarker) {
			return lines[i:], nil
		}
	}
	return nil, nil
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

// goBuild rebuilds outPath from pkg, unless this install has no Go source
// tree to build from at all -- a release archive (see
// scripts/build-release.sh) ships prebuilt bin/owpengram-server and
// bin/owpengram-admin-panel binaries plus this same owpengram-ctl, but none
// of the actual ./cmd/... source, so a "go build" here would either fail
// outright (no go.mod) or, if the user happens to also have Go installed,
// fail confusingly (no such package). In that case the existing binary
// already on disk *is* the build; only report an error if it's missing too.
func (m *Manager) goBuild(ctx context.Context, outPath, pkg string) (string, error) {
	if !m.hasSourceTree() {
		if _, err := os.Stat(outPath); err == nil {
			return fmt.Sprintf("$ %s already present (prebuilt release install, no source tree to rebuild from)\n", filepath.Base(outPath)), nil
		}
		return "", fmt.Errorf("%s is missing and there is no source tree at %s to build it from -- redownload the release archive", filepath.Base(outPath), m.Root)
	}
	if _, err := exec.LookPath("go"); err != nil {
		if _, statErr := os.Stat(outPath); statErr == nil {
			return fmt.Sprintf("$ %s already present (Go toolchain not found, keeping existing binary)\n", filepath.Base(outPath)), nil
		}
		return "", fmt.Errorf("%s is missing and Go is not installed to build it -- install Go from https://go.dev/dl/", filepath.Base(outPath))
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return "", fmt.Errorf("mkdir bin: %w", err)
	}
	cmd := exec.CommandContext(ctx, "go", "build", "-o", outPath, pkg)
	cmd.Dir = m.Root
	hideWindow(cmd)
	out, err := cmd.CombinedOutput()
	return fmt.Sprintf("$ go build -o %s %s\n%s", filepath.Base(outPath), pkg, string(out)), err
}

// hasSourceTree reports whether this install has a Go module checked out at
// all (a git clone) as opposed to being a prebuilt release archive (bin/ +
// data/ + .env.example, no go.mod, no cmd/ sources).
func (m *Manager) hasSourceTree() bool {
	_, err := os.Stat(filepath.Join(m.Root, "go.mod"))
	return err == nil
}

// --- high-level actions ----------------------------------------------------

// Update pulls the checkout and rebuilds both binaries. It does not start
// anything: the supervisor runs the new binaries when RestartServer (and the
// admin panel's own exit) make it launch them again.
func (m *Manager) Update(ctx context.Context) (string, error) {
	pullLog, err := m.GitPull(ctx)
	if err != nil {
		return pullLog, fmt.Errorf("git pull failed: %w", err)
	}
	buildLog, err := m.buildBoth(ctx)
	if err != nil {
		return pullLog + "\n" + buildLog, fmt.Errorf("build failed: %w", err)
	}
	return pullLog + "\n" + buildLog, nil
}

// RestartServer stops owpengram-server; the supervisor starts it again with
// the current .env and binary. It is a no-op error when no supervisor is
// running, because then nothing would start it again.
func (m *Manager) RestartServer() error {
	st := m.loadState()
	if !pidAlive(st.CtlPID) {
		return fmt.Errorf("owpengram-ctl is not running, so nothing would start the server again -- start it with owpengram-ctl")
	}
	if !pidAlive(st.ServerPID) {
		return nil
	}
	killPID(st.ServerPID)
	return nil
}

// --- .env.example / .env editing -------------------------------------------

var (
	activeFieldRe    = regexp.MustCompile(`^(TELESRV_[A-Z0-9_]+)=(.*)$`)
	commentedFieldRe = regexp.MustCompile(`^#\s*(TELESRV_[A-Z0-9_]+)=(.*)$`)
	sensitiveKeyRe   = regexp.MustCompile(`(PASSWORD|SECRET|_TOKEN|API_KEY)`)
	// sensitiveKeyExceptRe excludes names that trip sensitiveKeyRe on the
	// word "SECRET" while meaning Telegram's secret-chat feature, not a
	// credential (e.g. TELESRV_SECRET_CHAT_DELETE_FILE_AFTER_DOWNLOAD) --
	// there's nothing to mask there, it's a plain boolean toggle.
	sensitiveKeyExceptRe = regexp.MustCompile(`SECRET_CHAT`)
	groupHeaderRe        = regexp.MustCompile(`^##\s*(.+?)\s*--\s*(.+)$`)
	sectionBreakRe       = regexp.MustCompile(`^#\s*={10,}\s*$`)
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
			Sensitive:        sensitiveKeyRe.MatchString(key) && !sensitiveKeyExceptRe.MatchString(key),
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
// preserves comments/layout. Only keys present in values are set to a new
// value; every other key keeps whatever is already in the current .env
// (falling back to the template's own default only for a key .env never
// set) -- previously this fell straight back to the template default for
// any key not in this particular save's payload, silently wiping out every
// other customized setting (e.g. TELESRV_ADMIN_UI_PASSWORD) on every save
// that only touches one section's keys. A template-commented optional field
// is uncommented when given a non-empty value and left as-is when given an
// empty one.
func (m *Manager) WriteEnvValues(values map[string]string) error {
	tmplPath := filepath.Join(m.Root, ".env.example")
	tmplData, err := os.ReadFile(tmplPath)
	if err != nil {
		return fmt.Errorf("read .env.example: %w", err)
	}
	existing, err := m.readEnvFile()
	if err != nil {
		return err
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
		if a := activeFieldRe.FindStringSubmatch(line); a != nil && !seen[a[1]] {
			if v, ok := values[a[1]]; ok {
				seen[a[1]] = true
				out = append(out, a[1]+"="+v)
				continue
			}
			if v, ok := existing[a[1]]; ok {
				seen[a[1]] = true
				out = append(out, a[1]+"="+v)
				continue
			}
		}
		if c := commentedFieldRe.FindStringSubmatch(line); c != nil && !seen[c[1]] {
			if v, ok := values[c[1]]; ok {
				seen[c[1]] = true
				if v != "" {
					out = append(out, c[1]+"="+v)
				} else {
					out = append(out, raw)
				}
				continue
			}
			// A previously-enabled optional field shows up in the current
			// .env as an active line even though the template still has it
			// commented out -- keep it enabled with its existing value.
			if v, ok := existing[c[1]]; ok {
				seen[c[1]] = true
				out = append(out, c[1]+"="+v)
				continue
			}
		}
		out = append(out, raw)
	}
	return os.WriteFile(filepath.Join(m.Root, ".env"), []byte(strings.Join(out, "\n")+"\n"), 0o644)
}

// StopChildren stops owpengram-server and owpengram-admin-panel; the
// supervisor starts both again, from whatever binaries are on disk by then.
func (m *Manager) StopChildren() {
	st := m.loadState()
	for _, pid := range []int{st.ServerPID, st.AdminPID} {
		if pidAlive(pid) {
			killPID(pid)
		}
	}
}
