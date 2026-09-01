[CmdletBinding(SupportsShouldProcess)]
param(
    [Parameter(Mandatory = $true)]
    [ValidateNotNullOrEmpty()]
    [string]$AdvertiseIP,

    [Parameter()]
    [string]$PublicBaseURL = "",

    [Parameter()]
    [string]$PublicWebBaseURL = "",

    [Parameter()]
    [string]$AdminBindIP = "127.0.0.1",

    [Parameter()]
    [switch]$HostNetwork,

    [Parameter()]
    [switch]$BridgeNetwork,

    [Parameter()]
    [switch]$AllowInsecureDevelopmentAuth
)

$ErrorActionPreference = "Stop"

if ($HostNetwork -and $BridgeNetwork) {
    throw "HostNetwork and BridgeNetwork are mutually exclusive."
}

$parsedIP = $null
if (-not [System.Net.IPAddress]::TryParse($AdvertiseIP, [ref]$parsedIP)) {
    throw "AdvertiseIP must be an IPv4 or IPv6 address, not a DNS name."
}
$isLoopback = [System.Net.IPAddress]::IsLoopback($parsedIP)
$isIPv6 = $parsedIP.AddressFamily -eq [System.Net.Sockets.AddressFamily]::InterNetworkV6

$parsedAdminBindIP = $null
if (-not [System.Net.IPAddress]::TryParse($AdminBindIP, [ref]$parsedAdminBindIP)) {
    throw "AdminBindIP must be an IPv4 or IPv6 address."
}

if ([string]::IsNullOrWhiteSpace($PublicBaseURL)) {
    if (-not $isLoopback) {
        throw "PublicBaseURL is required when AdvertiseIP is not loopback."
    }
    $loopbackHost = if ($isIPv6) { "[::1]" } else { "127.0.0.1" }
    $PublicBaseURL = "http://${loopbackHost}:2401"
}
if ([string]::IsNullOrWhiteSpace($PublicWebBaseURL)) {
    $PublicWebBaseURL = $PublicBaseURL
}

function Assert-HTTPURL {
    param([string]$Name, [string]$Value)
    $uri = $null
    if (-not [Uri]::TryCreate($Value, [UriKind]::Absolute, [ref]$uri) -or
        ($uri.Scheme -ne "http" -and $uri.Scheme -ne "https") -or
        -not [string]::IsNullOrEmpty($uri.UserInfo)) {
        throw "$Name must be an absolute HTTP(S) URL without embedded credentials."
    }
}

Assert-HTTPURL -Name "PublicBaseURL" -Value $PublicBaseURL
Assert-HTTPURL -Name "PublicWebBaseURL" -Value $PublicWebBaseURL
if (-not $isLoopback -and -not $AllowInsecureDevelopmentAuth) {
    throw "Internet/LAN development-code auth requires AllowInsecureDevelopmentAuth; for production generate on loopback, then configure the webhook provider before startup."
}

$repoRoot = Split-Path -Parent $PSScriptRoot
$dockerDir = Join-Path $repoRoot "deploy\docker"
$templatePath = Join-Path $dockerDir ".env.example"
$outputPath = Join-Path $dockerDir ".env"
if (-not (Test-Path -LiteralPath $templatePath -PathType Leaf)) {
    throw "Docker environment template not found: $templatePath"
}
if (Test-Path -LiteralPath $outputPath) {
    throw "$outputPath already exists. Initialization never overwrites live credentials."
}

function New-HexSecret {
    param([int]$Bytes = 32)
    $buffer = New-Object byte[] $Bytes
    $generator = [System.Security.Cryptography.RandomNumberGenerator]::Create()
    try {
        $generator.GetBytes($buffer)
    }
    finally {
        $generator.Dispose()
    }
    return [BitConverter]::ToString($buffer).Replace("-", "").ToLowerInvariant()
}

function Get-GitValue {
    param([string[]]$GitArguments)
    try {
        $value = & git -C $repoRoot @GitArguments 2>$null
        if ($LASTEXITCODE -eq 0) { return ($value | Select-Object -First 1).Trim() }
    }
    catch {}
    return "unknown"
}

$publicBindIP = if ($isIPv6) { "::" } else { "0.0.0.0" }
$localBindIP = if ($isIPv6) { "::1" } else { "127.0.0.1" }
if ($isLoopback) {
    $publicBindIP = $parsedIP.ToString()
    $localBindIP = $parsedIP.ToString()
}
elseif (([Uri]$PublicBaseURL).Scheme -eq "http") {
    $publicLinkIP = $null
    $publicLinkHost = ([Uri]$PublicBaseURL).Host.Trim([char[]]"[]")
    if ([System.Net.IPAddress]::TryParse($publicLinkHost, [ref]$publicLinkIP) -and $publicLinkIP.Equals($parsedIP)) {
        $localBindIP = $parsedIP.ToString()
    }
}

$adminHealthIP = $parsedAdminBindIP.ToString()
if ($adminHealthIP -eq "0.0.0.0") { $adminHealthIP = "127.0.0.1" }
if ($adminHealthIP -eq "::") { $adminHealthIP = "::1" }
$publicListenHost = if ($publicBindIP.Contains(":")) { "[${publicBindIP}]" } else { $publicBindIP }
$localListenHost = if ($localBindIP.Contains(":")) { "[${localBindIP}]" } else { $localBindIP }
$serverHealthURLHost = $localListenHost
$adminListenIP = $parsedAdminBindIP.ToString()
$adminListenHost = if ($adminListenIP.Contains(":")) { "[${adminListenIP}]" } else { $adminListenIP }
$turnEnabled = (-not $isIPv6).ToString().ToLowerInvariant()
$turnAdvertiseIP = if ($isIPv6) { "127.0.0.1" } else { $parsedIP.ToString() }
$rtmpHost = if ($isIPv6) { "[$($parsedIP.ToString())]" } else { $parsedIP.ToString() }
$postgresPassword = New-HexSecret 24

