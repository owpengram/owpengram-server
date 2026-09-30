<#
.SYNOPSIS
  Builds the same self-contained release archives .github/workflows/build.yml
  produces in CI, locally.

.DESCRIPTION
  For each target platform: cross-compiles owpengram-server, owpengram-admin-panel
  and owpengram-ctl (CGO_ENABLED=0, so no C toolchain is needed even when
  cross-compiling), assembles a staging directory with .env.example,
  data/langpack, data/sticker-seed, deploy/docker-compose.yml and the right
  start.sh/start.bat launcher, pre-bundles the embedded-PostgreSQL binaries
  for that platform into data/pgcache/ (see internal/embeddedpg.go's
  CachePath doc comment -- this is what makes the portable edition's first
  start need zero network access), and packages the result into a .zip
  (Windows targets) or .tar.gz (everything else) under dist/.

Accepts both PowerShell-style (-All, -Platform, -Version, ...) and the
Unix-style double-dash spelling scripts/build-release.sh uses (--all,
--platform, --version, ...) -- so a command copied from one script's
example works unchanged in the other. Deliberately NOT a declarative
param() block: PowerShell's own parameter binder has no concept of "--"
flags at all (it would silently bind "--all" as a positional value
instead of recognizing it), so arguments are parsed by hand below instead.

.EXAMPLE
  ./scripts/build-release.ps1
  Builds just an archive for this machine's platform, into dist/.

.EXAMPLE
  ./scripts/build-release.ps1 -All -Version v1.4.0
  ./scripts/build-release.ps1 --all --version v1.4.0
  Both build all 4 platform archives, named owpengram-server-v1.4.0-<os>-<arch>.

.EXAMPLE
  ./scripts/build-release.ps1 -Platform linux/amd64 -Platform windows/amd64
  Builds just those two, repeating -Platform/--platform for each one.
#>

$ErrorActionPreference = 'Stop'

function Show-Usage {
    Write-Host @'
Usage: build-release.ps1 [options]

  -All, --all                  build linux/amd64, linux/arm64, windows/amd64
                                and windows/arm64 (same as CI)
  -Platform, --platform GOOS/GOARCH
                                build this one platform; repeat for more.
                                Defaults to just the host platform.
  -Version, --version V        embedded in the archive/file names (default: dev)
  -OutDir, --out-dir PATH      where archives go (default: dist)
  -SkipPgCache, --skip-pgcache skip pre-bundling embedded-PostgreSQL binaries
  -SkipWebBuild, --skip-web-build
                                skip npm ci && npm run build for the admin UI
  -Help, --help, -h            show this message
'@
}

$Platforms = @()
$All = $false
$Version = 'dev'
$OutDir = 'dist'
$SkipPgCache = $false
$SkipWebBuild = $false

$i = 0
while ($i -lt $args.Count) {
    $arg = $args[$i]
    switch ($arg) {
        { $_ -in '-All', '--all' } { $All = $true; $i++ }
        { $_ -in '-Platform', '--platform' } {
            if ($i + 1 -ge $args.Count) { throw "$arg requires a value" }
            $Platforms += $args[$i + 1]
            $i += 2
        }
        { $_ -in '-Version', '--version' } {
            if ($i + 1 -ge $args.Count) { throw "$arg requires a value" }
            $Version = $args[$i + 1]
            $i += 2
        }
        { $_ -in '-OutDir', '--out-dir' } {
            if ($i + 1 -ge $args.Count) { throw "$arg requires a value" }
            $OutDir = $args[$i + 1]
            $i += 2
        }
        { $_ -in '-SkipPgCache', '--skip-pgcache' } { $SkipPgCache = $true; $i++ }
        { $_ -in '-SkipWebBuild', '--skip-web-build' } { $SkipWebBuild = $true; $i++ }
        { $_ -in '-Help', '--help', '-h' } { Show-Usage; exit 0 }
        default { throw "unknown argument: $arg (see -Help)" }
    }
}

# Must match internal/embeddedpg.go's pgVersion constant -- there is no
# automated check tying these together, same caveat as the CI workflow.
$PgVersion = '17.5.0'

