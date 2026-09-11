#Requires -Version 5.1
<#
.SYNOPSIS
Installs everything owpengram-server.bat checks for, via winget: Go, Python 3
(+ the panel's packages in a venv) and OpenSSL.

.DESCRIPTION
Docker is the deliberate exception. It is only reported, with a link: on Windows
the containers this server needs (PostgreSQL, Redis, MinIO) are Linux images, so
the daemon has to sit on a Linux kernel -- which means Docker Desktop with WSL2,
an install that wants a reboot and carries its own licence terms. That is not a
thing to start behind someone's back.

.PARAMETER DryRun
Only report what would be installed.

.PARAMETER Yes
Skip the confirmation prompt.
#>
param(
    [switch]$DryRun,
    [switch]$Yes
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$DockerDocs = 'https://docs.docker.com/desktop/setup/install/windows-install/'
$GoMinMinor = 25

function Write-Ok   { param([string]$Text) Write-Host "[ok] $Text" -ForegroundColor Green }
function Write-Info { param([string]$Text) Write-Host "[..] $Text" -ForegroundColor Cyan }
function Write-Warn { param([string]$Text) Write-Host "[WARN] $Text" -ForegroundColor Yellow }
function Write-Err  { param([string]$Text) Write-Host "[ERROR] $Text" -ForegroundColor Red }

function Test-Command { param([string]$Name) [bool](Get-Command $Name -ErrorAction SilentlyContinue) }

# winget puts new tools on the machine/user PATH, but this process was started
# with the old one -- without this the venv step cannot find the Python it just
# installed, and the closing "go version" would report the absence of Go.
function Update-PathFromRegistry {
    $machine = [Environment]::GetEnvironmentVariable('Path', 'Machine')
    $user    = [Environment]::GetEnvironmentVariable('Path', 'User')
    $env:Path = (@($machine, $user) | Where-Object { $_ }) -join ';'
}

# python.exe on a stock Windows is often the Microsoft Store's app-execution
# alias: present on PATH, and it opens the Store instead of running anything.
# Only a real version string counts, which is the same trap owpengram-server.bat
# documents.
function Get-WorkingPython {
    foreach ($candidate in @('py -3', 'python3', 'python')) {
        $parts = $candidate.Split(' ')
        if (-not (Test-Command $parts[0])) { continue }
        try {
            $out = & $parts[0] @($parts[1..($parts.Length - 1)] + '--version') 2>&1 | Out-String
        } catch { continue }
        if ($out -match 'Python 3\.') { return $candidate }
    }
    return $null
}

function Test-GoRecentEnough {
    if (-not (Test-Command 'go')) { return $false }
    try { $version = (& go env GOVERSION 2>$null | Out-String).Trim() } catch { return $false }
    if ($version -match '^go1\.(\d+)') { return [int]$Matches[1] -ge $GoMinMinor }
    return $false
}

# Same precedence owpengram-server.bat uses when it picks an interpreter: the
# venv when one exists, otherwise whatever Python is on PATH. Checking only the
# venv would report the packages missing on a machine that already has them
# installed system-wide, and build a venv nobody asked for.
function Test-PanelDepsInstalled {
    $checker = Join-Path $RepoRoot 'tui-panel\check_deps.py'
    $venvPython = Join-Path $RepoRoot '.venv\Scripts\python.exe'
    if (Test-Path $venvPython) {
        $missing = & $venvPython $checker 2>$null | Out-String
        return [string]::IsNullOrWhiteSpace($missing)
    }
    $fallback = Get-WorkingPython
    if (-not $fallback) { return $false }
    $parts = $fallback.Split(' ')
    $missing = & $parts[0] @($parts[1..($parts.Length - 1)] + $checker) 2>$null | Out-String
    return [string]::IsNullOrWhiteSpace($missing)
}

function Install-WingetPackage {
    param([string]$Id, [string]$Label)
    Write-Info "Installing $Label ($Id)"
    & winget install --exact --id $Id --source winget `
        --accept-package-agreements --accept-source-agreements --disable-interactivity
    # 0 is installed; -1978335189 is "no applicable upgrade", i.e. already current.
    if ($LASTEXITCODE -ne 0 -and $LASTEXITCODE -ne -1978335189) {
        throw "winget failed to install $Id (exit $LASTEXITCODE)"
    }
    Update-PathFromRegistry
}

if (-not (Test-Command 'winget')) {
    Write-Err 'winget is not available. Install "App Installer" from the Microsoft Store, then run this again.'
    exit 1
}

# --- what is missing ---------------------------------------------------------
$python = Get-WorkingPython
$needed = [System.Collections.Generic.List[string]]::new()
if (-not (Test-GoRecentEnough))     { $needed.Add('go') }
if (-not $python)                   { $needed.Add('python') }
if (-not (Test-Command 'openssl'))  { $needed.Add('openssl') }
if (-not (Test-PanelDepsInstalled)) { $needed.Add('pydeps') }
$dockerMissing = -not (Test-Command 'docker')

if ($needed.Count -eq 0 -and -not $dockerMissing) {
    Write-Ok 'All prerequisites are already installed.'
    exit 0
}

Write-Host ''
Write-Host '== Missing prerequisites ==' -ForegroundColor Yellow
foreach ($item in $needed) {
    switch ($item) {
        'go'      { Write-Host "  - Go 1.$GoMinMinor+ (builds owpengram-server and the admin panel)" }
        'python'  { Write-Host '  - Python 3 (runs the server-panel TUI)' }
        'pydeps'  { Write-Host '  - Python packages: textual, psutil, cryptography (into .\.venv)' }
        'openssl' { Write-Host "  - OpenSSL (exports the server's RSA public key for clients)" }
    }
}
if ($dockerMissing) { Write-Host '  - Docker (runs PostgreSQL, Redis and MinIO) -- install this one yourself, see below' }
Write-Host ''

if ($DryRun) {
    Write-Ok 'Dry run -- nothing was installed.'
    exit 0
}

if ($needed.Count -gt 0 -and -not $Yes) {
    $answer = Read-Host 'Install these now? [Y/n]'
    if ($answer -and $answer -notmatch '^(y|yes)$') {
        Write-Err 'cancelled'
        exit 1
    }
}

if ($needed -contains 'go')      { Install-WingetPackage -Id 'GoLang.Go' -Label 'Go' }
if ($needed -contains 'python')  { Install-WingetPackage -Id 'Python.Python.3.13' -Label 'Python 3.13' }
if ($needed -contains 'openssl') { Install-WingetPackage -Id 'ShiningLight.OpenSSL.Light' -Label 'OpenSSL' }

# --- the panel's Python packages ---------------------------------------------
# A venv rather than the machine-wide interpreter, to match what the Linux side
# does and what owpengram-server.bat already prefers once one exists.
if ($needed -contains 'pydeps' -or $needed -contains 'python') {
    $python = Get-WorkingPython
    if (-not $python) {
        Write-Err 'Python still is not callable. Open a new terminal and run this script again.'
        exit 1
    }
    Write-Info 'Installing the panel''s Python packages into .\.venv'
    $venvPython = Join-Path $RepoRoot '.venv\Scripts\python.exe'
    if (-not (Test-Path $venvPython)) {
        $parts = $python.Split(' ')
        & $parts[0] @($parts[1..($parts.Length - 1)] + @('-m', 'venv', (Join-Path $RepoRoot '.venv')))
        if ($LASTEXITCODE -ne 0) { throw 'could not create .venv' }
    }
    & $venvPython -m pip install --quiet --upgrade pip
    & $venvPython -m pip install --quiet -r (Join-Path $RepoRoot 'tui-panel\requirements-panel.txt')
    if ($LASTEXITCODE -ne 0) { throw 'could not install the panel''s Python packages' }
    Write-Ok 'Python packages installed'
}

Write-Host ''
if ($dockerMissing) {
    Write-Warn 'Docker still has to be installed by hand.'
    Write-Host "     The server's PostgreSQL, Redis and MinIO are Linux containers, so Windows"
    Write-Host '     needs Docker Desktop (it brings the WSL2 backend that runs them).'
    Write-Host "     $DockerDocs"
    Write-Host '     Install it, reboot if it asks, then run this script again.'
    Write-Host ''
}
if ($needed.Count -gt 0) {
    Write-Ok 'Prerequisites are ready.'
    Write-Host '     Open a new terminal so the updated PATH is picked up, then start the server with:'
    Write-Host '       owpengram-server.bat'
}