$treeState = "unknown"
try {
    $treeOutput = & git -C $repoRoot status --porcelain 2>$null
    if ($LASTEXITCODE -eq 0) { $treeState = if (@($treeOutput).Count -gt 0) { "dirty" } else { "clean" } }
}
catch {}

$values = [ordered]@{
    TELESRV_BUILD_COMMIT                    = Get-GitValue @("rev-parse", "HEAD")
    TELESRV_BUILD_BRANCH                    = Get-GitValue @("rev-parse", "--abbrev-ref", "HEAD")
    TELESRV_BUILD_TREE_STATE                = $treeState
    TELESRV_BUILD_DATE                      = [DateTime]::UtcNow.ToString("o")
    POSTGRES_PASSWORD                       = $postgresPassword
    TELESRV_POSTGRES_DSN                    = "postgres://telesrv:${postgresPassword}@127.0.0.1:15432/telesrv_main?sslmode=disable"
    TELESRV_REDIS_PASSWORD                  = New-HexSecret 32
    TELESRV_ADMIN_API_TOKEN                 = New-HexSecret 32
    TELESRV_ADMIN_UI_PASSWORD               = New-HexSecret 24
    TELESRV_ADMIN_SESSION_KEY               = New-HexSecret 32
    TELESRV_TURN_SECRET                      = New-HexSecret 32
    TELESRV_OTP_WEBHOOK_SECRET              = New-HexSecret 32
    TELESRV_ALLOW_INSECURE_DEVELOPMENT_AUTH = ($isLoopback -or $AllowInsecureDevelopmentAuth).ToString().ToLowerInvariant()
    TELESRV_ADVERTISE_IP                    = $parsedIP.ToString()
    TELESRV_PUBLIC_BASE_URL                 = $PublicBaseURL
    TELESRV_PUBLIC_WEB_BASE_URL             = $PublicWebBaseURL
    TELESRV_SERVER_HOST_NETWORK             = (-not $BridgeNetwork).ToString().ToLowerInvariant()
    TELESRV_SFU_ADVERTISE_IP                = $parsedIP.ToString()
    TELESRV_TURN_ENABLE                      = $turnEnabled
    TELESRV_TURN_ADVERTISE_IP                = $turnAdvertiseIP
    TELESRV_LIVESTREAM_RTMP_URL              = "rtmp://${rtmpHost}:2400/live"
    TELESRV_PUBLIC_BIND_IP                   = $publicBindIP
    TELESRV_PUBLIC_LISTEN_HOST               = $publicListenHost
    TELESRV_LOCAL_BIND_IP                    = $localBindIP
    TELESRV_LOCAL_LISTEN_HOST                = $localListenHost
    TELESRV_SERVER_HEALTH_IP                 = $localBindIP
    TELESRV_SERVER_HEALTH_URL_HOST           = $serverHealthURLHost
    TELESRV_ADMIN_BIND_IP                    = $adminListenIP
    TELESRV_ADMIN_LISTEN_HOST                = $adminListenHost
    TELESRV_ADMIN_HEALTH_IP                  = $adminHealthIP
}

$content = [IO.File]::ReadAllText($templatePath)
foreach ($entry in $values.GetEnumerator()) {
    $pattern = "(?m)^$([Regex]::Escape($entry.Key))=.*$"
    if (-not [Regex]::IsMatch($content, $pattern)) { throw "Template is missing $($entry.Key)." }
    $replacement = ("{0}={1}" -f $entry.Key, $entry.Value).Replace('$', '$$')
    $content = [Regex]::Replace($content, $pattern, $replacement)
}

function Protect-SecretFile {
    param([string]$Path)

    if ($env:OS -eq "Windows_NT") {
        $owner = [System.Security.Principal.WindowsIdentity]::GetCurrent().User
        $acl = New-Object System.Security.AccessControl.FileSecurity
        $acl.SetAccessRuleProtection($true, $false)
        $acl.SetOwner($owner)
        $identities = @(
            $owner,
            (New-Object System.Security.Principal.SecurityIdentifier("S-1-5-18")),
            (New-Object System.Security.Principal.SecurityIdentifier("S-1-5-32-544"))
        )
        foreach ($identity in $identities) {
            $rule = New-Object System.Security.AccessControl.FileSystemAccessRule(
                $identity,
                [System.Security.AccessControl.FileSystemRights]::FullControl,
                [System.Security.AccessControl.AccessControlType]::Allow
            )
            [void]$acl.AddAccessRule($rule)
        }
        Set-Acl -LiteralPath $Path -AclObject $acl
        return
    }

    & chmod 600 -- $Path
    if ($LASTEXITCODE -ne 0) { throw "chmod 600 failed for $Path" }
}

if ($PSCmdlet.ShouldProcess($outputPath, "write owner-only Docker deployment environment")) {
    $temporaryPath = "$outputPath.tmp.$PID"
    try {
        [IO.File]::WriteAllText($temporaryPath, $content, (New-Object Text.UTF8Encoding($false)))
        Protect-SecretFile -Path $temporaryPath
        Move-Item -LiteralPath $temporaryPath -Destination $outputPath
    }
    finally {
        if (Test-Path -LiteralPath $temporaryPath) { Remove-Item -LiteralPath $temporaryPath -Force }
    }
    Write-Host "Created $outputPath with generated deployment credentials."
}