# goos/goarch -> zonkyio/embedded-postgres-binaries os/arch. Empty means "no
# published artifact" -- pre-bundling is skipped for that platform.
$PgMap = @{
    'linux/amd64'   = @{ os = 'linux';   arch = 'amd64' }
    'linux/arm64'   = @{ os = 'linux';   arch = 'arm64v8' }
    'windows/amd64' = @{ os = 'windows'; arch = 'amd64' }
    'windows/arm64' = @{ os = '';        arch = '' }
}

function Info($msg) { Write-Host "[build-release] $msg" -ForegroundColor Cyan }
function Warn($msg) { Write-Host "[build-release] $msg" -ForegroundColor Yellow }

$repoRoot = Split-Path -Parent $PSScriptRoot
Push-Location $repoRoot
try {
    if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
        throw "Go is not on PATH -- install it from https://go.dev/dl/"
    }

    if (-not $Platforms -or $Platforms.Count -eq 0) {
        if ($All) {
            $Platforms = @('linux/amd64', 'linux/arm64', 'windows/amd64', 'windows/arm64')
        } else {
            $hostGoos = (go env GOOS).Trim()
            $hostGoarch = (go env GOARCH).Trim()
            $Platforms = @("$hostGoos/$hostGoarch")
            Info "No -Platform given, building only for the host platform: $hostGoos/$hostGoarch (pass -All/--all for every CI platform)."
        }
    }

    if (-not $SkipWebBuild) {
        Info "Building admin panel web assets (npm ci && npm run build)..."
        Push-Location "cmd/telesrv-admin/web"
        try {
            npm ci
            if ($LASTEXITCODE -ne 0) { throw "npm ci failed" }
            npm run build
            if ($LASTEXITCODE -ne 0) { throw "npm run build failed" }
        } finally {
            Pop-Location
        }
    } else {
        Warn "Skipping web asset build (-SkipWebBuild) -- the admin binary will embed whatever is already in cmd/telesrv-admin/web/dist."
    }

    if (Test-Path $OutDir) {
        New-Item -ItemType Directory -Force -Path $OutDir | Out-Null
    } else {
        New-Item -ItemType Directory -Force -Path $OutDir | Out-Null
    }

    foreach ($platform in $Platforms) {
        $parts = $platform -split '/'
        if ($parts.Count -ne 2) { throw "Bad platform '$platform', expected 'goos/goarch'" }
        $goos = $parts[0]
        $goarch = $parts[1]
        $suffix = ''
        if ($goos -eq 'windows') { $suffix = '.exe' }

        Info "=== $goos/$goarch ==="
        $stageName = "owpengram-server-$Version-$goos-$goarch"
        $stageDir = Join-Path $OutDir "staging/$stageName"
        if (Test-Path $stageDir) { Remove-Item -Recurse -Force $stageDir }
        New-Item -ItemType Directory -Force -Path (Join-Path $stageDir 'bin') | Out-Null

        $env:CGO_ENABLED = '0'
        $env:GOOS = $goos
        $env:GOARCH = $goarch
        try {
            Info "Building binaries..."
            & go build -trimpath -ldflags="-s -w" -o (Join-Path $stageDir "bin/owpengram-server$suffix") ./cmd/telesrv
            if ($LASTEXITCODE -ne 0) { throw "go build owpengram-server failed" }
            & go build -trimpath -ldflags="-s -w" -o (Join-Path $stageDir "bin/owpengram-admin-panel$suffix") ./cmd/telesrv-admin
            if ($LASTEXITCODE -ne 0) { throw "go build owpengram-admin-panel failed" }
            & go build -trimpath -ldflags="-s -w" -o (Join-Path $stageDir "owpengram-ctl$suffix") ./cmd/telesrv-ctl
            if ($LASTEXITCODE -ne 0) { throw "go build owpengram-ctl failed" }
        } finally {
            Remove-Item Env:\CGO_ENABLED, Env:\GOOS, Env:\GOARCH -ErrorAction SilentlyContinue
        }

        Info "Assembling archive contents..."
        Copy-Item '.env.example' (Join-Path $stageDir '.env.example')
        Copy-Item 'release/README.md' (Join-Path $stageDir 'README.md')
        New-Item -ItemType Directory -Force -Path (Join-Path $stageDir 'deploy') | Out-Null
        Copy-Item 'deploy/docker-compose.yml' (Join-Path $stageDir 'deploy/docker-compose.yml')
        Copy-Item -Recurse 'data/langpack' (Join-Path $stageDir 'data/langpack')
        Copy-Item -Recurse 'data/sticker-seed' (Join-Path $stageDir 'data/sticker-seed')
        if ($goos -eq 'windows') {
            Copy-Item 'release/start.bat' (Join-Path $stageDir 'start.bat')
        } else {
            Copy-Item 'release/start.sh' (Join-Path $stageDir 'start.sh')
        }

        if (-not $SkipPgCache -and $PgMap.ContainsKey($platform) -and $PgMap[$platform].os -ne '') {
            $pgOs = $PgMap[$platform].os
            $pgArch = $PgMap[$platform].arch
            $jarUrl = "https://repo1.maven.org/maven2/io/zonky/test/postgres/embedded-postgres-binaries-$pgOs-$pgArch/$PgVersion/embedded-postgres-binaries-$pgOs-$pgArch-$PgVersion.jar"
            Info "Pre-bundling embedded PostgreSQL ($pgOs/$pgArch)..."
            $tmpJar = Join-Path $env:TEMP "owpg-$([guid]::NewGuid().ToString('N')).zip"
            $tmpExtract = Join-Path $env:TEMP "owpg-extract-$([guid]::NewGuid().ToString('N'))"
            try {
                Invoke-WebRequest -Uri $jarUrl -OutFile $tmpJar -UseBasicParsing
                Expand-Archive -Path $tmpJar -DestinationPath $tmpExtract -Force
                $txz = Get-ChildItem -Path $tmpExtract -Filter '*.txz' -Recurse | Select-Object -First 1
                if (-not $txz) { throw "no .txz found inside $jarUrl" }
                $pgCacheDir = Join-Path $stageDir 'data/pgcache'
                New-Item -ItemType Directory -Force -Path $pgCacheDir | Out-Null
                Copy-Item $txz.FullName (Join-Path $pgCacheDir "embedded-postgres-binaries-$pgOs-$pgArch-$PgVersion.txz")
            } catch {
                Warn "Could not pre-bundle embedded PostgreSQL for $pgOs/$pgArch ($($_.Exception.Message)) -- portable edition will download it on first start instead."
            } finally {
                Remove-Item -Force -ErrorAction SilentlyContinue $tmpJar
                Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $tmpExtract
            }
        } elseif (-not $SkipPgCache) {
            Warn "No published embedded-PostgreSQL binaries for $platform -- portable edition will download them on first start instead."
        }

        Info "Packaging..."
        Push-Location (Join-Path $OutDir 'staging')
        try {
            if ($goos -eq 'windows') {
                $archivePath = Join-Path '..' "$stageName.zip"
                if (Test-Path $archivePath) { Remove-Item -Force $archivePath }
                Compress-Archive -Path $stageName -DestinationPath $archivePath
            } else {
                $archivePath = Join-Path '..' "$stageName.tar.gz"
                if (Test-Path $archivePath) { Remove-Item -Force $archivePath }
                & tar -czf $archivePath $stageName
                if ($LASTEXITCODE -ne 0) { throw "tar failed" }
            }
        } finally {
            Pop-Location
        }
        Info "-> $OutDir/$stageName$(if ($goos -eq 'windows') { '.zip' } else { '.tar.gz' })"

        # The archive above already has everything this held -- no reason
        # to leave a second, uncompressed copy (bin/ alone can be 100MB+)
        # lying around after every run.
        Remove-Item -Recurse -Force $stageDir
    }

    $stagingRoot = Join-Path $OutDir 'staging'
    if ((Test-Path $stagingRoot) -and -not (Get-ChildItem $stagingRoot -Force)) {
        Remove-Item -Force $stagingRoot
    }
    Info "Done. Archives are in $OutDir/."
} finally {
    Pop-Location
}
