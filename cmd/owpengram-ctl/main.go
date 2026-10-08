// Command owpengram-ctl is the whole owpengram server as one foreground
// program: it runs the admin panel and owpengram-server, starts the embedded
// PostgreSQL when that is what was chosen, and keeps them running until it is
// stopped (Ctrl+C, closing its window, or `owpengram-ctl stop`). Everything
// else -- where PostgreSQL and media live, the server's name, mail, operators
// -- is set in the admin panel; see internal/procctl for how the pieces fit.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"telesrv/internal/procctl"
)

func main() {
	root := flag.String("root", ".", "project root (the folder containing bin/, .env and data/)")
	flag.Usage = usage
	flag.Parse()

	args := flag.Args()
	command := "run"
	if len(args) > 0 {
		command = args[0]
	}

	absRoot, err := filepath.Abs(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "owpengram-ctl:", err)
		os.Exit(1)
	}
	// Relative paths in .env (data/..., logs/...) are meant against the root,
	// for this process and for the ones it starts.
	if err := os.Chdir(absRoot); err != nil {
		fmt.Fprintln(os.Stderr, "owpengram-ctl:", err)
		os.Exit(1)
	}
	m := procctl.NewManager(absRoot)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch command {
	case "run", "start":
		err = cmdRun(ctx, m)
	case "stop":
		err = cmdStop(m)
	case "restart":
		err = cmdRestart(m)
	case "status":
		err = cmdStatus(m)
	case "logs":
		err = cmdLogs(m)
	case "update":
		err = cmdUpdate(ctx, m)
	default:
		fmt.Fprintf(os.Stderr, "owpengram-ctl: unknown command %q\n\n", command)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "owpengram-ctl:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `owpengram-ctl -- runs the OwpenGram server and its admin panel

Usage:
  owpengram-ctl [-root PATH] [command]

Commands:
  run      (default) start the admin panel and the server and keep them
           running until this program is stopped. On a fresh install it
           creates .env and prints the admin panel address and first password;
           everything else is configured in the admin panel.
  stop     stop a running owpengram-ctl (and with it the server and admin panel)
  restart  restart the server and the admin panel of a running owpengram-ctl
  status   show whether owpengram-ctl, the server and the admin panel are up
  logs     print the current run's startup log
  update   git pull, rebuild, and restart the server and admin panel of a
           running owpengram-ctl
`)
}

func cmdRun(ctx context.Context, m *procctl.Manager) error {
	generatedPassword, err := m.BootstrapEnv()
	if err != nil {
		return fmt.Errorf("bootstrap .env: %w", err)
	}

	if groups, err := m.ReadEnvGroups(); err == nil {
		if url, ok := procctl.AdminUIURL(groups); ok {
			fmt.Printf("Admin panel: %s\n", url)
			if generatedPassword != "" {
				fmt.Printf("Login: %s\n", procctl.AdminBreakGlassUsername)
				fmt.Printf("Initial admin password: %s\n", generatedPassword)
			}
		} else {
			fmt.Println("[WARN] TELESRV_ADMIN_UI_ADDR is not set -- can't show the admin panel address.")
		}
	}
	fmt.Println("Press Ctrl+C to stop the server and the admin panel.")

	logf := func(format string, args ...any) {
		fmt.Printf("%s  %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
	}
	if err := m.Run(ctx, logf); err != nil {
		return err
	}
	fmt.Println("Stopped.")
	return nil
}

func cmdStop(m *procctl.Manager) error {
	wasRunning, err := m.RequestStop(60 * time.Second)
	if err != nil {
		return err
	}
	if !wasRunning {
		fmt.Println("owpengram-ctl is not running.")
		return nil
	}
	fmt.Println("Stopped.")
	return nil
}

func cmdRestart(m *procctl.Manager) error {
	if !m.Status().CtlAlive {
		return fmt.Errorf("owpengram-ctl is not running -- start it with `owpengram-ctl`")
	}
	m.StopChildren()
	fmt.Println("Restarting the server and the admin panel.")
	return nil
}

func cmdUpdate(ctx context.Context, m *procctl.Manager) error {
	log, err := m.Update(ctx)
	fmt.Println(strings.TrimSpace(log))
	if err != nil {
		return err
	}
	// The supervisor starts both again from the rebuilt binaries.
	if st := m.Status(); st.CtlAlive {
		m.StopChildren()
		fmt.Println("Restarting the server and the admin panel.")
	} else {
		fmt.Println("owpengram-ctl is not running; the new build is used the next time it starts.")
	}
	return nil
}

func cmdStatus(m *procctl.Manager) error {
	st := m.Status()
	fmt.Printf("owpengram-ctl:            %s\n", aliveLabel(st.CtlAlive, st.CtlPID))
	fmt.Printf("owpengram-server:       %s\n", aliveLabel(st.ServerAlive, st.ServerPID))
	fmt.Printf("owpengram-admin-panel:  %s\n", aliveLabel(st.AdminAlive, st.AdminPID))
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
