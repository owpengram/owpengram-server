#!/usr/bin/env python3
"""Control panel for the OwpenGram server: a non-interactive quickstart mode
and an interactive TUI, both built on the same ServerManager.

With no arguments (the default `owpengram-server.sh` / .bat invocation),
quickstart() bootstraps .env on a fresh install (generating the one secret
that has no safe default -- the admin panel password -- instead of asking
for it), brings up Docker, builds, launches both binaries, and prints the
admin panel URL. No prompts; first-time configuration (branding, SMTP,
changing that password) happens afterward in the web panel instead of here.

The `panel` argument instead opens the interactive TUI below: Docker naming
migration, Postgres naming migration, docker compose up, Go build, launching
owpengram-server / owpengram-admin-panel, all behind a menu, for anyone who
wants stop/restart/logs/.env editing from the terminal.

Starting the server launches both binaries as fully detached background
processes and writes their PIDs to .server_panel.json next to .env; closing
the panel (Exit) does NOT stop them -- only the Stop action does. Reopening
the panel later picks the same PIDs back up and reports live status.

Requires: pip install -r tui-panel/requirements-panel.txt
Run: owpengram-server.sh (Linux/macOS) or owpengram-server.bat (Windows) from
the repo root -- they check prerequisites first, then launch this.
"""
from __future__ import annotations

import json
import os
import platform
import re
import secrets
import shutil
import signal
import subprocess
import sys
import time
from collections.abc import Callable
from dataclasses import dataclass, field
from pathlib import Path

import psutil
from cryptography.hazmat.primitives.serialization import Encoding, PublicFormat, load_pem_private_key
from textual import work
from textual.app import App, ComposeResult
from textual.binding import Binding
from textual.containers import Horizontal, Vertical, VerticalScroll
from textual.screen import Screen
from textual.widgets import Button, Checkbox, Collapsible, DataTable, Footer, Header, Input, Label, OptionList, ProgressBar, RichLog, Static
from textual.widgets.option_list import Option

IS_WINDOWS = platform.system() == "Windows"

# psutil.cpu_percent()'s first call always returns a meaningless 0.0 baseline
# (it measures against process start); priming it once here means the first
# real reading in the stats timer is already a proper since-last-call delta.
psutil.cpu_percent(interval=None)

ROOT = Path(__file__).resolve().parent.parent
DEPLOY_DIR = ROOT / "deploy"
BIN_DIR = ROOT / "bin"
LOG_DIR = ROOT / "logs"
ENV_FILE = ROOT / ".env"
ENV_EXAMPLE_FILE = ROOT / ".env.example"
COMPOSE_FILE = DEPLOY_DIR / "docker-compose.yml"
STATE_FILE = ROOT / ".server_panel.json"

SERVER_EXE = BIN_DIR / ("owpengram-server.exe" if IS_WINDOWS else "owpengram-server")
ADMIN_EXE = BIN_DIR / ("owpengram-admin-panel.exe" if IS_WINDOWS else "owpengram-admin-panel")
SERVER_LOG = LOG_DIR / "owpengram-server.log"
ADMIN_LOG = LOG_DIR / "owpengram-admin-panel.log"

BANNER = r"""
   _____      _____ ___ _  _  ___ ___    _   __  __
  / _ \ \    / / _ \ __| \| |/ __| _ \  /_\ |  \/  |
 | (_) \ \/\/ /|  _/ _|| .` | (_ |   / / _ \| |\/| |
  \___/ \_/\_/ |_| |___|_|\_|\___|_|_\/_/ \_\_|  |_|
""".strip("\n")


# --- Process/state management -----------------------------------------------


def load_state() -> dict:
    if STATE_FILE.exists():
        try:
            return json.loads(STATE_FILE.read_text())
        except (OSError, json.JSONDecodeError):
            return {}
    return {}


def save_state(state: dict) -> None:
    STATE_FILE.write_text(json.dumps(state))


def pid_alive(pid: int | None) -> bool:
    if not pid:
        return False
    if IS_WINDOWS:
        out = subprocess.run(
            ["tasklist", "/FI", f"PID eq {pid}"],
            capture_output=True, text=True,
        ).stdout
        return str(pid) in out
    try:
        os.kill(pid, 0)
    except OSError:
        return False
    return True


def kill_pid(pid: int | None) -> None:
    if not pid:
        return
    if IS_WINDOWS:
        # /T kills the whole process tree, not just this PID -- matters when
        # the process was launched via a shell wrapper.
        subprocess.run(["taskkill", "/PID", str(pid), "/T", "/F"], capture_output=True)
        return
    try:
        os.killpg(pid, signal.SIGTERM)
    except OSError:
        try:
            os.kill(pid, signal.SIGTERM)
        except OSError:
            pass
    time.sleep(1)
    try:
        os.killpg(pid, signal.SIGKILL)
    except OSError:
        try:
            os.kill(pid, signal.SIGKILL)
        except OSError:
            pass


@dataclass
class Status:
    server_pid: int | None
    server_alive: bool
    admin_pid: int | None
    admin_alive: bool
    containers: list[tuple[str, str | None]]

    @property
    def running(self) -> bool:
        return self.server_alive or self.admin_alive


