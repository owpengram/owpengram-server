package procctl

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// DockerAvailable reports whether the docker CLI is reachable on PATH --
// used by cmd/telesrv-ctl's edition picker to know whether "standard"
// (Docker-backed) is actually usable before recommending it.
func (m *Manager) DockerAvailable() bool {
	_, err := exec.LookPath("docker")
	return err == nil
}

// Edition selects how this install gets PostgreSQL and blob storage: see
// internal/config.Config.Edition's doc comment for the full picture ("standard"
// runs Postgres/MinIO in Docker; "portable" runs an embedded PostgreSQL owned
// by owpengram-server itself and forces localfs blob storage -- no Docker
// at all). Unlike the reverted "run owpengram-server/owpengram-admin-panel
// themselves as containers" experiment, this only changes where the
// *infrastructure* lives, not where the two binaries run -- so it needs no
// container orchestration, no cross-process networking, none of that
// class of bug.
//
// Edition reads the persisted TELESRV_EDITION choice from .env. ok is false
// when it's unset or not a recognized value -- callers should then resolve
// one (cmd/telesrv-ctl's interactive picker, or an automatic default for a
// non-interactive run) and persist it with SetEdition.
func (m *Manager) Edition() (edition string, ok bool) {
	values, err := m.readEnvFile()
	if err != nil {
		return "", false
	}
	switch values["TELESRV_EDITION"] {
	case "standard", "portable":
		return values["TELESRV_EDITION"], true
	default:
		return "", false
	}
}

// SetEdition persists the chosen edition to .env, so a future start/restart
// doesn't need to ask again.
//
// Choosing "portable" also pins TELESRV_BLOB_BACKEND to localfs, because
// internal/config.Load forces exactly that anyway (there is no MinIO in the
// portable edition) -- leaving a stale "s3" in .env only bought a scary
// "s3 blob backend configured but failed to initialize" warning on every
// single start, describing a backend that was never going to be used.
// Switching from portable back to "standard" restores s3, the standard
// edition's default: the pin above is this function's own doing, so leaving
// it in place would keep writing new media to disk under TELESRV_BLOB_DIR
// while the MinIO container sits unused. Media written to disk in the
// meantime stays readable -- cmd/telesrv registers the local filesystem as
// the additional backend whenever s3 is active, and file_blobs.backend is
// recorded per row.
func (m *Manager) SetEdition(edition string) error {
	values := map[string]string{"TELESRV_EDITION": edition}
	switch edition {
	case "portable":
		values["TELESRV_BLOB_BACKEND"] = "localfs"
	case "standard":
		if current, ok := m.Edition(); ok && current == "portable" {
			values["TELESRV_BLOB_BACKEND"] = "s3"
		}
	}
	return m.WriteEnvValues(values)
}

// embeddedPostgresDataDir resolves TELESRV_EMBEDDED_POSTGRES_DIR exactly
// the way internal/config does -- default data/postgres, relative paths
// against the checkout root.
func (m *Manager) embeddedPostgresDataDir() string {
	dir := ""
	if values, err := m.readEnvFile(); err == nil {
		dir = values["TELESRV_EMBEDDED_POSTGRES_DIR"]
	}
	if dir == "" {
		dir = "data/postgres"
	}
	if filepath.IsAbs(dir) {
		return dir
	}
	return filepath.Join(m.Root, dir)
}

// StopEmbeddedPostgres gracefully stops the portable edition's embedded
// PostgreSQL, if one is running for this checkout.
//
// killPID deliberately never tree-kills (see its doc comment: neither
// owpengram-server nor owpengram-admin-panel is reliably the other's
// ancestor, so a tree-kill either way can take out the process doing the
// killing). That leaves the embedded postmaster -- a genuine child of
// owpengram-server -- alive after the server itself is gone, still holding
// TELESRV_EMBEDDED_POSTGRES_PORT. So every Stop/Restart/Update pairs the
// server kill with this: a real `pg_ctl stop -m fast`, which is also
// strictly better than any kill would be, since the database gets a clean
// checkpoint instead of crash recovery on the next start.
//
// Silent no-op outside the portable edition, or when there's nothing
// running (no postmaster.pid, no downloaded runtime yet). Best-effort:
// errors are ignored, because embeddedpg.Start's own stopStaleInstance is
// the backstop that makes the next start work regardless.
func (m *Manager) StopEmbeddedPostgres() {
	if edition, ok := m.Edition(); !ok || edition != "portable" {
		return
	}
	dir := m.embeddedPostgresDataDir()
	if _, err := os.Stat(filepath.Join(dir, "data", "postmaster.pid")); err != nil {
		return
	}
	pgCtl := filepath.Join(dir, "runtime", "bin", "pg_ctl")
	if runtime.GOOS == "windows" {
		pgCtl += ".exe"
	}
	if _, err := os.Stat(pgCtl); err != nil {
		return
	}
	cmd := exec.Command(pgCtl, "stop", "-D", filepath.Join(dir, "data"), "-m", "fast")
	hideWindow(cmd)
	_ = cmd.Run()
}

// DisplayEditionName maps the value persisted in .env (TELESRV_EDITION,
// still "standard" internally) to the name shown to a human: "classic"
// reads better than "standard" now that portable is the recommended
// default, and changing the .env value itself would break every install
// that already has TELESRV_EDITION=standard set. Shared by telesrv-ctl's
// plain-text output and internal/panel's TUI, so neither shows a
// different name for the same edition than the other.
func DisplayEditionName(edition string) string {
	if edition == "standard" {
		return "classic"
	}
	return edition
}
