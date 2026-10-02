// Command telesrv-ctl is a standalone, Go-only front end for
// internal/procctl.Manager -- the same process-control code the admin web
// panel already calls into for Restart/Update, given a plain main() so a
// self-hoster (or a launcher script) can drive start/stop/restart/status/
// logs/update from a terminal without Docker or Python being involved at
// all. Run with no arguments from a real terminal (a double-clicked
// owpengram-server.bat/start.bat/start.sh included) and it drops into
// internal/panel's interactive TUI after the initial start: a live
// dashboard, start/stop/restart/update, and an edition switch. Log
// viewing (this binary's own non-interactive `logs` subcommand still
// covers that) and .env editing stay the admin web panel's job -- see
// internal/panel's own package doc comment for why.
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

	"telesrv/internal/panel"
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
		err = cmdSetEdition(ctx, m, commandArgs)
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
  set-edition  portable|classic [--blobs=move|keep] [--force] -- switch
               edition, carrying the database over to the other edition's
               PostgreSQL and restarting. Media files move to the new
               edition's storage (--blobs=move, the default) or stay where
               they are and are read from there (--blobs=keep; classic
               only -- portable has no MinIO). The other edition's old data
               is replaced. --force skips media files that cannot be moved
               instead of aborting, and allows --blobs=keep for portable.

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
		if url, ok := procctl.AdminUIURL(groups); ok {
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
// then opens internal/panel's interactive dashboard: start/stop/restart/
// update/edition, the control actions tui-panel/server-panel.py's own
// Start/Stop/Restart/Update key bindings covered (its log viewer and .env
// editor are deliberately not replicated here -- see internal/panel's
// package doc comment). Only reached for a genuinely bare invocation on a
// real terminal (see main()) -- `telesrv-ctl start` typed explicitly, or
// any non-interactive invocation (a script, a launcher running headless),
// still just starts and returns, unchanged.
func cmdMenu(ctx context.Context, m *procctl.Manager) error {
	if err := cmdStart(ctx, m); err != nil {
		fmt.Fprintln(os.Stderr, "telesrv-ctl:", err)
	}
	// panel.Run takes the terminal into raw mode for the rest of this
	// process's life -- nothing after this point may read os.Stdin itself
	// (see cmdStart/resolveEdition's own bufio.Reader use, which is why
	// that runs to completion above, before this call, not after it).
	return panel.Run(ctx, m)
}

// cmdSetEdition accepts "classic" as the documented spelling for the
// Docker-backed edition and "standard" as a silent alias (the actual value
// persisted to .env -- see displayEditionName's doc comment for why that
// doesn't change).
//
// Goes through ChangeEdition rather than SetEdition so the data moves with
// the choice: the edition decides which PostgreSQL the server talks to, and
// changing it on its own leaves every existing client unknown to the
// database it now connects to.
func cmdSetEdition(ctx context.Context, m *procctl.Manager, args []string) error {
	edition := ""
	opts := procctl.EditionChange{}
	for _, arg := range args {
		switch arg {
		case "--force", "-f":
			opts.Force = true
		case "--blobs=move":
			opts.Blobs = procctl.BlobsMove
		case "--blobs=keep":
			opts.Blobs = procctl.BlobsKeep
		case "classic", "standard":
			edition = "standard"
		case "portable":
			edition = "portable"
		default:
			return fmt.Errorf("usage: telesrv-ctl set-edition portable|classic [--blobs=move|keep] [--force]")
		}
	}
	if edition == "" {
		return fmt.Errorf("usage: telesrv-ctl set-edition portable|classic [--blobs=move|keep] [--force]")
	}

	if err := m.ChangeEdition(ctx, edition, opts, func(text string) {
		fmt.Print(strings.TrimRight(text, "\n") + "\n")
	}); err != nil {
		return err
	}
	fmt.Printf("Now running the %q edition.\n", procctl.DisplayEditionName(edition))
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
	// Portable is the recommended default regardless of whether Docker
	// happens to be available: it needs nothing else installed at all, and
	// is the simpler path for most self-hosters. Classic (Docker) remains
	// a fully supported choice for anyone who wants PostgreSQL/MinIO as
	// separate containers -- just no longer the one offered first.
	recommended := "portable"

	var edition string
	if isInteractiveTerminal() {
		fmt.Println("How should this install get PostgreSQL and blob storage?")
		fmt.Println("  1) Portable (recommended) -- embedded PostgreSQL and local disk storage, no Docker at all")
		if dockerAvailable {
			fmt.Println("  2) Classic -- PostgreSQL and MinIO run in Docker")
		} else {
			fmt.Println("  2) Classic -- needs Docker, which was not found on PATH; install it to use this")
		}
		fmt.Printf("Choice [%s]: ", editionMenuDefault(recommended))
		reader := bufio.NewReader(os.Stdin)
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		switch line {
		case "":
			edition = recommended
		case "1", "portable":
			edition = "portable"
		case "2", "classic", "standard":
			if !dockerAvailable {
				return fmt.Errorf("docker was not found on PATH -- install Docker, or choose 1 (portable)")
			}
			edition = "standard"
		default:
			return fmt.Errorf("unrecognized choice %q", line)
		}
	} else {
		edition = recommended
		fmt.Printf("[cfg] No TTY attached -- defaulting to edition %q. Change later with `telesrv-ctl set-edition portable|classic`.\n",
			procctl.DisplayEditionName(edition))
	}

	if err := m.SetEdition(edition); err != nil {
		return fmt.Errorf("save edition: %w", err)
	}
	return nil
}

func editionMenuDefault(edition string) string {
	if edition == "portable" {
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

// Admin UI URL display -- see internal/procctl.AdminUIURL, shared with
// internal/panel's TUI so the two never drift on how they show it.