class ServerManager:
    """Owns the actual start/stop/build/launch mechanics. No Textual
    dependency in here on purpose, so it stays easy to reason about /
    reuse outside the TUI if that's ever useful."""

    def container_status(self, name: str) -> str | None:
        """Returns Docker's own State.Status (running/exited/created/...),
        or None if the container doesn't exist at all."""
        r = subprocess.run(
            ["docker", "inspect", "-f", "{{.State.Status}}", name],
            capture_output=True, text=True,
        )
        if r.returncode != 0:
            return None
        return r.stdout.strip() or None

    def status(self) -> Status:
        state = load_state()
        server_pid = state.get("server_pid")
        admin_pid = state.get("admin_pid")
        # Falls back to the cached naming decision (or the "owpengram"
        # default for a never-started, fresh install) so the container list
        # shows something sensible even before Start has ever run.
        prefix = self.cached_docker_naming() or state.get("docker_prefix") or "owpengram"
        containers = [
            (f"{prefix}-{service}", self.container_status(f"{prefix}-{service}"))
            for service in ("postgres", "redis")
        ]
        # MinIO is optional (only relevant when TELESRV_BLOB_BACKEND=s3 points
        # at the self-hosted container rather than AWS S3), so unlike
        # postgres/redis above it's only added to the list when the container
        # actually exists -- no "not created" row cluttering a deployment that
        # never uses it.
        minio_name = f"{prefix}-minio"
        minio_state = self.container_status(minio_name)
        if minio_state is not None:
            containers.append((minio_name, minio_state))
        return Status(
            server_pid=server_pid,
            server_alive=pid_alive(server_pid),
            admin_pid=admin_pid,
            admin_alive=pid_alive(admin_pid),
            containers=containers,
        )

    # -- naming migrations (interactive, must run with the real terminal) --
    #
    # Both migrations cache their decision in a plain state file the first
    # time they run. Reading that file directly and skipping the subprocess
    # (and the app.suspend()/resume dance it needs) whenever a decision is
    # already cached keeps suspend() out of the hot path for every ordinary
    # Start/Restart -- suspending and resuming Textual's terminal driver
    # repeatedly is what was leaving the menu unresponsive until the app was
    # fully restarted. It's now only exercised once per machine, the first
    # time there's an actual prompt to show.

    def cached_docker_naming(self) -> str | None:
        state_file = ROOT / ".docker_naming"
        if not state_file.exists():
            return None
        value = state_file.read_text().strip()
        return value if value in ("owpengram", "telesrv") else None

    def cached_db_naming(self) -> str | None:
        state_file = ROOT / ".db_naming"
        if not state_file.exists():
            return None
        value = state_file.read_text().strip()
        return value if value in ("owpengram", "telesrv") else None

    def resolve_docker_naming(self, run_interactive) -> tuple[str, str]:
        if IS_WINDOWS:
            cmd = [
                "powershell", "-NoProfile", "-ExecutionPolicy", "Bypass",
                "-File", str(DEPLOY_DIR / "migrate-docker-naming.ps1"),
            ]
        else:
            cmd = [
                "bash", str(DEPLOY_DIR / "migrate-docker-naming.sh"),
                str(COMPOSE_FILE), str(ROOT / ".docker_naming"),
            ]
        out = run_interactive(cmd)
        lines = [l.strip() for l in out.splitlines() if l.strip()]
        if len(lines) >= 2:
            return lines[0], lines[1]
        return "owpengram", "owpengram"

    def resolve_db_naming(self, run_interactive, container_name: str) -> None:
        if IS_WINDOWS:
            cmd = [
                "powershell", "-NoProfile", "-ExecutionPolicy", "Bypass",
                "-File", str(DEPLOY_DIR / "migrate-db-naming.ps1"),
                "-ContainerName", container_name,
            ]
        else:
            cmd = [
                "bash", str(DEPLOY_DIR / "migrate-db-naming.sh"),
                container_name, str(ENV_FILE), str(ROOT / ".db_naming"),
            ]
        run_interactive(cmd)

    # -- non-interactive steps (safe to run off the UI thread) --

    def docker_compose_up(self, project: str, prefix: str) -> tuple[bool, str]:
        env = os.environ.copy()
        env["TELESRV_DOCKER_PROJECT"] = project
        env["TELESRV_DOCKER_PREFIX"] = prefix
        r = subprocess.run(
            ["docker", "compose", "-f", str(COMPOSE_FILE), "up", "-d"],
            cwd=ROOT, env=env, capture_output=True, text=True,
        )
        return r.returncode == 0, (r.stdout + r.stderr)

    def docker_compose_stop(self, project: str, prefix: str) -> None:
        env = os.environ.copy()
        env["TELESRV_DOCKER_PROJECT"] = project
        env["TELESRV_DOCKER_PREFIX"] = prefix
        subprocess.run(
            ["docker", "compose", "-f", str(COMPOSE_FILE), "stop"],
            cwd=ROOT, env=env, capture_output=True,
        )

    def wait_postgres(self, prefix: str, timeout: float = 60) -> bool:
        deadline = time.time() + timeout
        while time.time() < deadline:
            r = subprocess.run(
                ["docker", "exec", f"{prefix}-postgres", "pg_isready", "-U", "telesrv", "-d", "telesrv"],
                capture_output=True,
            )
            if r.returncode == 0:
                return True
            time.sleep(2)
        return False

    def git_pull(self) -> tuple[bool, str]:
        """--ff-only, deliberately: this runs unattended from the panel, with
        no way to resolve a merge conflict or review a merge commit before it
        happens. A diverged branch or dirty tree just fails cleanly here
        instead of silently rewriting history or leaving a merge commit
        nobody asked for -- the operator resolves it by hand (git status)
        and re-runs Update."""
        r = subprocess.run(
            ["git", "pull", "--ff-only"],
            cwd=ROOT, capture_output=True, text=True,
        )
        ok = r.returncode == 0
        return ok, f"$ git pull --ff-only\n{r.stdout}{r.stderr}"

    def build(self) -> tuple[bool, str]:
        BIN_DIR.mkdir(exist_ok=True)
        log = []
        # cmd/telesrv-admin embeds web/dist via go:embed -- that dist/ is
        # committed to git, built manually (npm run build in
        # cmd/telesrv-admin/web) and staged like any other change whenever
        # the frontend changes. This step intentionally never touches it.
        for out_path, pkg in ((SERVER_EXE, "./cmd/telesrv"), (ADMIN_EXE, "./cmd/telesrv-admin")):
            r = subprocess.run(
                ["go", "build", "-o", str(out_path), pkg],
                cwd=ROOT, capture_output=True, text=True,
            )
            log.append(f"$ go build -o {out_path.name} {pkg}\n{r.stdout}{r.stderr}")
            if r.returncode != 0:
                return False, "\n".join(log)
        return True, "\n".join(log)

    def launch(self, exe_path: Path, log_path: Path) -> int:
        LOG_DIR.mkdir(exist_ok=True)
        logf = open(log_path, "ab")
        kwargs: dict = {}
        if IS_WINDOWS:
            kwargs["creationflags"] = subprocess.CREATE_NEW_PROCESS_GROUP | subprocess.DETACHED_PROCESS
        else:
            kwargs["start_new_session"] = True
        proc = subprocess.Popen(
            [str(exe_path)], cwd=ROOT,
            stdout=logf, stderr=subprocess.STDOUT, stdin=subprocess.DEVNULL,
            **kwargs,
        )
        logf.close()
        return proc.pid

    def stop(self) -> None:
        state = load_state()
        kill_pid(state.get("server_pid"))
        kill_pid(state.get("admin_pid"))
        project = state.get("docker_project", "owpengram")
        prefix = state.get("docker_prefix", "owpengram")
        self.docker_compose_stop(project, prefix)
        state["server_pid"] = None
        state["admin_pid"] = None
        save_state(state)


MANAGER = ServerManager()


# --- .env / clipboard / system stats helpers --------------------------------


def is_initialized() -> bool:
    """Whether this install has ever been through Setup. .env not existing
    yet is the one reliable signal -- everything else (bin/, containers)
    could plausibly be absent even on a configured install (e.g. right after
    `docker compose down` or before the first build)."""
    return ENV_FILE.exists()


def read_env_value(key: str) -> str | None:
    if not ENV_FILE.exists():
        return None
    prefix = f"{key}="
    for line in ENV_FILE.read_text(encoding="utf-8", errors="replace").splitlines():
        line = line.strip()
        if line.startswith(prefix):
            return line[len(prefix):].strip()
    return None


# --- .env.example parsing / editing ------------------------------------

_ACTIVE_FIELD_RE = re.compile(r"^(TELESRV_[A-Z0-9_]+)=(.*)$")
_COMMENTED_FIELD_RE = re.compile(r"^#\s*(TELESRV_[A-Z0-9_]+)=(.*)$")
_SENSITIVE_KEY_RE = re.compile(r"(PASSWORD|SECRET|_TOKEN|API_KEY)")
_GROUP_HEADER_RE = re.compile(r"^##\s*(.+?)\s*--\s*(.+)$")
_SECTION_BREAK_RE = re.compile(r"^#\s*={10,}\s*$")


@dataclass
class EnvField:
    key: str
    default_value: str
    description: str
    enabled_by_default: bool

    @property
    def sensitive(self) -> bool:
        return bool(_SENSITIVE_KEY_RE.search(self.key))


@dataclass
class EnvGroup:
    title: str
    description: str = ""
    fields: list[EnvField] = field(default_factory=list)


def parse_env_template() -> list[EnvGroup]:
    """Parses .env.example into panel-visible groups.

    Only fields inside an explicit "## Title -- description." header belong
    to a group and show up in the editor. A "# ====...====" banner line (the
    "Advanced / internal tuning" divider) ends panel-group collection for
    the rest of the file -- those fields are still perfectly valid config
    the server reads normally, they're just left out of the TUI on purpose
    to keep it to what a self-hoster actually needs to touch."""
    if not ENV_EXAMPLE_FILE.exists():
        return []

    groups: list[EnvGroup] = []
    current: EnvGroup | None = None
    pending: list[str] = []
    in_comment_run = False
    seen_keys: set[str] = set()

    for raw_line in ENV_EXAMPLE_FILE.read_text(encoding="utf-8", errors="replace").splitlines():
        stripped = raw_line.strip()

        if not stripped:
            pending = []
            in_comment_run = False
            continue

        header = _GROUP_HEADER_RE.match(stripped)
        if header:
            current = EnvGroup(title=header.group(1).strip(), description=header.group(2).strip())
            groups.append(current)
            pending = []
            in_comment_run = False
            continue

        if _SECTION_BREAK_RE.match(stripped):
            current = None
            pending = []
            in_comment_run = False
            continue

        active = _ACTIVE_FIELD_RE.match(stripped)
        if active:
            # TELESRV_AI_PROVIDERS legitimately appears twice in the file: once
            # as the real setting, once as an inline "if you want Kimi" example
            # inside an unrelated comment block further down. A second Input
            # for the same env var would just be confusing, so the first
            # occurrence wins and later repeats fold into descriptive text.
            if active.group(1) not in seen_keys:
                seen_keys.add(active.group(1))
                if current is not None:
                    current.fields.append(EnvField(
                        key=active.group(1), default_value=active.group(2),
                        description=" ".join(pending), enabled_by_default=True,
                    ))
            in_comment_run = False
            continue

        if stripped.startswith("#"):
            commented = _COMMENTED_FIELD_RE.match(stripped)
            if commented and commented.group(1) not in seen_keys:
                seen_keys.add(commented.group(1))
                if current is not None:
                    current.fields.append(EnvField(
                        key=commented.group(1), default_value=commented.group(2),
                        description=" ".join(pending), enabled_by_default=False,
                    ))
                in_comment_run = False
                continue
            text = stripped.lstrip("#").strip()
            if in_comment_run:
                pending.append(text)
            else:
                pending = [text]
            in_comment_run = True
            continue

        # Any other line (shouldn't normally happen in this file) just ends
        # whatever comment run was in progress without touching fields.
        in_comment_run = False

    return [g for g in groups if g.fields]


