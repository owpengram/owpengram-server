// Command telesrv-ctl is a standalone, Go-only front end for
// internal/procctl.Manager -- the same process-control code the admin web
// panel already calls into for Restart/Update, given a plain main() so a
// self-hoster (or a launcher script) can drive start/stop/restart/status/
// logs/update from a terminal without Docker or Python being involved at
// all. Run with no arguments from a real terminal (a double-clicked
// owpengram-server.bat/start.bat/start.sh included) and it drops into an
// interactive menu after the initial start, covering everything
// tui-panel/server-panel.py's own Start/Stop/Restart/Update/Logs bindings
// do. tui-panel remains available only for what this menu doesn't cover
// yet -- a live dashboard and an in-place .env editor (the admin web
// panel's Server Settings page already covers .env editing too).
package main

import (
	"bufio"
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
	// script. When that bare invocation is also a real terminal (as
	// opposed to a script piping/redirecting stdin, or `telesrv-ctl start`
	// typed explicitly), it then drops into the interactive menu instead
	// of just exiting -- see cmdMenu.
	args := flag.Args()
	command := "start"
	var commandArgs []string
	bareInvocation := len(args) == 0
	if len(args) > 0 {
		command = args[0]
		commandArgs = args[1:]
	}

	m := procctl.NewManager(*root)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch command {
	case "start":
		if bareInvocation && isInteractiveTerminal() {
			err = cmdMenu(ctx, m)
		} else {
			err = cmdStart(ctx, m)
		}
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
	case "set-edition":
		err = cmdSetEdition(m, commandArgs)
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
  start        bootstrap .env on a fresh install (asking once whether to
               use Docker for PostgreSQL/MinIO, or run a fully portable
               install with an embedded PostgreSQL and no Docker at all),
               then build and launch whatever isn't already running
               (safe to run repeatedly). Run with no arguments at all from
               a real terminal and this is followed by an interactive menu
               (stop/restart/status/update/logs/edition) instead of exiting.
  stop         stop owpengram-server and owpengram-admin-panel
  restart      rebuild and relaunch both from the current working tree
  status       show whether each process (and, if Docker is in use, each
               container) is up
  logs         print the current run's startup log
  update       git pull --ff-only, then rebuild and relaunch both
  set-edition  standard|portable -- change the edition choice made at
               start without re-prompting for it

`)
}

func cmdStart(ctx context.Context, m *procctl.Manager) error {
	generatedPassword, err := m.BootstrapEnv()
	if err != nil {
		return fmt.Errorf("bootstrap .env: %w", err)
	}
	if err := resolveEdition(m); err != nil {
		return err
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
	return nil
}

// cmdMenu runs cmdStart once (identical to a bare `telesrv-ctl` call today),
// then loops an interactive menu covering the actions a self-hoster
// previously had to leave this binary for: stop/restart/status/update/
// logs/edition, the same set tui-panel/server-panel.py's own Start/Stop/
// Restart/Update/Logs key bindings cover. Only reached for a genuinely
// bare invocation on a real terminal (see main()) -- `telesrv-ctl start`
// typed explicitly, or any non-interactive invocation (a script, a
// launcher running headless), still just starts and returns, unchanged.
func cmdMenu(ctx context.Context, m *procctl.Manager) error {
	if err := cmdStart(ctx, m); err != nil {
		fmt.Fprintln(os.Stderr, "telesrv-ctl:", err)
	}

	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Println()
		fmt.Println("== OwpenGram control menu ==")
		st := m.Status()
		fmt.Printf("  owpengram-server:       %s\n", aliveLabel(st.ServerAlive, st.ServerPID))
		fmt.Printf("  owpengram-admin-panel:  %s\n", aliveLabel(st.AdminAlive, st.AdminPID))
		fmt.Println()
		fmt.Println("  1) Start")
		fmt.Println("  2) Stop")
		fmt.Println("  3) Restart")
		fmt.Println("  4) Status (incl. Docker containers, if in use)")
		fmt.Println("  5) Update (git pull --ff-only, rebuild, relaunch)")
		fmt.Println("  6) Logs (current run's startup log)")
		fmt.Println("  7) Change edition (standard/portable)")
		fmt.Println("  8) Exit")
		fmt.Print("\nChoice: ")

		line, readErr := reader.ReadString('\n')
		choice := strings.TrimSpace(line)

		var actionErr error
		switch choice {
		case "1":
			actionErr = cmdStart(ctx, m)
		case "2":
			actionErr = cmdStop(m)
		case "3":
			actionErr = cmdRestart(ctx, m)
		case "4":
			actionErr = cmdStatus(ctx, m)
		case "5":
			actionErr = cmdUpdate(ctx, m)
		case "6":
			actionErr = cmdLogs(m)
		case "7":
			actionErr = menuChangeEdition(m, reader)
		case "8", "q", "quit", "exit":
			return nil
		case "":
			if readErr != nil {
				// stdin closed (e.g. piped in from something that ran dry)
				// -- leave quietly instead of spinning on empty reads.
				return nil
			}
			continue
		default:
			fmt.Printf("Unrecognized choice %q\n", choice)
			continue
		}
		if actionErr != nil {
			fmt.Fprintln(os.Stderr, "telesrv-ctl:", actionErr)
		}
		fmt.Print("\nPress Enter to continue...")
		reader.ReadString('\n')
	}
}

// menuChangeEdition is option 7's prompt -- a thin interactive wrapper
// around cmdSetEdition so the menu doesn't need the caller to already know
// the standard/portable argument syntax.
func menuChangeEdition(m *procctl.Manager, reader *bufio.Reader) error {
	current, ok := m.Edition()
	if ok {
		fmt.Printf("Current edition: %s\n", current)
	}
	fmt.Println("  1) Standard -- PostgreSQL and MinIO run in Docker")
	fmt.Println("  2) Portable -- embedded PostgreSQL and local disk storage, no Docker at all")
	fmt.Print("Choice: ")
	line, _ := reader.ReadString('\n')
	switch strings.TrimSpace(line) {
	case "1", "standard":
		return cmdSetEdition(m, []string{"standard"})
	case "2", "portable":
		return cmdSetEdition(m, []string{"portable"})
	default:
		fmt.Println("Not a recognized choice -- edition left unchanged.")
		return nil
	}
}

func cmdSetEdition(m *procctl.Manager, args []string) error {
	if len(args) != 1 || (args[0] != "standard" && args[0] != "portable") {
		return fmt.Errorf("usage: telesrv-ctl set-edition standard|portable")
	}
	if err := m.SetEdition(args[0]); err != nil {
		return err
	}
	fmt.Printf("Edition set to %q. Run `telesrv-ctl restart` to apply it.\n", args[0])
	return nil
}

// resolveEdition makes sure TELESRV_EDITION is set before Start ever needs
// it, asking interactively when stdin is a real terminal and no choice was
// made yet, or picking automatically (and saying so) for a non-interactive
// run -- e.g. a scripted install, or a launcher double-click on a console
// that hasn't attached a real terminal.
func resolveEdition(m *procctl.Manager) error {
	if _, ok := m.Edition(); ok {
		return nil
	}
	dockerAvailable := m.DockerAvailable()
	// Docker available: "standard" is the existing default behaviour, most
	// self-hosters already have Docker if they got this far. No Docker:
	// "portable" needs nothing else installed at all.
	recommended := "standard"
	if !dockerAvailable {
		recommended = "portable"
	}

	var edition string
	if isInteractiveTerminal() {
		fmt.Println("How should this install get PostgreSQL and blob storage?")
		if dockerAvailable {
			fmt.Println("  1) Standard (recommended) -- PostgreSQL and MinIO run in Docker")
		} else {
			fmt.Println("  1) Standard -- needs Docker, which was not found on PATH; install it to use this")
		}
		fmt.Println("  2) Portable -- embedded PostgreSQL and local disk storage, no Docker at all")
		fmt.Printf("Choice [%s]: ", editionMenuDefault(recommended))
		reader := bufio.NewReader(os.Stdin)
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		switch line {
		case "":
			edition = recommended
		case "1", "standard":
			if !dockerAvailable {
				return fmt.Errorf("docker was not found on PATH -- install Docker, or choose 2 (portable)")
			}
			edition = "standard"
		case "2", "portable":
			edition = "portable"
		default:
			return fmt.Errorf("unrecognized choice %q", line)
		}
	} else {
		edition = recommended
		fmt.Printf("[cfg] No TTY attached -- defaulting to edition %q (%s). Change later with `telesrv-ctl set-edition standard|portable`.\n",
			edition, map[bool]string{true: "Docker available", false: "Docker not found"}[dockerAvailable])
	}

	if err := m.SetEdition(edition); err != nil {
		return fmt.Errorf("save edition: %w", err)
	}
	return nil
}

func editionMenuDefault(edition string) string {
	if edition == "standard" {
		return "1"
	}
	return "2"
}

// isInteractiveTerminal reports whether stdin looks like a real terminal
// rather than a pipe/redirect -- the no-extra-dependency way to do this in
// Go (golang.org/x/term isn't otherwise a dependency of this repo).
func isInteractiveTerminal() bool {
	stat, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return stat.Mode()&os.ModeCharDevice != 0
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
