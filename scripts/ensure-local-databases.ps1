<#
.SYNOPSIS
Ensures the branch-isolated local PostgreSQL databases exist.

.DESCRIPTION
The main and v2 branches have independent migration histories. They must never
share one schema_migrations row. This helper creates telesrv_main and
telesrv_v2 in the local Compose PostgreSQL container without modifying or
deleting the legacy telesrv database.

Optional template parameters are intended for a one-time local split when an
existing database snapshot should be preserved. They are only used when the
target database does not already exist.
#>
[CmdletBinding()]
param(
    [string]$PostgresContainer = "telesrv-postgres",
    [string]$DbUser = "telesrv",
    [string]$MainTemplate,
    [string]$V2Template
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Assert-SafeIdentifier {
    param([string]$Name, [string]$Value)
    if ([string]::IsNullOrWhiteSpace($Value) -or $Value -notmatch '^[A-Za-z_][A-Za-z0-9_]*$') {
        throw "$Name must be a PostgreSQL identifier containing only letters, digits, and underscores: '$Value'"
    }
}

function Invoke-Docker {
    param([string[]]$Arguments)
    $oldErrorActionPreference = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    try {
        $output = & docker @Arguments 2>&1
        $exitCode = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $oldErrorActionPreference
    }
    $text = ($output | ForEach-Object { $_.ToString() }) -join "`n"
    if ($exitCode -ne 0) {
        throw "docker $($Arguments -join ' ') failed with exit code ${exitCode}:`n$text"
    }
    return $text.Trim()
}

function Test-DatabaseExists {
    param([string]$Database)
    Assert-SafeIdentifier "database" $Database
    $result = Invoke-Docker @(
        "exec", $PostgresContainer,
        "psql", "-U", $DbUser, "-d", "postgres",
        "-v", "ON_ERROR_STOP=1", "-At", "-c",
        "SELECT 1 FROM pg_database WHERE datname = '$Database';"
    )
    return $result -eq "1"
}

function Ensure-Database {
    param([string]$Database, [string]$Template)
    if (Test-DatabaseExists $Database) {
        Write-Host "[ok] PostgreSQL database already exists: $Database"
        return
    }

    $args = @("exec", $PostgresContainer, "createdb", "-U", $DbUser, "-O", $DbUser)
    if (-not [string]::IsNullOrWhiteSpace($Template)) {
        Assert-SafeIdentifier "template database" $Template
        if (-not (Test-DatabaseExists $Template)) {
            throw "template database does not exist: $Template"
        }
        $args += @("-T", $Template)
    }
    $args += $Database
    Invoke-Docker $args | Out-Null
    $templateSuffix = ""
    if (-not [string]::IsNullOrWhiteSpace($Template)) {
        $templateSuffix = " (template: $Template)"
    }
    Write-Host "[ok] created PostgreSQL database: $Database$templateSuffix"
}

Assert-SafeIdentifier "database user" $DbUser
if ($PostgresContainer -notmatch '^[A-Za-z0-9_.-]+$') {
    throw "invalid Docker container name: '$PostgresContainer'"
}

$running = Invoke-Docker @("inspect", "-f", "{{.State.Running}}", $PostgresContainer)
if ($running -ne "true") {
    throw "PostgreSQL container is not running: $PostgresContainer"
}

Ensure-Database "telesrv_main" $MainTemplate
Ensure-Database "telesrv_v2" $V2Template