def current_env_values(groups: list[EnvGroup]) -> dict[str, str]:
    """Current value for every known field: from .env if it's set there,
    otherwise the template's default -- EXCEPT for a field that's commented
    out (disabled) in the template, whose "default_value" is only the
    example shown in the comment (e.g. "openai_responses"), not something
    that should silently start populated and get activated on save. Those
    start blank; the example still shows as the input's placeholder."""
    values: dict[str, str] = {}
    for group in groups:
        for f in group.fields:
            existing = read_env_value(f.key)
            if existing is not None:
                values[f.key] = existing
            elif f.enabled_by_default:
                values[f.key] = f.default_value
            else:
                values[f.key] = ""
    return values


def save_env(values: dict[str, str]) -> None:
    """(Re)writes .env from .env.example's exact text, substituting each
    known field's value in place -- this is what preserves every comment and
    the file's layout untouched. A template-commented optional field is
    uncommented when given a non-empty value, and left as-is (commented,
    disabled) when left empty."""
    out_lines = []
    seen_keys: set[str] = set()
    for raw_line in ENV_EXAMPLE_FILE.read_text(encoding="utf-8", errors="replace").splitlines():
        stripped = raw_line.strip()
        active = _ACTIVE_FIELD_RE.match(stripped)
        if active and active.group(1) in values and active.group(1) not in seen_keys:
            seen_keys.add(active.group(1))
            out_lines.append(f"{active.group(1)}={values[active.group(1)]}")
            continue
        commented = _COMMENTED_FIELD_RE.match(stripped)
        if commented and commented.group(1) in values and commented.group(1) not in seen_keys:
            seen_keys.add(commented.group(1))
            key = commented.group(1)
            value = values[key]
            out_lines.append(f"{key}={value}" if value else raw_line)
            continue
        out_lines.append(raw_line)
    ENV_FILE.write_text("\n".join(out_lines) + "\n", encoding="utf-8")


def missing_env_fields() -> list[tuple[str, str]]:
    """(key, default_value) pairs for every *active* (uncommented) field
    .env.example defines that .env doesn't have at all -- e.g. after a git
    pull brought in new TELESRV_* settings for features that didn't exist
    when this install's .env was first created.

    Deliberately scans the whole file, not just parse_env_template()'s
    panel-visible groups, so an Advanced-section field missing from .env
    gets caught too. Deliberately skips template-commented (disabled by
    default) fields -- those are meant to stay absent/off unless a self-hoster
    opts in, and the server already falls back to the same default shown in
    the comment when the key isn't set at all, so there's nothing to fix.
    A key already present in .env is never touched, even if blank -- clearing
    a field on purpose must never get silently reintroduced."""
    if not ENV_FILE.exists() or not ENV_EXAMPLE_FILE.exists():
        return []
    existing_keys: set[str] = set()
    for line in ENV_FILE.read_text(encoding="utf-8", errors="replace").splitlines():
        m = _ACTIVE_FIELD_RE.match(line.strip())
        if m:
            existing_keys.add(m.group(1))
    missing: list[tuple[str, str]] = []
    seen: set[str] = set()
    for line in ENV_EXAMPLE_FILE.read_text(encoding="utf-8", errors="replace").splitlines():
        m = _ACTIVE_FIELD_RE.match(line.strip())
        if m and m.group(1) not in existing_keys and m.group(1) not in seen:
            seen.add(m.group(1))
            missing.append((m.group(1), m.group(2)))
    return missing


def append_missing_env_fields(missing: list[tuple[str, str]]) -> None:
    """Appends (key, default_value) pairs to .env in one clearly-labeled,
    timestamped block, so a self-hoster immediately sees what was added and
    why. Purely additive -- never rewrites, reorders, or removes a single
    existing line, unlike save_env()'s full rewrite-from-template."""
    if not missing:
        return
    block = [
        "",
        f"# --- Added automatically by server-panel.py on "
        f"{time.strftime('%Y-%m-%d %H:%M')}: new fields found in .env.example "
        f"that this .env didn't have yet ---",
    ]
    block += [f"{key}={value}" for key, value in missing]
    with ENV_FILE.open("a", encoding="utf-8") as f:
        f.write("\n".join(block) + "\n")


def admin_ui_info() -> tuple[str, str | None] | None:
    """Returns (url, password) for the admin UI, or None if it isn't
    configured at all. password is None when TELESRV_ADMIN_UI_PASSWORD is
    empty (token-only auth)."""
    addr = read_env_value("TELESRV_ADMIN_UI_ADDR")
    if not addr:
        return None
    url = addr if addr.startswith(("http://", "https://")) else f"http://{addr}"
    return url, (read_env_value("TELESRV_ADMIN_UI_PASSWORD") or None)


def server_address() -> str | None:
    """The MTProto address clients connect to (advertise IP + listen port).
    Present as soon as .env is configured, even before the first Start."""
    ip = read_env_value("TELESRV_ADVERTISE_IP")
    listen = read_env_value("TELESRV_LISTEN")
    if not ip or not listen:
        return None
    port = listen.rsplit(":", 1)[-1]
    if not port.isdigit():
        return None
    return f"{ip}:{port}"


def server_public_key_pem() -> str | None:
    """The server's RSA public key (PKCS1 "RSA PUBLIC KEY" PEM, the format
    patched into client builds), derived from the private key file. That
    file is only generated the first time telesrv actually starts, so this
    is legitimately absent on a never-started install -- and returns None
    rather than raising on a missing/corrupt/unparseable file instead of
    crashing the panel over it."""
    key_path_value = read_env_value("TELESRV_RSA_KEY") or "data/server_rsa.pem"
    key_path = Path(key_path_value)
    if not key_path.is_absolute():
        key_path = ROOT / key_path
    if not key_path.exists():
        return None
    try:
        private_key = load_pem_private_key(key_path.read_bytes(), password=None)
        public_pem = private_key.public_key().public_bytes(Encoding.PEM, PublicFormat.PKCS1)
        return public_pem.decode("ascii").strip()
    except Exception:  # noqa: BLE001 - any parse/format issue just means "unavailable"
        return None


# --- quickstart (non-interactive: bootstrap .env, start, print the URL) ----
#
# The interactive Setup screen asks for four things a human has an opinion
# on (advertise IP, public URL, app scheme, admin password) plus two pure
# secrets it already generates without asking (TELESRV_ADMIN_API_TOKEN,
# TELESRV_ADMIN_SESSION_KEY). Of the four, three already have a working
# .env.example default (loopback/localhost) -- editable later from the web
# panel's Server Settings, which parses the same .env.example groups this
# does. The fourth, the admin password, is the one thing telesrv-admin
# refuses to boot without and can't default to something public, so
# quickstart generates that one too instead of blocking on terminal input.


def bootstrap_env() -> str | None:
    """Creates .env from .env.example if this is a fresh install. Returns
    the freshly generated admin password, or None if .env already existed
    (nothing was generated or touched -- an existing password is never read
    back for display, generated or not)."""
    if is_initialized():
        return None
    values = current_env_values(parse_env_template())
    admin_password = secrets.token_urlsafe(12)
    values["TELESRV_ADMIN_API_TOKEN"] = secrets.token_hex(32)
    values["TELESRV_ADMIN_SESSION_KEY"] = secrets.token_urlsafe(32)
    values["TELESRV_ADMIN_UI_PASSWORD"] = admin_password
    save_env(values)
    return admin_password


def _run_naming_helper(cmd: list[str]) -> str:
    """Runs a Docker/DB naming-migration helper directly against the real
    console. StartupProgressScreen's equivalent (_run_interactive) goes
    through self.app.suspend() because Textual owns the terminal at that
    point; quickstart never starts Textual, so there is nothing to suspend.
    Safe to call unconditionally, even on a first-ever run: both scripts
    only prompt when they find pre-existing 'telesrv_*' Docker state to
    migrate, and fall back cleanly rather than hang when stdin isn't a real
    terminal -- see their own doc comments."""
    proc = subprocess.run(cmd, cwd=ROOT, stdout=subprocess.PIPE, text=True)
    return proc.stdout or ""


def resolve_naming_headless() -> tuple[str, str]:
    cached = MANAGER.cached_docker_naming()
    if cached is not None:
        project, prefix = cached, cached
    else:
        project, prefix = MANAGER.resolve_docker_naming(_run_naming_helper)
    if prefix == "owpengram" and MANAGER.cached_db_naming() is None:
        MANAGER.resolve_db_naming(_run_naming_helper, f"{prefix}-postgres")
    return project, prefix


