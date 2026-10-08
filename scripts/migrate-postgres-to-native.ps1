<#
.SYNOPSIS
  Moves owpengram's PostgreSQL data from the external (Docker) instance to the
  embedded (native) PostgreSQL that owpengram-ctl manages -- automatically,
  end to end, with no manual steps, on Windows.

.DESCRIPTION
  The embedded PostgreSQL is bootstrapped from scratch when it isn't there yet:
  the binaries are taken from data/pgcache/ (or downloaded from Maven Central,
  the same source the embedded-postgres library uses), initdb'd with the exact
  flags the library uses (see internal/embeddedpg.go and the
  fergusstrange/embedded-postgres prepare_database.go), and the "owpengram"
  database is created. Then the Docker database is dumped and restored into
  it, and the embedded PostgreSQL is left stopped for owpengram-ctl to take
  over.

  Mirrors scripts/migrate-postgres-to-native.sh for Linux.

  Usage:
    .\scripts\migrate-postgres-to-native.ps1                    # or: ... migrate
        Run the whole migration automatically.

    .\scripts\migrate-postgres-to-native.ps1 dump
        Just pg_dump the Docker instance into .\pgmigrate\ (read-only).

    .\scripts\migrate-postgres-to-native.ps1 restore <dump-file> [<dest-dsn>]
        Just restore a dump into <dest-dsn> (defaults to the embedded DSN).

    .\scripts\migrate-postgres-to-native.ps1 bootstrap
        Just obtain the embedded binaries + initdb the data dir (idempotent),
        without touching the Docker instance or restoring anything.

    .\scripts\migrate-postgres-to-native.ps1 start-pg | stop-pg
        Start / stop the embedded PostgreSQL by hand (via its own pg_ctl).

  Flags:
    -Force, --force             stop owpengram-ctl automatically if it is
                                running (otherwise the script refuses and asks
                                you to stop it first)

  Environment variables (all optional, defaults match this deployment):
    DOCKER_CONTAINER              Postgres container name (default: owpengram-postgres)
    DOCKER_DB                     database name inside it   (default: owpengram)
    DOCKER_USER                   role name inside it       (default: owpengram)
    PG_RESTORE                    pg_restore binary to use  (default: the embedded
                                  PostgreSQL's own pg_restore.exe)
    TELESRV_EMBEDDED_POSTGRES_DIR data dir                    (default: data/postgres)
    TELESRV_EMBEDDED_POSTGRES_PORT port                       (default: 15433)
    FORCE=1                       same as -Force

.NOTES
  dump uses `docker cp` (writes the dump to a temp file inside the container,
  then copies it out) rather than a `>` redirect, because Windows PowerShell
  re-encodes a native command's stdout as text and would corrupt the binary
  custom-format dump. `docker cp` copies bytes unchanged.

  PostgreSQL on Windows refuses to run with admin privileges, so the script
  aborts early if it is running elevated -- re-run from a normal (non-admin)
  shell.
#>

$ErrorActionPreference = 'Stop'

# ---------------------------------------------------------------------------
# Repo root, defaults, environment overrides (parity with the bash script and
# internal/config's defaults).
# ---------------------------------------------------------------------------
$repoRoot = Split-Path -Parent $PSScriptRoot
$pgmigrateDir = Join-Path $repoRoot 'pgmigrate'

$DockerContainer = $env:DOCKER_CONTAINER
if (-not $DockerContainer) { $DockerContainer = 'owpengram-postgres' }
$DockerDb = $env:DOCKER_DB
if (-not $DockerDb) { $DockerDb = 'owpengram' }
$DockerUser = $env:DOCKER_USER
if (-not $DockerUser) { $DockerUser = 'owpengram' }

$pgDir = $env:TELESRV_EMBEDDED_POSTGRES_DIR
if (-not $pgDir) { $pgDir = 'data/postgres' }
$pgPort = $env:TELESRV_EMBEDDED_POSTGRES_PORT
if (-not $pgPort) { $pgPort = '15433' }

# Fixed by internal/embeddedpg.go (V17) -- must match scripts/build-release.ps1.
$PgVersion = '17.5.0'
$PgUser = 'owpengram'
$PgPass = 'owpengram'
$PgDb = 'owpengram'

# Embedded PostgreSQL layout (internal/embeddedpg.go): PGDATA lives in
# <dir>/data, binaries in <dir>/runtime/bin, cache in <parent>/pgcache.
if ([IO.Path]::IsPathRooted($pgDir)) {
    $pgDirAbs = $pgDir
} else {
    $pgDirAbs = Join-Path $repoRoot $pgDir
}
$pgDataPath = Join-Path $pgDirAbs 'data'
$pgRuntime = Join-Path $pgDirAbs 'runtime'
$pgCacheDir = Join-Path (Split-Path -Parent $pgDirAbs) 'pgcache'
$pgBin = Join-Path $pgRuntime 'bin'
$pgCtl = Join-Path $pgBin 'pg_ctl.exe'
$pgRestoreBin = Join-Path $pgBin 'pg_restore.exe'
$psql = Join-Path $pgBin 'psql.exe'
$initdb = Join-Path $pgBin 'initdb.exe'
$pgLog = Join-Path $pgDirAbs 'pg_ctl.log'
$embeddedDsn = "postgres://${PgUser}:${PgPass}@127.0.0.1:${pgPort}/${PgDb}?sslmode=disable"

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------
function Write-Info($msg) { Write-Host "[migrate-postgres] $msg" -ForegroundColor Cyan }
function Write-Warn($msg) { Write-Host "[migrate-postgres] WARNING: $msg" -ForegroundColor Yellow }
function Write-Die($msg) { Write-Host "[migrate-postgres] ERROR: $msg" -ForegroundColor Red; exit 1 }

# Runs a native command without letting stderr abort the script under
# PowerShell 5.1's 'Stop' preference (same lesson as scripts/build-release.ps1),
# returning its exit code.
function Invoke-Native($scriptBlock) {
    $prev = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        & $scriptBlock
        return $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $prev
    }
}

