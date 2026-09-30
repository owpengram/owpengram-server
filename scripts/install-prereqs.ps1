#Requires -Version 5.1
<#
.SYNOPSIS
Installs everything owpengram-server.bat checks for, via winget: Go and OpenSSL.

.DESCRIPTION
Docker is the deliberate exception. It is only reported, with a link: on Windows
the containers this server needs (PostgreSQL, MinIO) are Linux images, so
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

$DockerDocs = 'https://docs.docker.com/desktop/setup/install/windows-install/'
$GoMinMinor = 25

function Write-Ok   { param([string]$Text) Write-Host "[ok] $Text" -ForegroundColor Green }
function Write-Info { param([string]$Text) Write-Host "[..] $Text" -ForegroundColor Cyan }
function Write-Warn { param([string]$Text) Write-Host "[WARN] $Text" -ForegroundColor Yellow }
function Write-Err  { param([string]$Text) Write-Host "[ERROR] $Text" -ForegroundColor Red }

function Test-Command { param([string]$Name) [bool](Get-Command $Name -ErrorAction SilentlyContinue) }

# winget puts new tools on the machine/user PATH, but this process was started
# with the old one -- without this the closing "go version" would report the
# absence of Go.
function Update-PathFromRegistry {
    $machine = [Environment]::GetEnvironmentVariable('Path', 'Machine')
    $user    = [Environment]::GetEnvironmentVariable('Path', 'User')
    $env:Path = (@($machine, $user) | Where-Object { $_ }) -join ';'
}

function Test-GoRecentEnough {
    if (-not (Test-Command 'go')) { return $false }
    try { $version = (& go env GOVERSION 2>$null | Out-String).Trim() } catch { return $false }
    if ($version -match '^go1\.(\d+)') { return [int]$Matches[1] -ge $GoMinMinor }
    return $false
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
$needed = [System.Collections.Generic.List[string]]::new()
if (-not (Test-GoRecentEnough))    { $needed.Add('go') }
if (-not (Test-Command 'openssl')) { $needed.Add('openssl') }
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
        'openssl' { Write-Host "  - OpenSSL (exports the server's RSA public key for clients)" }
    }
}
if ($dockerMissing) { Write-Host '  - Docker (runs PostgreSQL and MinIO) -- install this one yourself, see below' }
Write-Host ''

if ($DryRun) {
    Write-Ok 'Dry run -- nothing was installed.'
    exit 0
}

if ($needed.Count -gt 0 -and -not $Yes) {
    # Without a console there is nobody to answer, and Read-Host would block for
    # as long as the caller is willing to wait -- forever, for a CI job or a
    # wrapping script. Say so instead of hanging.
    if ([Console]::IsInputRedirected) {
        Write-Err 'no console to confirm on -- re-run with -Yes to install without asking, or -DryRun to only look'
        exit 1
    }
    $answer = Read-Host 'Install these now? [Y/n]'
    if ($answer -and $answer -notmatch '^(y|yes)$') {
        Write-Err 'cancelled'
        exit 1
    }
}

if ($needed -contains 'go')      { Install-WingetPackage -Id 'GoLang.Go' -Label 'Go' }
if ($needed -contains 'openssl') { Install-WingetPackage -Id 'ShiningLight.OpenSSL.Light' -Label 'OpenSSL' }

Write-Host ''
if ($dockerMissing) {
    Write-Warn 'Docker still has to be installed by hand.'
    Write-Host "     The server's PostgreSQL and MinIO are Linux containers, so Windows"
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