def quickstart() -> int:
    """The default entry point: bootstrap, start, print where to go, exit --
    no menu, no prompts. First-run configuration (branding, SMTP, the admin
    password itself) all happens afterward in the web admin panel instead
    of here; the interactive TUI (stop/restart/logs/.env editing) is still
    available with the 'panel' argument for anyone who wants it. Returns a
    process exit code."""
    generated_password = bootstrap_env()

    status = MANAGER.status()
    if status.running:
        print("[ok] Already running.")
    else:
        print("== Starting OwpenGram ==")
        print()
        try:
            project, prefix = resolve_naming_headless()
        except Exception as exc:  # noqa: BLE001 - report and exit, no traceback
            print(f"[ERROR] Docker naming resolution failed: {exc}")
            return 1

        print("[..] Starting Docker infrastructure...")
        ok, out = MANAGER.docker_compose_up(project, prefix)
        if not ok:
            print(f"[ERROR] docker compose up failed:\n{out.strip()}")
            return 1
        print("[ok] Docker infrastructure up.")

        print("[..] Waiting for PostgreSQL...")
        if not MANAGER.wait_postgres(prefix):
            print("[ERROR] PostgreSQL did not become ready within 60s.")
            return 1
        print("[ok] PostgreSQL ready.")

        print("[..] Building binaries (go build)...")
        ok, out = MANAGER.build()
        if not ok:
            print(f"[ERROR] Build failed:\n{out.strip()}")
            return 1
        print("[ok] Build complete.")

        print("[..] Launching owpengram-server and owpengram-admin-panel...")
        server_pid = MANAGER.launch(SERVER_EXE, SERVER_LOG)
        admin_pid = MANAGER.launch(ADMIN_EXE, ADMIN_LOG)
        save_state({
            "server_pid": server_pid,
            "admin_pid": admin_pid,
            "docker_project": project,
            "docker_prefix": prefix,
        })
        print("[ok] Launched.")

    print()
    info = admin_ui_info()
    if info is None:
        print("[WARN] TELESRV_ADMIN_UI_ADDR is not set -- can't show the admin panel URL.")
    else:
        url, _ = info
        print(f"Open {url} to finish setting up your server.")
        if generated_password:
            print(f"Initial admin password: {generated_password}")
            print("(create a named operator account, or change this one, from Operators in the admin panel)")
    print()
    print("For stop/restart/logs/.env editing from the terminal instead: owpengram-server.bat panel")
    return 0


def copy_to_clipboard(text: str) -> bool:
    if IS_WINDOWS:
        candidates = [["clip"]]
    elif platform.system() == "Darwin":
        candidates = [["pbcopy"]]
    else:
        candidates = [
            ["xclip", "-selection", "clipboard"],
            ["xsel", "--clipboard", "--input"],
            ["wl-copy"],
        ]
    for cmd in candidates:
        try:
            subprocess.run(cmd, input=text.encode("utf-8"), check=True, capture_output=True)
            return True
        except (FileNotFoundError, subprocess.CalledProcessError):
            continue
    return False


@dataclass
class SystemStats:
    cpu_percent: float
    ram_percent: float
    ram_used_gb: float
    ram_total_gb: float
    disk_percent: float
    disk_used_gb: float
    disk_total_gb: float


def system_stats() -> SystemStats:
    """CPU/RAM/disk usage for the drive this project lives on. RAM/disk carry
    used/total GB alongside the percentage -- the percentage alone doesn't say
    whether "83%" is 8GB of 10 or 800GB of 1000, and the progress bar widget
    itself already renders the percentage, so the label is where that actually
    useful number belongs."""
    gib = 1024**3
    cpu = psutil.cpu_percent(interval=None)
    vm = psutil.virtual_memory()
    du = psutil.disk_usage(str(ROOT))
    return SystemStats(
        cpu_percent=cpu,
        ram_percent=vm.percent,
        ram_used_gb=vm.used / gib,
        ram_total_gb=vm.total / gib,
        disk_percent=du.percent,
        disk_used_gb=du.used / gib,
        disk_total_gb=du.total / gib,
    )


# --- UI -----------------------------------------------------------------


class CopyButton(Static):
    """A clickable label that copies `value` to the clipboard on click. The
    actual value is never shown on screen -- just the label and a "click to
    copy" hint. value=None renders as "not available" and isn't clickable --
    used before the first Start, when e.g. the RSA public key file doesn't
    exist yet."""

    def __init__(self, label: str, value: str | None, on_copy_callback, **kwargs):
        super().__init__(**kwargs)
        self.label = label
        self.value = value
        self._on_copy_callback = on_copy_callback

    def on_mount(self) -> None:
        self._update_display()

    def _update_display(self) -> None:
        # NOTE: deliberately not named `_render` -- that name collides with
        # Widget's own internal `_render()` (returns a Visual, used by
        # get_content_height during layout); shadowing it with a method that
        # returns None broke height calculation as soon as anything forced a
        # reflow (e.g. pushing another screen), with a `'NoneType' object has
        # no attribute 'get_height'` crash.
        if self.value is None:
            self.update(f"{self.label}: [dim]not available[/]")
        else:
            self.update(f"{self.label}: [dim](click to copy)[/]")

    def set_value(self, value: str | None) -> None:
        """Updates the underlying value (e.g. after the env editor changes
        it) without ever having displayed the old one on screen."""
        self.value = value
        self._update_display()

    def on_click(self, event) -> None:
        if self.value is not None:
            self._on_copy_callback(self)


class LogTailScreen(Screen):
    """Live-tails one or two log files. Escape/b goes back to the main menu.

    RichLog renders through the terminal like everything else in this app,
    so plain mouse-drag text selection doesn't reach the terminal -- Textual
    captures the mouse for widget interaction instead. "c" copies everything
    shown so far (all panes, with headers) as a workaround, using the same
    clipboard/OSC 52 fallback as the CopyButton widgets."""

    BINDINGS = [
        Binding("escape", "back", "Back"),
        Binding("b", "back", "Back"),
        Binding("c", "copy_logs", "Copy logs"),
    ]

    def __init__(self, panes: list[tuple[str, Path]]):
        super().__init__()
        self._panes = panes
        self._offsets: dict[Path, int] = {path: 0 for _, path in panes}
        self._logs: dict[Path, RichLog] = {}
        self._buffers: dict[Path, list[str]] = {path: [] for _, path in panes}

    def compose(self) -> ComposeResult:
        yield Header()
        with Horizontal():
            for title, path in self._panes:
                with Vertical():
                    yield Label(f" {title} ({path.name}) ")
                    log = RichLog(highlight=False, markup=False, wrap=True)
                    self._logs[path] = log
                    yield log
        yield Footer()

    def on_mount(self) -> None:
        for title, path in self._panes:
            self._prime(path)
        self.set_interval(0.5, self._poll)

    def _prime(self, path: Path) -> None:
        # Seed each pane with the tail of the file instead of starting empty.
        if not path.exists():
            self._logs[path].write("(no logs yet)")
            return
        size = path.stat().st_size
        with open(path, "rb") as f:
            f.seek(max(0, size - 8000))
            data = f.read()
        self._offsets[path] = size
        text = data.decode("utf-8", errors="replace")
        if text:
            self._logs[path].write(text)
            self._buffers[path].append(text)

    def _poll(self) -> None:
        for _, path in self._panes:
            if not path.exists():
                continue
            size = path.stat().st_size
            offset = self._offsets[path]
            if size < offset:
                # Log file got rotated/truncated (e.g. new run started).
                offset = 0
            if size > offset:
                with open(path, "rb") as f:
                    f.seek(offset)
                    data = f.read()
                self._offsets[path] = size
                text = data.decode("utf-8", errors="replace")
                self._logs[path].write(text)
                self._buffers[path].append(text)

    def action_back(self) -> None:
        self.app.pop_screen()

    def action_copy_logs(self) -> None:
        parts = []
        for title, path in self._panes:
            content = "".join(self._buffers[path]).strip()
            parts.append(f"--- {title} ({path.name}) ---\n{content or '(no logs yet)'}")
        text = "\n\n".join(parts)
        if copy_to_clipboard(text):
            self.notify("Logs copied to clipboard")
            return
        self.app.copy_to_clipboard(text)
        self.notify("Logs copied to clipboard")