# PostgreSQL on Windows will not run with admin privileges -- refuse early.
function Assert-NotElevated {
    if ($env:OS -ne 'Windows_NT') { return }
    $id = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = New-Object Security.Principal.WindowsPrincipal($id)
    if ($principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        Write-Die "this shell is elevated -- PostgreSQL on Windows refuses to run with admin privileges; re-run from a normal (non-admin) shell"
    }
}

function Test-CtlRunning {
    $proc = Get-Process -Name 'owpengram-ctl' -ErrorAction SilentlyContinue
    return ($null -ne $proc)
}

function Get-PgArtifactKey {
    if ($env:PROCESSOR_ARCHITECTURE -match 'ARM') {
        Write-Die "no published embedded-PostgreSQL binaries for Windows ARM64 -- use an amd64 build"
    }
    return 'windows-amd64'
}

# Obtains the embedded PostgreSQL binaries into $pgBin if they are missing.
function Ensure-Binaries {
    if (Test-Path $pgCtl) { return }

    Write-Info "embedded PostgreSQL binaries not found at $pgBin -- obtaining them"
    New-Item -ItemType Directory -Force -Path $pgCacheDir | Out-Null
    New-Item -ItemType Directory -Force -Path $pgDirAbs | Out-Null

    $key = Get-PgArtifactKey
    $cache = Join-Path $pgCacheDir "embedded-postgres-binaries-${key}-${PgVersion}.txz"

    if (-not (Test-Path $cache)) {
        $jarUrl = "https://repo1.maven.org/maven2/io/zonky/test/postgres/embedded-postgres-binaries-${key}/${PgVersion}/embedded-postgres-binaries-${key}-${PgVersion}.jar"
        Write-Info "downloading $jarUrl"
        $tmpJar = Join-Path $env:TEMP ("owpg-jar-" + [guid]::NewGuid().ToString('N') + ".zip")
        $tmpDir = Join-Path $env:TEMP ("owpg-extract-" + [guid]::NewGuid().ToString('N'))
        try {
            Invoke-WebRequest -Uri $jarUrl -OutFile $tmpJar -UseBasicParsing
            Add-Type -AssemblyName System.IO.Compression.FileSystem
            [System.IO.Compression.ZipFile]::ExtractToDirectory($tmpJar, $tmpDir)
            $txz = Get-ChildItem -Path $tmpDir -Filter '*.txz' -Recurse | Select-Object -First 1
            if (-not $txz) { throw "no .txz found inside $jarUrl" }
            Copy-Item $txz.FullName $cache
        } catch {
            throw "failed to download/extract embedded PostgreSQL binaries: $($_.Exception.Message)"
        } finally {
            Remove-Item -Force -ErrorAction SilentlyContinue $tmpJar
            Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $tmpDir
        }
    }

    Write-Info "extracting $cache -> $pgRuntime"
    New-Item -ItemType Directory -Force -Path $pgRuntime | Out-Null
    $code = Invoke-Native { tar -xJf $cache -C $pgRuntime }
    if ($code -ne 0) { Write-Die "extraction failed (tar exit $code)" }
    if (-not (Test-Path $pgCtl)) { Write-Die "extraction failed: $pgCtl still missing" }
}

