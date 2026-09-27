package procctl

import "os/exec"

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
func (m *Manager) SetEdition(edition string) error {
	return m.WriteEnvValues(map[string]string{"TELESRV_EDITION": edition})
}