class LogPickerScreen(Screen):
    BINDINGS = [Binding("escape", "back", "Back")]

    def compose(self) -> ComposeResult:
        yield Header()
        yield OptionList(
            Option("owpengram-server logs", id="server"),
            Option("owpengram-admin-panel logs", id="admin"),
            Option("Both (split view)", id="both"),
            Option("Back", id="back"),
        )
        yield Footer()

    def action_back(self) -> None:
        self.app.pop_screen()

    def on_option_list_option_selected(self, event: OptionList.OptionSelected) -> None:
        option_id = event.option.id
        if option_id == "server":
            self.app.push_screen(LogTailScreen([("owpengram-server", SERVER_LOG)]))
        elif option_id == "admin":
            self.app.push_screen(LogTailScreen([("owpengram-admin-panel", ADMIN_LOG)]))
        elif option_id == "both":
            self.app.push_screen(LogTailScreen([
                ("owpengram-server", SERVER_LOG),
                ("owpengram-admin-panel", ADMIN_LOG),
            ]))
        else:
            self.app.pop_screen()


class EnvEditorScreen(Screen):
    """Every field .env.example knows about, grouped and described, with an
    Input per field. If .env doesn't exist yet, it's copied verbatim from
    .env.example first (so editing starts from the same defaults a fresh
    `cp .env.example .env` would have given you). Save rewrites .env from
    .env.example's exact text with just the values swapped in, so every
    comment and the file's layout survive untouched; Back discards unsaved
    edits."""

    BINDINGS = [
        Binding("escape", "back", "Back"),
        Binding("ctrl+s", "save", "Save"),
    ]

    def __init__(self) -> None:
        super().__init__()
        self._created_env = False
        if not ENV_FILE.exists() and ENV_EXAMPLE_FILE.exists():
            shutil.copy(ENV_EXAMPLE_FILE, ENV_FILE)
            self._created_env = True
        self._groups: list[EnvGroup] = parse_env_template()
        self._values: dict[str, str] = current_env_values(self._groups)

    def compose(self) -> ComposeResult:
        yield Header()
        yield Static("Environment configuration (.env)", id="env-title")
        if not self._groups:
            yield Static(
                "[b red]No .env.example found -- nothing to configure.[/]",
                id="env-missing",
            )
        else:
            with VerticalScroll(id="env-scroll"):
                for i, group in enumerate(self._groups):
                    with Collapsible(title=f"{group.title} ({len(group.fields)})", id=f"env-group-{i}"):
                        if group.description:
                            yield Static(group.description, classes="env-group-desc")
                        for f in group.fields:
                            with Vertical(classes="env-field"):
                                badge = "" if f.enabled_by_default else "  [dim i](optional, currently disabled)[/]"
                                yield Static(f"[b]{f.key}[/]{badge}", classes="env-field-key")
                                if f.description:
                                    yield Static(f.description, classes="env-field-desc")
                                yield Input(
                                    value=self._values.get(f.key, ""),
                                    placeholder=f.default_value if not f.enabled_by_default else "",
                                    password=f.sensitive,
                                    id=self._input_id(f.key),
                                )
            with Horizontal(id="env-actions"):
                yield Button("Save", id="env-save-button", variant="success")
                yield Button("Back", id="env-back-button", variant="default")
        yield Footer()

    @staticmethod
    def _input_id(key: str) -> str:
        return f"env-{key}"

    def on_mount(self) -> None:
        if self._created_env:
            self.notify("Created .env from .env.example")

    def on_button_pressed(self, event: Button.Pressed) -> None:
        if event.button.id == "env-save-button":
            self.action_save()
        elif event.button.id == "env-back-button":
            self.action_back()

    def action_save(self) -> None:
        if not self._groups:
            return
        values = dict(self._values)
        for group in self._groups:
            for f in group.fields:
                values[f.key] = self.query_one(f"#{self._input_id(f.key)}", Input).value
        try:
            save_env(values)
        except OSError as exc:
            self.notify(f"Failed to save .env: {exc}", severity="error", timeout=10)
            return
        self._values = values
        self.notify(".env saved")

    def action_back(self) -> None:
        self.dismiss()


@dataclass
class SetupField:
    key: str
    label: str
    description: str = ""
    required: bool = False
    secret: bool = False
    checkbox: bool = False


SETUP_FIELDS: list[tuple[str, list[SetupField]]] = [
    ("Server & Network", [
        SetupField(
            "TELESRV_ADVERTISE_IP", "Server public IP or hostname",
            "Your server's public IP address.",
            required=True,
        ),
    ]),
    ("Phone Login Codes", [
        SetupField(
            "TELESRV_DEV_AUTH_CODE", "Fixed dev code",
            "Accepted for every phone number as long as no webhook is set "
            "below -- fine for personal/testing use, but it's a well-known "
            "default, so change it if this server will be reachable by "
            "anyone else.",
        ),
        SetupField(
            "TELESRV_OTP_WEBHOOK_URL", "OTP webhook URL -- optional",
            "Leave blank to keep using the fixed dev code above. Fill in "
            "to send real, per-login codes to phone numbers via your own "
            "webhook endpoint instead (see docs/otp-delivery.md).",
        ),
        SetupField("TELESRV_OTP_WEBHOOK_SECRET", "OTP webhook secret", secret=True),
    ]),
    ("Public Links & Branding", [
        SetupField(
            "TELESRV_PUBLIC_BASE_URL", "Public base URL",
            "Used for links this server generates (invites, sticker packs). "
            "e.g. https://example.com",
            required=True,
        ),
        SetupField(
            "TELESRV_PUBLIC_APP_SCHEME", "Custom app link scheme",
            "Must match what your client builds were compiled with (e.g. owpg).",
            required=True,
        ),
        SetupField(
            "TELESRV_PUBLIC_APP_NAME", "Product name",
            "Shown on public landing pages.",
        ),
    ]),
    ("Email Login & Signup -- optional", [
        SetupField(
            "TELESRV_LOGIN_EMAIL_ENABLE", "Allow email as a login method",
            "Lets an existing phone-number account also add an email "
            "address for login codes. Needs the SMTP fields below filled "
            "in to actually deliver anything -- phone login keeps working "
            "either way.",
            checkbox=True,
        ),
        SetupField(
            "TELESRV_EMAIL_SIGNUP_ENABLE", "Register with email instead of a phone number",
            "Lets people sign up with just an email address. The account "
            "still gets a phone number under the hood (the protocol needs "
            "one), but it's a random one auto-generated from the prefixes "
            "below, not something the user provides. Also needs SMTP "
            "configured below to actually send codes.",
            checkbox=True,
        ),
        SetupField(
            "TELESRV_EMAIL_SIGNUP_PHONE_PREFIXES", "Random phone number prefixes",
            "Comma-separated prefixes used to generate that random phone "
            "number shown on email-signup accounts (e.g. \"888,380\") -- "
            "purely cosmetic, doesn't need a client update to change.",
        ),
        SetupField(
            "TELESRV_SMTP_HOST", "SMTP host",
            "Required to actually deliver email codes if either checkbox "
            "above is on. Leave every SMTP field blank to leave both "
            "unchecked for now -- phone login already works out of the "
            "box. You can always fill this in later from Configure .env.",
        ),
        SetupField("TELESRV_SMTP_PORT", "SMTP port"),
        SetupField("TELESRV_SMTP_USERNAME", "SMTP username"),
        SetupField("TELESRV_SMTP_PASSWORD", "SMTP password", secret=True),
        SetupField("TELESRV_SMTP_FROM", "SMTP from address"),
        SetupField(
            "TELESRV_SMTP_TLS", "SMTP encryption",
            "starttls, tls, or none -- use \"none\" only for a local test "
            "server like Mailpit that doesn't support encryption at all.",
        ),
    ]),
    ("Admin Panel", [
        SetupField(
            "TELESRV_ADMIN_UI_PASSWORD", "Admin panel password",
            "Required, and chosen by you -- unlike the API token and "
            "session key below, this is never auto-generated.",
            required=True, secret=True,
        ),
    ]),
]


