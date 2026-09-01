[CmdletBinding()]
param(
    [Parameter()]
    [string]$AdvertiseIP = "",

    [Parameter()]
    [string]$PublicBaseURL = "",

    [Parameter()]
    [string]$PublicWebBaseURL = "",

    [Parameter()]
    [string]$AdminBindIP = "",

    [Parameter()]
    [switch]$HostNetwork,

    [Parameter()]
    [switch]$BridgeNetwork,

    [Parameter()]
    [switch]$AllowInsecureDevelopmentAuth,

    [Parameter()]
    [switch]$Build
)

$ErrorActionPreference = "Stop"
if ($HostNetwork -and $BridgeNetwork) { throw "HostNetwork and BridgeNetwork are mutually exclusive." }

$repoRoot = Split-Path -Parent $PSScriptRoot
$dockerDir = Join-Path $repoRoot "deploy\docker"
$composePath = Join-Path $dockerDir "compose.yaml"
$envPath = Join-Path $dockerDir ".env"
$generatorPath = Join-Path $PSScriptRoot "new-docker-env.ps1"

if (-not (Test-Path -LiteralPath $envPath -PathType Leaf)) {
    if ([string]::IsNullOrWhiteSpace($AdvertiseIP)) { $AdvertiseIP = "127.0.0.1" }
    $generatorArguments = @{ AdvertiseIP = $AdvertiseIP }
    if (-not [string]::IsNullOrWhiteSpace($PublicBaseURL)) { $generatorArguments.PublicBaseURL = $PublicBaseURL }
    if (-not [string]::IsNullOrWhiteSpace($PublicWebBaseURL)) { $generatorArguments.PublicWebBaseURL = $PublicWebBaseURL }
    if (-not [string]::IsNullOrWhiteSpace($AdminBindIP)) { $generatorArguments.AdminBindIP = $AdminBindIP }
    if ($HostNetwork) { $generatorArguments.HostNetwork = $true }
    if ($BridgeNetwork) { $generatorArguments.BridgeNetwork = $true }
    if ($AllowInsecureDevelopmentAuth) { $generatorArguments.AllowInsecureDevelopmentAuth = $true }
    & $generatorPath @generatorArguments
}
elseif ($PSBoundParameters.Keys | Where-Object { $_ -notin @("Build") }) {
    Write-Warning "deploy/docker/.env already exists; initialization options were ignored to preserve credentials and deployment identity."
}

$deployment = @{}
foreach ($line in Get-Content -LiteralPath $envPath) {
    if ($line -match '^([A-Z0-9_]+)=(.*)$') { $deployment[$Matches[1]] = $Matches[2] }
}
if ($deployment['TELESRV_DEPLOYMENT_PROFILE'] -ne 'main-monolith-v1') {
    throw "$envPath belongs to an older or different topology; move it aside and rerun so credentials are regenerated safely."
}

$composeBase = @("compose", "--project-directory", $dockerDir, "--env-file", $envPath, "--file", $composePath)
$hostNetworkValue = $deployment['TELESRV_SERVER_HOST_NETWORK']
if ([string]::IsNullOrWhiteSpace($hostNetworkValue)) { $hostNetworkValue = "true" }
if ($hostNetworkValue -ne "true" -and $hostNetworkValue -ne "false") { throw "TELESRV_SERVER_HOST_NETWORK must be true or false." }
if ($hostNetworkValue -eq "false") { $composeBase += @("--file", (Join-Path $dockerDir "compose.bridge-network.yaml")) }

function Invoke-Compose {
    param([string[]]$Arguments)
    & docker @composeBase @Arguments
    if ($LASTEXITCODE -ne 0) { throw "docker compose $($Arguments -join ' ') failed with exit code $LASTEXITCODE" }
}

Invoke-Compose -Arguments @("config", "--quiet")
if ($Build) { Invoke-Compose -Arguments @("build", "--pull") } else { Invoke-Compose -Arguments @("pull") }
try { Invoke-Compose -Arguments @("up", "--detach", "--no-build", "--wait", "--wait-timeout", "600") }
catch { & docker @composeBase logs --no-color --tail 160; throw }

Invoke-Compose -Arguments @("ps", "--all")
Write-Host "gramsrv main Docker stack is ready. Configuration: $envPath"
if ($deployment['TELESRV_PHONE_CODE_DELIVERY_PROVIDER'] -eq 'development') { Write-Host "Development login code: $($deployment['TELESRV_DEV_AUTH_CODE'])" }
Write-Host "MTProto: $($deployment['TELESRV_ADVERTISE_IP']):$($deployment['TELESRV_SERVER_PORT'])"
if ($deployment['TELESRV_TURN_ENABLE'] -eq 'true') {
    Write-Host "TURN/STUN: udp://$($deployment['TELESRV_TURN_ADVERTISE_IP']):$($deployment['TELESRV_TURN_UDP_PORT'])"
    $turnRelayMaxPort = $deployment['TELESRV_TURN_RELAY_MAX_PORT']
    if ($hostNetworkValue -eq 'false') { $turnRelayMaxPort = $deployment['TELESRV_TURN_BRIDGE_RELAY_MAX_PORT'] }
    Write-Host "TURN relay UDP range: $($deployment['TELESRV_TURN_RELAY_MIN_PORT'])-$turnRelayMaxPort"
}
$adminHost = $deployment['TELESRV_ADMIN_BIND_IP']
if ($adminHost -eq "0.0.0.0" -or $adminHost -eq "::") { $adminHost = $deployment['TELESRV_ADVERTISE_IP'] }
if ($adminHost.Contains(":")) { $adminHost = "[${adminHost}]" }
Write-Host "Admin UI: http://${adminHost}:$($deployment['TELESRV_ADMIN_PORT']) (password is stored in $envPath)"