# initdb's the data dir with the exact flags the embedded-postgres library
# uses (prepare_database.go), unless it is already a 17.x data dir.
function Ensure-InitDb {
    $pgVersionFile = Join-Path $pgDataPath 'PG_VERSION'
    if (Test-Path $pgVersionFile) {
        $v = (Get-Content $pgVersionFile -Raw).Trim()
        if (-not $v.StartsWith('17')) {
            Write-Die "existing data dir is PostgreSQL $v, expected 17.x -- refusing to overwrite it; move $pgDataPath aside first"
        }
        return
    }

    Write-Info "initializing embedded PostgreSQL (initdb)..."
    New-Item -ItemType Directory -Force -Path $pgRuntime | Out-Null
    $pwfile = Join-Path $pgRuntime 'pwfile'
    [IO.File]::WriteAllText($pwfile, $PgPass)
    try {
        $code = Invoke-Native { & $initdb -A password -U $PgUser -D $pgDataPath "--pwfile=$pwfile" --locale=C --encoding=UTF8 }
        if ($code -ne 0) { Write-Die "initdb failed (exit $code)" }
    } finally {
        Remove-Item -Force -ErrorAction SilentlyContinue $pwfile
    }
}

function Stop-If-Running {
    if (Test-Path (Join-Path $pgDataPath 'postmaster.pid')) {
        Write-Info "embedded PostgreSQL appears to be running -- stopping it first"
        $code = Invoke-Native { & $pgCtl stop -D $pgDataPath -m fast }
        if ($code -ne 0) { Write-Warn "could not stop a running embedded PostgreSQL (it may already be stopped)" }
    }
}

function Start-Pg {
    Assert-NotElevated
    if (-not (Test-Path $pgCtl)) { Write-Die "pg_ctl not found at $pgCtl -- run bootstrap first" }
    if (-not (Test-Path (Join-Path $pgDataPath 'PG_VERSION'))) { Write-Die "data dir not initialized -- run bootstrap first" }
    Write-Info "starting embedded PostgreSQL on port $pgPort (data in $pgDataPath)..."
    $code = Invoke-Native { & $pgCtl start -w -D $pgDataPath -o "-p $pgPort" -l $pgLog }
    if ($code -ne 0) { Write-Die "pg_ctl start failed (exit $code)" }
}

function Stop-Pg {
    if (-not (Test-Path $pgCtl)) { Write-Die "pg_ctl not found at $pgCtl -- run bootstrap first" }
    Write-Info "stopping embedded PostgreSQL..."
    $code = Invoke-Native { & $pgCtl stop -D $pgDataPath -m fast }
    if ($code -ne 0) { Write-Warn "pg_ctl stop reported an error (exit $code) -- it may already be stopped" }
}