class SetupWizardScreen(Screen):
    """Shown instead of Start/Stop on a fresh install (no .env yet). Asks
    only the handful of values that actually need a human decision; every
    other field gets its .env.example template default. The admin API token
    and session key are generated here automatically -- the admin *password*
    is deliberately not, since that's the one secret meant to be something
    the operator actually chose and remembers."""

    BINDINGS = [Binding("escape", "cancel", "Cancel")]

    def __init__(self) -> None:
        super().__init__()
        # Same template defaults the regular .env editor uses, so e.g. the
        # app scheme/name fields start pre-filled with "owpg"/"OwpenGram"
        # instead of empty -- fields with no sensible universal default
        # (advertise IP, base URL, admin password) just come back empty.
        self._template_values: dict[str, str] = current_env_values(parse_env_template())

    def compose(self) -> ComposeResult:
        yield Header()
        yield Static("First-time setup", id="env-title")
        yield Static(
            "This runs once. Fields marked [b]required[/] need an answer; "
            "everything else can be left blank and configured later from "
            "Configure .env. The admin API token and session key are "
            "generated automatically -- only the admin password is yours "
            "to choose.",
            id="setup-intro",
        )
        with VerticalScroll(id="env-scroll"):
            for title, fields in SETUP_FIELDS:
                with Vertical(classes="env-field-group"):
                    yield Static(title, classes="setup-section-title")
                    for f in fields:
                        with Vertical(classes="env-field"):
                            if f.checkbox:
                                checked = self._template_values.get(f.key, "").strip().lower() == "true"
                                yield Checkbox(f.label, value=checked, id=self._input_id(f.key))
                                if f.description:
                                    yield Static(f.description, classes="env-field-desc")
                            else:
                                badge = "  [b red](required)[/]" if f.required else ""
                                yield Static(f"[b]{f.label}[/]{badge}", classes="env-field-key")
                                if f.description:
                                    yield Static(f.description, classes="env-field-desc")
                                yield Input(
                                    value=self._template_values.get(f.key, ""),
                                    password=f.secret,
                                    id=self._input_id(f.key),
                                )
        with Horizontal(id="env-actions"):
            yield Button("Begin setup", id="setup-begin-button", variant="success")
            yield Button("Cancel", id="setup-cancel-button", variant="default")
        yield Footer()

    @staticmethod
    def _input_id(key: str) -> str:
        return f"setup-{key}"

    def on_button_pressed(self, event: Button.Pressed) -> None:
        if event.button.id == "setup-begin-button":
            self.action_begin()
        elif event.button.id == "setup-cancel-button":
            self.action_cancel()

    def action_cancel(self) -> None:
        self.dismiss(False)

    def action_begin(self) -> None:
        answers: dict[str, str] = {}
        missing: list[str] = []
        for _, fields in SETUP_FIELDS:
            for f in fields:
                if f.checkbox:
                    checked = self.query_one(f"#{self._input_id(f.key)}", Checkbox).value
                    answers[f.key] = "true" if checked else "false"
                    continue
                value = self.query_one(f"#{self._input_id(f.key)}", Input).value.strip()
                answers[f.key] = value
                if f.required and not value:
                    missing.append(f.label)
        if missing:
            self.notify(f"Please fill in: {', '.join(missing)}", severity="error", timeout=6)
            return

        values = dict(self._template_values)
        values.update(answers)

        webhook_configured = bool(answers.get("TELESRV_OTP_WEBHOOK_URL"))
        values["TELESRV_PHONE_CODE_DELIVERY_PROVIDER"] = "webhook" if webhook_configured else "development"

        values["TELESRV_ADMIN_API_TOKEN"] = secrets.token_hex(32)
        values["TELESRV_ADMIN_SESSION_KEY"] = secrets.token_urlsafe(32)

        try:
            save_env(values)
        except OSError as exc:
            self.notify(f"Failed to write .env: {exc}", severity="error", timeout=10)
            return

        self.dismiss(True)


START_STEPS: list[tuple[str, str]] = [
    ("docker", "Starting Docker infrastructure"),
    ("postgres", "Waiting for PostgreSQL"),
    ("build", "Building binaries"),
    ("launch", "Launching binaries"),
]
RESTART_STEPS: list[tuple[str, str]] = [
    ("stop", "Stopping current instance"),
    *START_STEPS,
]
UPDATE_STEPS: list[tuple[str, str]] = [
    ("pull", "Pulling latest code (git pull)"),
    *RESTART_STEPS,
]

_STEP_ICONS = {
    "active": "[b yellow]●[/]",
    "done": "[b green]✓[/]",
    "failed": "[b red]✗[/]",
}


class StartupProgressScreen(Screen):
    """Persistent step checklist shown during Start/Restart, replacing the
    old toast-only feedback. Not dismissible until the sequence finishes
    (success or failure) -- there's no Escape binding while it's running,
    just the Close button, which stays disabled until then."""

    def __init__(self, title: str, steps: list[tuple[str, str]], on_close: Callable[[bool], None] | None = None) -> None:
        super().__init__()
        self._title = title
        self._steps = steps
        self._labels = dict(steps)
        self._on_close = on_close
        self._success = False

    def compose(self) -> ComposeResult:
        yield Header()
        yield Static(self._title, id="startup-title")
        with Vertical(id="startup-steps"):
            for key, label in self._steps:
                yield Static(f"○ {label}", id=f"startup-step-{key}", classes="startup-step")
        yield Static("", id="startup-result")
        with Horizontal(id="startup-actions"):
            yield Button("Close", id="startup-close-button", variant="default", disabled=True)
        yield Footer()

    def set_step(self, key: str, status: str, detail: str = "") -> None:
        icon = _STEP_ICONS.get(status, "○")
        text = f"{icon} {self._labels.get(key, key)}"
        if detail:
            text += f"  [dim]{detail}[/]"
        try:
            self.query_one(f"#startup-step-{key}", Static).update(text)
        except Exception:  # noqa: BLE001 - screen may already be gone
            pass

    def finish(self, success: bool, message: str) -> None:
        self._success = success
        try:
            result = self.query_one("#startup-result", Static)
            result.update(f"[b green]{message}[/]" if success else f"[b red]{message}[/]")
            self.query_one("#startup-close-button", Button).disabled = False
        except Exception:  # noqa: BLE001 - screen may already be gone
            pass

    def on_button_pressed(self, event: Button.Pressed) -> None:
        if event.button.id == "startup-close-button":
            self.dismiss()
            if self._on_close:
                self._on_close(self._success)


