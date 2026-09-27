// Command telesrv-ctl is a standalone, Go-only front end for
// internal/procctl.Manager -- the same process-control code the admin web
// panel already calls into for Restart/Update, given a plain main() so a
// self-hoster (or a launcher script) can drive start/stop/restart/status/
// logs/update from a terminal without Docker or Python being involved at
// all. tui-panel/server-panel.py remains available as a richer, optional
// interactive alternative; this is the minimum every install can rely on.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"telesrv/internal/procctl"
)

func main() {
	root := flag.String("root", ".", "project root (the checkout containing bin/, deploy/, .env)")
	flag.Usage = usage
	flag.Parse()

	// No subcommand is the common case (a launcher script or a
	// double-clicked shortcut): behave like "start" -- bootstrap on a fresh
	// install, otherwise just make sure everything is up -- same default
	// entry point tui-panel/server-panel.py's quickstart() is for that
	// script.
	command := "start"
	if args := flag.Args(); len(args) > 0 {
		command = args[0]
	}

	m := procctl.NewManager(*root)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch command {
	case "start":
		err = cmdStart(ctx, m)
	case "stop":
		err = cmdStop(m)
	case "restart":
		err = cmdRestart(ctx, m)
	case "status":
		err = cmdStatus(ctx, m)
	case "logs":
		err = cmdLogs(m)
	case "update":
		err = cmdUpdate(ctx, m)
	default:
		fmt.Fprintf(os.Stderr, "telesrv-ctl: unknown command %q\n\n", command)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "telesrv-ctl:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `telesrv-ctl -- start/stop/restart/status/logs/update for owpengram-server

Usage:
  telesrv-ctl [-root PATH] <command>

Commands:
  start    bootstrap .env on a fresh install, then build and launch
           whatever isn't already running (safe to run repeatedly)
  stop     stop owpengram-server and owpengram-admin-panel
  restart  rebuild and relaunch both from the current working tree
  status   show whether each process (and, if Docker is in use, each
           container) is up
  logs     print the current run's startup log
  update   git pull --ff-only, then rebuild and relaunch both

`)
}

func cmdStart(ctx context.Context, m *procctl.Manager) error {
	generatedPassword, err := m.BootstrapEnv()
	if err != nil {
		return fmt.Errorf("bootstrap .env: %w", err)
	}

	fmt.Println("== Starting OwpenGram ==")
	log, startErr := m.Start(ctx)
	if strings.TrimSpace(log) != "" {
		fmt.Println(strings.TrimSpace(log))
	}
	if startErr != nil {
		return startErr
	}

	fmt.Println()
	groups, envErr := m.ReadEnvGroups()
	if envErr == nil {
		if url, ok := adminUIURL(groups); ok {
			fmt.Printf("Open %s to finish setting up your server.\n", url)
			if generatedPassword != "" {
				fmt.Printf("Login: %s\n", procctl.AdminBreakGlassUsername)
				fmt.Printf("Initial admin password: %s\n", generatedPassword)
			}
		} else {
			fmt.Println("[WARN] TELESRV_ADMIN_UI_ADDR is not set -- can't show the admin panel URL.")
		}
	}
	fmt.Println()
	fmt.Println("For the interactive TUI (stop/restart/logs/.env editing) instead: python tui-panel/server-panel.py panel")
	return nil
}

func cmdStop(m *procctl.Manager) error {
	fmt.Print(m.Stop())
	return nil
}

func cmdRestart(ctx context.Context, m *procctl.Manager) error {
	log, err := m.Restart(ctx)
	fmt.Println(strings.TrimSpace(log))
	return err
}

func cmdUpdate(ctx context.Context, m *procctl.Manager) error {
	log, err := m.Update(ctx)
	fmt.Println(strings.TrimSpace(log))
	return err
}

func cmdStatus(ctx context.Context, m *procctl.Manager) error {
	st := m.Status()
	fmt.Printf("owpengram-server:       %s\n", aliveLabel(st.ServerAlive, st.ServerPID))
	fmt.Printf("owpengram-admin-panel:  %s\n", aliveLabel(st.AdminAlive, st.AdminPID))

	services, err := m.DockerStatus(ctx)
	if err != nil {
		return fmt.Errorf("docker status: %w", err)
	}
	for _, svc := range services {
		health := svc.Health
		if health == "" {
			health = "-"
		}
		fmt.Printf("docker %-10s state=%-10s health=%s\n", svc.Name, svc.State, health)
	}
	return nil
}

func aliveLabel(alive bool, pid int) string {
	if alive {
		return fmt.Sprintf("running (pid=%d)", pid)
	}
	return "stopped"
}

func cmdLogs(m *procctl.Manager) error {
	lines, err := m.StartupLogTail()
	if err != nil {
		return err
	}
	for _, line := range lines {
		fmt.Println(line)
	}
	return nil
}

// adminUIURL rewrites TELESRV_ADMIN_UI_ADDR into something a browser can
// actually open, same as server-panel.py's browsable_host_port(): a
// wildcard bind (0.0.0.0, ::, empty) displays as loopback, since the bind
// itself is never something to type into a browser.
func adminUIURL(groups []procctl.EnvGroup) (string, bool) {
	addr := envGroupValue(groups, "TELESRV_ADMIN_UI_ADDR")
	if addr == "" {
		return "", false
	}
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		scheme, rest, _ := strings.Cut(addr, "://")
		netloc, slash, path := cutFirst(rest, "/")
		return scheme + "://" + browsableHostPort(netloc) + slash + path, true
	}
	return "http://" + browsableHostPort(addr), true
}

func cutFirst(s, sep string) (before, sepFound, after string) {
	if i := strings.Index(s, sep); i >= 0 {
		return s[:i], sep, s[i+len(sep):]
	}
	return s, "", ""
}

func browsableHostPort(hostPort string) string {
	s := strings.TrimSpace(hostPort)
	if strings.HasPrefix(s, "[") {
		closeIdx := strings.Index(s, "]")
		if closeIdx == -1 {
			return s
		}
		host, rest := s[1:closeIdx], s[closeIdx+1:]
		if host == "::" || host == "" {
			return "[::1]" + rest
		}
		return "[" + host + "]" + rest
	}
	lastColon := strings.LastIndex(s, ":")
	if lastColon < 0 {
		return s
	}
	host, port := s[:lastColon], s[lastColon+1:]
	switch host {
	case "0.0.0.0", "", "*":
		return "127.0.0.1:" + port
	case "::":
		return "[::1]:" + port
	default:
		return s
	}
}

func envGroupValue(groups []procctl.EnvGroup, key string) string {
	for _, g := range groups {
		for _, f := range g.Fields {
			if f.Key == key {
				return f.Value
			}
		}
	}
	return ""
}