# Creates the "owpengram" database if it doesn't exist (fresh initdb only has
# postgres/template0/template1).
function Ensure-Database {
    $env:PGPASSWORD = $PgPass
    try {
        $exists = (& $psql -h 127.0.0.1 -p $pgPort -U $PgUser -d postgres -tAc "SELECT 1 FROM pg_database WHERE datname='$PgDb'") | Out-String
        if ($exists.Trim() -eq '1') { return }
        Write-Info "creating database '$PgDb'"
        $code = Invoke-Native { & $psql -h 127.0.0.1 -p $pgPort -U $PgUser -d postgres -c "CREATE DATABASE `"$PgDb`"" }
        if ($code -ne 0) { Write-Die "CREATE DATABASE failed (exit $code)" }
    } finally {
        Remove-Item Env:\PGPASSWORD -ErrorAction SilentlyContinue
    }
}

# ---------------------------------------------------------------------------
# Subcommands
# ---------------------------------------------------------------------------
function Invoke-Dump {
    if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
        Write-Die "docker not found on PATH -- this reads from the Docker Postgres container"
    }
    $names = & docker ps -a --format '{{.Names}}'
    if (-not ($names -contains $DockerContainer)) {
        Write-Die "no container named '$DockerContainer' (set DOCKER_CONTAINER= if yours is named differently)"
    }

    if (-not (Test-Path $pgmigrateDir)) { New-Item -ItemType Directory -Force -Path $pgmigrateDir | Out-Null }
    $stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
    $dumpName = "owpengram_$stamp.dump"
    $out = Join-Path $pgmigrateDir $dumpName

    # Write inside the container, then docker cp out (see .NOTES above).
    $containerTmp = '/tmp/owpengram_migrate.dump'

    Write-Info "dumping '$DockerDb' from container '$DockerContainer' (custom format, compressed)..."
    $code = Invoke-Native { docker exec $DockerContainer pg_dump -U $DockerUser -d $DockerDb -Fc -f $containerTmp }
    if ($code -ne 0) { Write-Die "pg_dump inside the container failed (exit $code)" }

    $code = Invoke-Native { docker cp "${DockerContainer}:$containerTmp" $out }
    if ($code -ne 0) { Write-Die "docker cp failed (exit $code)" }

    $code = Invoke-Native { docker exec $DockerContainer rm -f $containerTmp }
    if ($code -ne 0) { Write-Warn "could not remove $containerTmp inside the container (harmless)" }

    $size = (Get-Item $out).Length
    Write-Info "done: $out ($([math]::Round($size/1MB,1)) MB)"
    return $out
}

function Invoke-Restore($dump, $dsn) {
    if (-not $dump) { Write-Die "usage: $PSCommandPath restore <dump-file> [<dest-dsn>]" }
    if (-not $dsn) { $dsn = $embeddedDsn }
    if (-not (Test-Path $dump)) { Write-Die "no such dump file: $dump" }
    $dump = (Resolve-Path $dump).Path

    # Prefer the embedded build's own pg_restore.exe (no client install needed
    # on Windows); PG_RESTORE= overrides, then a bare pg_restore on PATH.
    $pgRestore = $env:PG_RESTORE
    if (-not $pgRestore) {
        if (Test-Path $pgRestoreBin) { $pgRestore = $pgRestoreBin }
        else { $pgRestore = 'pg_restore' }
    }
    if (-not (Test-Path $pgRestore) -and -not (Get-Command $pgRestore -ErrorAction SilentlyContinue)) {
        Write-Die "$pgRestore not found -- run bootstrap first, or set PG_RESTORE= to a pg_restore binary"
    }

    Write-Info "restoring $dump into the destination..."
    Write-Info "this DROPS and recreates every object the dump contains in that database first (--clean --if-exists) -- nothing outside what the dump itself describes is touched."

    $code = Invoke-Native { & $pgRestore --clean --if-exists --no-owner --no-privileges "--dbname=$dsn" $dump }
    if ($code -ne 0) { Write-Die "pg_restore failed (exit $code)" }

    Write-Info "restore finished."
}

function Invoke-Bootstrap {
    Assert-NotElevated
    Ensure-Binaries
    Stop-If-Running
    Ensure-InitDb
    Write-Info "embedded PostgreSQL is bootstrapped (binaries + initialized data dir); it is not running."
}

function Invoke-Migrate {
    Assert-NotElevated

    if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
        Write-Die "docker not found on PATH"
    }
    $names = & docker ps -a --format '{{.Names}}'
    if (-not ($names -contains $DockerContainer)) {
        Write-Die "no container named '$DockerContainer' (set DOCKER_CONTAINER= if yours is named differently)"
    }

    if (Test-CtlRunning) {
        if ($script:Force) {
            Write-Warn "owpengram-ctl is running -- stopping it (-Force)"
            Get-Process -Name 'owpengram-ctl' -ErrorAction SilentlyContinue | Stop-Process -Force
            Start-Sleep -Seconds 2
        } else {
            Write-Die "owpengram-ctl is running -- stop it first, or pass -Force to stop it automatically"
        }
    }

    Ensure-Binaries
    Stop-If-Running
    Ensure-InitDb

    $dumpfile = Invoke-Dump

    Start-Pg
    Ensure-Database
    Invoke-Restore $dumpfile $null
    Stop-Pg

    Write-Info "migration complete: Docker '$DockerDb' -> embedded PostgreSQL (port $pgPort, data in $pgDataPath)."
    Write-Info "next: run owpengram-ctl (it starts the embedded PostgreSQL and applies any"
    Write-Info "migrations newer than the dump's schema_migrations table), then migrate"
    Write-Info "blobs from MinIO to localfs with cmd/blob-migrate."
}

function Show-Usage {
    Write-Host @'
Usage: migrate-postgres-to-native.ps1 [command] [-Force]

Commands:
  migrate   (default) run the whole migration automatically
  dump      just pg_dump the Docker container into .\pgmigrate\
  restore <dump-file> [<dest-dsn>]
            just restore a dump into <dest-dsn> (defaults to the embedded DSN)
  bootstrap obtain binaries + initdb the data dir (idempotent, no dump/restore)
  start-pg  start the embedded PostgreSQL by hand (via its own pg_ctl)
  stop-pg   stop the embedded PostgreSQL started by start-pg

Flags:
  -Force, --force   stop owpengram-ctl automatically if it is running

Environment (all optional, defaults match this deployment):
  DOCKER_CONTAINER              Postgres container name (default: owpengram-postgres)
  DOCKER_DB                     database name inside it   (default: owpengram)
  DOCKER_USER                   role name inside it       (default: owpengram)
  PG_RESTORE                    pg_restore binary to use  (default: the embedded
                                PostgreSQL's own pg_restore.exe)
  TELESRV_EMBEDDED_POSTGRES_DIR data dir                    (default: data/postgres)
  TELESRV_EMBEDDED_POSTGRES_PORT port                       (default: 15433)
'@
}

# ---------------------------------------------------------------------------
# Dispatch
# ---------------------------------------------------------------------------
$script:Force = ($env:FORCE -eq '1')
$cmd = $null
$positional = @()
foreach ($a in $args) {
    if ($a -in '-Force', '--force') { $script:Force = $true; continue }
    if ($null -eq $cmd) { $cmd = $a } else { $positional += $a }
}
if ($null -eq $cmd) { $cmd = 'migrate' }

switch ($cmd) {
    'migrate'   { Invoke-Migrate }
    'dump'      { Invoke-Dump | Out-Null }
    'restore'   { Invoke-Restore $positional[0] $positional[1] }
    'bootstrap' { Invoke-Bootstrap }
    'start-pg'  { Start-Pg }
    'stop-pg'   { Stop-Pg }
    'help'      { Show-Usage }
    default {
        Write-Host "[migrate-postgres] unknown command: $cmd`n" -ForegroundColor Red
        Show-Usage
        exit 2
    }
}