class MainScreen(Screen):
    BINDINGS = [
        Binding("1", "start", "Start"),
        Binding("2", "stop", "Stop"),
        Binding("3", "restart", "Restart"),
        Binding("4", "update", "Update"),
        Binding("5", "logs", "Logs"),
        Binding("6", "env", "Configure .env"),
        Binding("q", "quit_panel", "Exit"),
    ]

    def __init__(self, auto_start: bool = False) -> None:
        super().__init__()
        self._auto_start = auto_start

    def compose(self) -> ComposeResult:
        yield Header()
        yield Static(BANNER, id="banner")
        with Horizontal(id="main-body"):
            with Vertical(id="services-panel"):
                yield Label("Services", classes="panel-title")
                yield DataTable(id="services-table", cursor_type="none")
            with Vertical(id="sidebar"):
                yield Label("System", classes="panel-title")
                yield Label("CPU", id="cpu-label", classes="stat-label")
                yield ProgressBar(total=100, id="cpu-bar", show_eta=False)
                yield Label("RAM", id="ram-label", classes="stat-label")
                yield ProgressBar(total=100, id="ram-bar", show_eta=False)
                yield Label("Disk", id="disk-label", classes="stat-label")
                yield ProgressBar(total=100, id="disk-bar", show_eta=False)
                with Vertical(id="server-info-section"):
                    yield Label("Server", classes="panel-title")
                    yield CopyButton("Address", server_address(), self._handle_copy_click, id="server-address")
                    yield CopyButton("Public key", server_public_key_pem(), self._handle_copy_click, id="server-public-key")
                    yield Label("Admin UI", classes="panel-title")
                    yield Static(id="admin-link")
                    yield self._admin_password_widget()
        if is_initialized():
            yield OptionList(
                Option("Start server", id="start"),
                Option("Stop server", id="stop"),
                Option("Restart server", id="restart"),
                Option("Update (git pull + rebuild + restart)", id="update"),
                Option("View logs", id="logs"),
                Option("Configure .env", id="env"),
                Option("Exit panel (server keeps running)", id="exit"),
            )
        else:
            yield OptionList(
                Option("Setup", id="setup"),
                Option("Exit panel", id="exit"),
            )
        yield Footer()

    def _admin_password_widget(self) -> CopyButton:
        info = admin_ui_info()
        password = info[1] if info else None
        return CopyButton("Password", password, self._handle_copy_click, id="admin-password")

    def _handle_copy_click(self, widget: CopyButton) -> None:
        if copy_to_clipboard(widget.value):
            self.notify(f"{widget.label} copied to clipboard")
            return
        # No local clipboard tool worked -- typically means this is a
        # headless remote server (e.g. reached over SSH), where
        # xclip/xsel/wl-copy have no X/Wayland display to talk to even if
        # they happen to be installed. Fall back to OSC 52: a terminal
        # escape sequence that most modern terminal emulators (Windows
        # Terminal, iTerm2, kitty, alacritty, WezTerm, ...) intercept and
        # copy straight into the *local* client's clipboard, even across
        # SSH.
        self.app.copy_to_clipboard(widget.value)
        self.notify(f"{widget.label} copied to clipboard")

    def on_mount(self) -> None:
        self.query_one("#services-table", DataTable).add_columns("Service", "Type", "Status")
        self._sync_missing_env_fields()
        self.refresh_status()
        self.refresh_stats()
        self.refresh_config_widgets()
        self.set_interval(3, self.refresh_status)
        self.set_interval(2, self.refresh_stats)
        self.set_interval(3, self.refresh_config_widgets)
        if self._auto_start:
            self.action_start()

    def _sync_missing_env_fields(self) -> None:
        """Catches .env falling behind .env.example -- e.g. a git pull that
        added new TELESRV_* settings for a feature this install predates.
        Runs once per panel launch, before Start, so a missing field never
        surprises the server at startup instead. A no-op on a fresh install
        (Setup already writes every field at once) and a no-op once .env has
        caught up."""
        missing = missing_env_fields()
        if not missing:
            return
        append_missing_env_fields(missing)
        keys = ", ".join(key for key, _ in missing)
        self.notify(
            f"Added {len(missing)} new field(s) to .env from .env.example: {keys}",
            timeout=10,
        )

    def refresh_status(self) -> None:
        status = MANAGER.status()
        table = self.query_one("#services-table", DataTable)
        table.clear()

        table.add_row(
            "owpengram-server", "binary",
            "[b green]● RUNNING[/]" if status.server_alive else "[dim]○ stopped[/]",
        )
        table.add_row(
            "owpengram-admin-panel", "binary",
            "[b green]● RUNNING[/]" if status.admin_alive else "[dim]○ stopped[/]",
        )
        for name, state in status.containers:
            if state == "running":
                cell = "[b green]● RUNNING[/]"
            elif state is None:
                cell = "[dim]○ not created[/]"
            else:
                cell = f"[b red]● {state.upper()}[/]"
            table.add_row(name, "container", cell)

        # Address/public key/admin UI info is only meaningful while the
        # server is actually up -- e.g. the admin UI address isn't reachable
        # at all when the process behind it isn't running.
        self.query_one("#server-info-section").display = status.server_alive

    def refresh_stats(self) -> None:
        stats = system_stats()
        self.query_one("#cpu-bar", ProgressBar).update(progress=stats.cpu_percent)
        self.query_one("#ram-bar", ProgressBar).update(progress=stats.ram_percent)
        self.query_one("#disk-bar", ProgressBar).update(progress=stats.disk_percent)
        # The bar itself already renders the percentage (ProgressBar's built-in
        # PercentageStatus) -- repeating it here would be the exact duplication
        # this label is for, so CPU (no byte total to show) is caption-only,
        # and RAM/Disk show used/total GB instead of the percent a second time.
        self.query_one("#cpu-label", Label).update("CPU")
        self.query_one("#ram-label", Label).update(
            f"RAM   {stats.ram_used_gb:.1f} / {stats.ram_total_gb:.1f} GB"
        )
        self.query_one("#disk-label", Label).update(
            f"Disk  {stats.disk_used_gb:.1f} / {stats.disk_total_gb:.1f} GB"
        )

    def refresh_admin_link(self) -> None:
        info = admin_ui_info()
        widget = self.query_one("#admin-link", Static)
        if info:
            url, _ = info
            widget.update(f'[link="{url}"]{url}[/link]')
        else:
            widget.update("[dim]not configured[/]")

    def refresh_config_widgets(self) -> None:
        """Re-reads .env-derived values into the already-mounted widgets --
        matters after coming back from the env editor, since e.g. the admin
        password or the server's advertise address may have just changed."""
        self.refresh_admin_link()
        info = admin_ui_info()
        self.query_one("#admin-password", CopyButton).set_value(info[1] if info else None)
        self.query_one("#server-address", CopyButton).set_value(server_address())
        self.query_one("#server-public-key", CopyButton).set_value(server_public_key_pem())

    def on_option_list_option_selected(self, event: OptionList.OptionSelected) -> None:
        option_id = event.option.id
        if option_id == "start":
            self.action_start()
        elif option_id == "stop":
            self.action_stop()
        elif option_id == "restart":
            self.action_restart()
        elif option_id == "update":
            self.action_update()
        elif option_id == "logs":
            self.action_logs()
        elif option_id == "env":
            self.action_env()
        elif option_id == "setup":
            self.action_setup()
        elif option_id == "exit":
            self.action_quit_panel()

    def action_setup(self) -> None:
        self.app.push_screen(SetupWizardScreen(), self._after_setup)

    def _after_setup(self, completed: bool | None) -> None:
        if completed:
            # A fresh MainScreen recomposes the OptionList as the full
            # Start/Stop/... menu now that .env exists, and immediately
            # kicks off the same first-start sequence the Start action uses.
            self.app.switch_screen(MainScreen(auto_start=True))

    def action_logs(self) -> None:
        self.app.push_screen(LogPickerScreen())

    def action_env(self) -> None:
        # Refresh once the editor screen is popped -- the admin password,
        # server address, etc. it just edited are all shown right here too.
        self.app.push_screen(EnvEditorScreen(), lambda _: self.refresh_config_widgets())

    def action_quit_panel(self) -> None:
        self.app.exit()

    def _run_interactive(self, cmd: list[str]) -> str:
        """Runs an interactive helper (the naming migration scripts) with the
        real terminal handed back to it via suspend(), capturing only its
        stdout (the scripts write prompts to stderr specifically so this
        works). Must be called from the main thread -- suspend() manipulates
        the app's terminal driver directly."""
        with self.app.suspend():
            proc = subprocess.run(cmd, cwd=ROOT, stdout=subprocess.PIPE, text=True)
        return proc.stdout or ""

    def _resolve_naming(self) -> tuple[str, str]:
        """Resolves Docker/DB naming, preferring the cached decision so
        repeated Start/Restart never touches app.suspend() once a machine
        has answered the prompt once. suspend()/resume is only exercised the
        very first time, when there's an actual decision to make."""
        cached = MANAGER.cached_docker_naming()
        if cached is not None:
            project, prefix = cached, cached
        else:
            self.notify("Resolving Docker naming...")
            project, prefix = MANAGER.resolve_docker_naming(self._run_interactive)
        if prefix == "owpengram" and MANAGER.cached_db_naming() is None:
            MANAGER.resolve_db_naming(self._run_interactive, f"{prefix}-postgres")
        return project, prefix

    def action_start(self) -> None:
        status = MANAGER.status()
        if status.running:
            self.notify("Already running", severity="warning")
            return
        if not ENV_FILE.exists():
            self.notify(".env not found — copy .env.example to .env and configure it first", severity="error")
            return
        self.query_one(OptionList).disabled = True
        progress = StartupProgressScreen("Starting server", START_STEPS)
        self.app.push_screen(progress)
        self._begin_start(progress)

    def _begin_start(self, progress: StartupProgressScreen) -> None:
        """Runs on the main thread: naming resolution may need suspend()."""
        try:
            project, prefix = self._resolve_naming()
        except Exception as exc:  # noqa: BLE001 - never leave the menu stuck
            progress.finish(False, f"Naming resolution failed: {exc}")
            self._unlock_menu()
            return
        self._start_rest(project, prefix, progress)

    @work(thread=True, exclusive=True)
    def _start_rest(self, project: str, prefix: str, progress: StartupProgressScreen) -> None:
        try:
            self.app.call_from_thread(progress.set_step, "docker", "active")
            ok, out = MANAGER.docker_compose_up(project, prefix)
            if not ok:
                self.app.call_from_thread(progress.set_step, "docker", "failed")
                self.app.call_from_thread(progress.finish, False, f"docker compose up failed:\n{out[-300:]}")
                return
            self.app.call_from_thread(progress.set_step, "docker", "done")

            self.app.call_from_thread(progress.set_step, "postgres", "active")
            if not MANAGER.wait_postgres(prefix):
                self.app.call_from_thread(progress.set_step, "postgres", "failed")
                self.app.call_from_thread(progress.finish, False, "PostgreSQL not ready after 60s")
                return
            self.app.call_from_thread(progress.set_step, "postgres", "done")

            self.app.call_from_thread(progress.set_step, "build", "active")
            ok, out = MANAGER.build()
            if not ok:
                self.app.call_from_thread(progress.set_step, "build", "failed")
                self.app.call_from_thread(progress.finish, False, f"Build failed:\n{out[-300:]}")
                return
            self.app.call_from_thread(progress.set_step, "build", "done")

            self.app.call_from_thread(progress.set_step, "launch", "active")
            server_pid = MANAGER.launch(SERVER_EXE, SERVER_LOG)
            admin_pid = MANAGER.launch(ADMIN_EXE, ADMIN_LOG)
            save_state({
                "server_pid": server_pid,
                "admin_pid": admin_pid,
                "docker_project": project,
                "docker_prefix": prefix,
            })
            self.app.call_from_thread(progress.set_step, "launch", "done")
            self.app.call_from_thread(progress.finish, True, "Server started")
        except Exception as exc:  # noqa: BLE001 - never leave the menu stuck
            self.app.call_from_thread(progress.finish, False, f"Start failed: {exc}")
        finally:
            self.app.call_from_thread(self._unlock_menu)

    def _unlock_menu(self) -> None:
        option_list = self.query_one(OptionList)
        option_list.disabled = False
        # Disabling a widget blurs it, and Textual doesn't automatically
        # restore focus when it's re-enabled: the number-key/q bindings
        # (screen-level) kept working regardless, but arrow-key navigation,
        # Enter-to-select and mouse clicks on the list all silently stopped
        # doing anything until something explicitly refocused it.
        option_list.focus()
        self.refresh_status()

    def action_stop(self) -> None:
        status = MANAGER.status()
        if not status.running:
            self.notify("Not running", severity="warning")
            return
        self.query_one(OptionList).disabled = True
        self.notify("Stopping...")
        self._stop_worker()

    @work(thread=True, exclusive=True)
    def _stop_worker(self) -> None:
        try:
            MANAGER.stop()
            self.app.call_from_thread(self.notify, "Stopped")
        except Exception as exc:  # noqa: BLE001 - never leave the menu stuck
            self.app.call_from_thread(self.notify, f"Stop failed: {exc}", severity="error", timeout=10)
        finally:
            self.app.call_from_thread(self._unlock_menu)

    def action_restart(self) -> None:
        self.query_one(OptionList).disabled = True
        progress = StartupProgressScreen("Restarting server", RESTART_STEPS)
        self.app.push_screen(progress)
        self._restart_worker(progress)

    @work(thread=True, exclusive=True)
    def _restart_worker(self, progress: StartupProgressScreen) -> None:
        try:
            self.app.call_from_thread(progress.set_step, "stop", "active")
            status = MANAGER.status()
            if status.running:
                MANAGER.stop()
            self.app.call_from_thread(progress.set_step, "stop", "done")
        except Exception as exc:  # noqa: BLE001 - never leave the menu stuck
            self.app.call_from_thread(progress.set_step, "stop", "failed")
            self.app.call_from_thread(progress.finish, False, f"Restart (stop phase) failed: {exc}")
            self.app.call_from_thread(self._unlock_menu)
            return
        # Hand off to the main thread: starting back up needs to run there
        # (naming resolution may call suspend()), and _start_rest takes over
        # unlocking the menu once it's done.
        self.app.call_from_thread(self._begin_start, progress)

    def action_update(self) -> None:
        self.query_one(OptionList).disabled = True
        progress = StartupProgressScreen("Updating server", UPDATE_STEPS, on_close=self._after_update)
        self.app.push_screen(progress)
        self._update_worker(progress)

    @work(thread=True, exclusive=True)
    def _update_worker(self, progress: StartupProgressScreen) -> None:
        try:
            self.app.call_from_thread(progress.set_step, "pull", "active")
            ok, out = MANAGER.git_pull()
            if not ok:
                self.app.call_from_thread(progress.set_step, "pull", "failed")
                self.app.call_from_thread(progress.finish, False, f"git pull failed:\n{out[-500:]}")
                self.app.call_from_thread(self._unlock_menu)
                return
            self.app.call_from_thread(progress.set_step, "pull", "done")

            self.app.call_from_thread(progress.set_step, "stop", "active")
            status = MANAGER.status()
            if status.running:
                MANAGER.stop()
            self.app.call_from_thread(progress.set_step, "stop", "done")
        except Exception as exc:  # noqa: BLE001 - never leave the menu stuck
            self.app.call_from_thread(progress.finish, False, f"Update failed: {exc}")
            self.app.call_from_thread(self._unlock_menu)
            return
        # Same hand-off as restart -- _start_rest takes over unlocking the
        # menu. Once the operator closes the progress screen, _after_update
        # decides whether to re-exec the panel process itself (see its
        # docstring for why that's needed on top of the Go binaries restart).
        self.app.call_from_thread(self._begin_start, progress)

    def _after_update(self, success: bool) -> None:
        """git pull can change server-panel.py itself (or any module it
        imports) -- restarting the Go binaries alone leaves this already-
        running Python process on the old code. Re-exec'ing replaces the
        process image with a fresh interpreter run of the (now updated)
        script, so the panel picks up its own changes too. Only fires once
        the operator has actually seen and dismissed the "Server started"
        confirmation, and only on success -- a failed pull/build leaves the
        panel exactly as it was, still showing the failure, nothing to
        re-exec into."""
        if not success:
            return
        self.app.request_restart = True
        self.app.exit()


class ServerPanelApp(App):
    TITLE = "OwpenGram Server Panel"
    # Set by MainScreen._after_update once a git pull + rebuild succeeds;
    # __main__ checks this after run() returns to decide whether to re-exec
    # the whole process (picking up changes to this script itself) instead
    # of just exiting.
    request_restart = False
    CSS = """
    #banner {
        width: 100%;
        /* content-align centers the whole multi-line block as one unit;
        text-align would instead justify each line independently by its own
        width, which staggers hand-aligned ASCII art like this banner. */
        content-align: center middle;
        color: cyan;
        text-style: bold;
        margin: 1 1 0 1;
    }
    #main-body {
        height: 1fr;
        margin: 1 1 0 1;
    }
    .panel-title {
        text-style: bold;
        color: $accent;
        margin-bottom: 1;
    }
    #services-panel {
        width: 2fr;
        height: 100%;
        border: round $accent;
        padding: 1 2;
        margin-right: 1;
    }
    #services-table {
        height: 1fr;
    }
    #sidebar {
        width: 1fr;
        height: 100%;
        border: round $accent;
        padding: 1 2;
        overflow-y: auto;
    }
    .stat-label {
        margin-top: 1;
    }
    #sidebar ProgressBar {
        width: 100%;
    }
    #admin-link {
        margin-top: 1;
    }
    #admin-password, #server-address, #server-public-key {
        margin-top: 1;
    }
    #env-title {
        text-style: bold;
        color: $accent;
        padding: 1 2 0 2;
    }
    #env-missing {
        padding: 1 2;
    }
    #env-scroll {
        height: 1fr;
        padding: 1 2;
    }
    .env-group-desc {
        color: $text-muted;
        text-style: italic;
        margin: 1 0 0 2;
    }
    .env-field {
        height: auto;
        margin: 1 0 0 2;
    }
    .env-field-key {
        color: $accent;
    }
    .env-field-desc {
        color: $text-muted;
        margin-bottom: 1;
    }
    #env-actions {
        height: auto;
        padding: 1 2;
        align: right middle;
    }
    #env-actions Button {
        margin-left: 1;
    }
    #setup-intro {
        color: $text-muted;
        padding: 0 2 1 2;
    }
    .env-field-group {
        height: auto;
        margin-bottom: 1;
    }
    .setup-section-title {
        text-style: bold;
        color: $accent;
        margin: 1 0 0 2;
    }
    #startup-title {
        text-style: bold;
        color: $accent;
        padding: 1 2 0 2;
    }
    #startup-steps {
        height: auto;
        padding: 1 2;
    }
    .startup-step {
        height: auto;
        margin-bottom: 1;
    }
    #startup-result {
        height: auto;
        padding: 0 2 1 2;
    }
    #startup-actions {
        height: auto;
        padding: 1 2;
        align: right middle;
    }
    LogTailScreen Horizontal {
        height: 1fr;
    }
    LogTailScreen Vertical {
        width: 1fr;
        height: 1fr;
    }
    RichLog {
        border: round $accent;
        height: 1fr;
    }
    """

    def on_mount(self) -> None:
        self.push_screen(MainScreen())


if __name__ == "__main__":
    # No argument (the normal `owpengram-server.bat` / .sh invocation):
    # quickstart -- bootstrap, start, print the admin panel URL, exit.
    # `panel`: the interactive TUI this file used to always open, still
    # available for stop/restart/logs/.env editing from the terminal.
    if len(sys.argv) > 1 and sys.argv[1] == "panel":
        app = ServerPanelApp()
        app.run()
        if app.request_restart:
            os.execv(sys.executable, [sys.executable] + sys.argv)
    else:
        sys.exit(quickstart())
