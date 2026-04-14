param(
    [Parameter(Position = 0)]
    [string]$Command = "status",

    [Parameter(Position = 1)]
    [string]$Value,

    [string]$Model = "sentinel-router",

    [ValidateSet("high", "xhigh")]
    [string]$Effort = "high",

    [ValidateRange(16, 64)]
    [int]$Bytes = 32,

    [string]$Prompt = "Responda apenas: ok",

    [switch]$NoRestart,

    [switch]$Persist,

    [switch]$Watch
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$Root = Resolve-Path (Join-Path $PSScriptRoot "..")
Set-Location $Root

function Read-DotEnv {
    $envPath = Join-Path $Root ".env"
    $map = @{}
    if (-not (Test-Path $envPath)) {
        return $map
    }

    foreach ($line in Get-Content $envPath) {
        if ($line -match '^\s*#' -or $line -notmatch '^\s*([^=\s]+)\s*=(.*)$') {
            continue
        }
        $map[$matches[1]] = $matches[2].Trim()
    }
    return $map
}

function Set-DotEnvValue {
    param(
        [Parameter(Mandatory = $true)][string]$Key,
        [Parameter(Mandatory = $true)][string]$NewValue
    )

    $envPath = Join-Path $Root ".env"
    $lines = @()
    if (Test-Path $envPath) {
        $lines = @(Get-Content $envPath)
    }

    $updated = $false
    for ($i = 0; $i -lt $lines.Count; $i++) {
        if ($lines[$i] -match "^\s*$([regex]::Escape($Key))\s*=") {
            $lines[$i] = "$Key=$NewValue"
            $updated = $true
            break
        }
    }

    if (-not $updated) {
        $lines += "$Key=$NewValue"
    }

    Set-Content -Path $envPath -Value $lines
}

function Get-BaseURL {
    $envMap = Read-DotEnv
    $addr = ":8080"
    if ($envMap.ContainsKey("HTTP_ADDR") -and $envMap["HTTP_ADDR"]) {
        $addr = $envMap["HTTP_ADDR"]
    }

    if ($addr -match '^https?://') {
        return $addr.TrimEnd("/")
    }
    if ($addr.StartsWith(":")) {
        return "http://127.0.0.1$addr"
    }
    return "http://$addr"
}

function Get-OpenAICompatibleBaseURL {
    $base = (Get-BaseURL).TrimEnd("/")
    if ($base -match '/v1$') {
        return $base
    }
    return "$base/v1"
}

function Get-DefaultModel {
    $envMap = Read-DotEnv
    if ($envMap.ContainsKey("DEFAULT_MODEL") -and $envMap["DEFAULT_MODEL"]) {
        return $envMap["DEFAULT_MODEL"]
    }
    return "sentinel-router"
}

function Get-DefaultReasoningEffort {
    $envMap = Read-DotEnv
    if ($envMap.ContainsKey("DEFAULT_REASONING_EFFORT") -and $envMap["DEFAULT_REASONING_EFFORT"] -in @("high", "xhigh")) {
        return $envMap["DEFAULT_REASONING_EFFORT"]
    }
    return "high"
}

function Get-AuthHeaders {
    $envMap = Read-DotEnv
    if ($envMap.ContainsKey("SENTINEL_API_KEY") -and $envMap["SENTINEL_API_KEY"]) {
        return @{ "X-API-Key" = $envMap["SENTINEL_API_KEY"] }
    }
    return @{}
}

function Invoke-Sentinel {
    param(
        [Parameter(Mandatory = $true)][string]$Method,
        [Parameter(Mandatory = $true)][string]$Path,
        [object]$Body = $null,
        [int]$TimeoutSec = 30,
        [hashtable]$ExtraHeaders = @{}
    )

    $headers = Get-AuthHeaders
    foreach ($key in $ExtraHeaders.Keys) {
        $headers[$key] = $ExtraHeaders[$key]
    }

    $uri = "$(Get-BaseURL)$Path"
    if ($null -eq $Body) {
        return Invoke-RestMethod -Uri $uri -Method $Method -Headers $headers -TimeoutSec $TimeoutSec
    }

    $json = $Body | ConvertTo-Json -Depth 24
    return Invoke-RestMethod -Uri $uri -Method $Method -Headers $headers -Body $json -ContentType "application/json" -TimeoutSec $TimeoutSec
}

function Show-Help {
    @"
Sentinel control CLI

Usage:
  .\tools\sentinelctl.ps1 status
  .\tools\sentinelctl.ps1 accounts
  .\tools\sentinelctl.ps1 models
  .\tools\sentinelctl.ps1 chat -Model gpt-5.4 -Effort high -Prompt "Responda apenas: ok"
  .\tools\sentinelctl.ps1 test
  .\tools\sentinelctl.ps1 force acc_contato_deskimperial_online
  .\tools\sentinelctl.ps1 unforce
  .\tools\sentinelctl.ps1 disable acc_suporte_deskimperial_online
  .\tools\sentinelctl.ps1 enable acc_suporte_deskimperial_online
  .\tools\sentinelctl.ps1 use-model gpt-5.4 -Effort xhigh
  .\tools\sentinelctl.ps1 codex-install
  .\tools\sentinelctl.ps1 codex-install -Persist
  .\tools\sentinelctl.ps1 key-show
  .\tools\sentinelctl.ps1 key-new
  .\tools\sentinelctl.ps1 key-revoke
  .\tools\sentinelctl.ps1 restart
  .\tools\sentinelctl.ps1 logs -Watch

Notes:
  use-model changes sentinel-router and DEFAULT_REASONING_EFFORT, then restart is required.
  Effort is intentionally limited to high or xhigh.
  codex-install writes ~/.codex/config.toml with a managed Sentinel provider and exports CODEX_API_KEY for this shell.
  key-new and key-revoke rotate SENTINEL_API_KEY and restart by default.
"@
}

function New-SentinelAPIKey {
    param([int]$ByteCount = 32)

    $bytes = [byte[]]::new($ByteCount)
    [System.Security.Cryptography.RandomNumberGenerator]::Fill($bytes)
    $encoded = [Convert]::ToBase64String($bytes).TrimEnd("=")
    $encoded = $encoded.Replace("+", "-").Replace("/", "_")
    return "sk-sentinel-$encoded"
}

function Mask-Secret {
    param([string]$Secret)

    if ([string]::IsNullOrWhiteSpace($Secret)) {
        return "<empty>"
    }
    if ($Secret.Length -le 14) {
        return "********"
    }
    return "$($Secret.Substring(0, 11))...$($Secret.Substring($Secret.Length - 6))"
}

function Get-CurrentAPIKey {
    $envMap = Read-DotEnv
    if ($envMap.ContainsKey("SENTINEL_API_KEY")) {
        return $envMap["SENTINEL_API_KEY"]
    }
    return ""
}

function Rotate-APIKey {
    param(
        [int]$ByteCount = 32,
        [bool]$Restart = $true
    )

    $oldKey = Get-CurrentAPIKey
    $newKey = New-SentinelAPIKey -ByteCount $ByteCount
    Set-DotEnvValue -Key "SENTINEL_API_KEY" -NewValue $newKey

    [pscustomobject]@{
        old_key = Mask-Secret $oldKey
        new_key = $newKey
    } | Format-List

    if ($Restart) {
        Stop-Sentinel
        Start-Sleep -Milliseconds 500
        Start-Sentinel
        "Old API key revoked. New API key is active now."
    } else {
        "New API key was written to .env. Run '.\tools\sentinelctl.ps1 restart' to revoke the old key in memory."
    }
}

function Get-HealthOrNull {
    try {
        return Invoke-RestMethod -Uri "$(Get-BaseURL)/healthz" -Method Get -TimeoutSec 5
    } catch {
        return $null
    }
}

function Start-Sentinel {
    if (Get-HealthOrNull) {
        "Sentinel already responds at $(Get-BaseURL)"
        return
    }

    $exe = Join-Path $Root ".tools\sentinel.exe"
    if (-not (Test-Path $exe)) {
        $go = Join-Path $Root ".tools\go\bin\go.exe"
        if (-not (Test-Path $go)) {
            throw "Missing $exe and local Go toolchain $go"
        }
        $env:GOCACHE = Join-Path $Root ".tools\gocache"
        & $go build -o $exe .\cmd\sentinel
        if ($LASTEXITCODE -ne 0) {
            throw "go build failed"
        }
    }

    $out = Join-Path $Root "sentinel.out.log"
    $err = Join-Path $Root "sentinel.err.log"
    $p = Start-Process -FilePath $exe -WorkingDirectory $Root -RedirectStandardOutput $out -RedirectStandardError $err -PassThru
    Set-Content -Path (Join-Path $Root ".sentinel.pid") -Value $p.Id

    for ($i = 0; $i -lt 20; $i++) {
        Start-Sleep -Milliseconds 500
        $p.Refresh()
        if ($p.HasExited) {
            throw "Sentinel exited early with code $($p.ExitCode). See sentinel.err.log."
        }
        if (Get-HealthOrNull) {
            "Sentinel started: PID=$($p.Id) URL=$(Get-BaseURL)"
            return
        }
    }

    throw "Sentinel did not become healthy. See sentinel.err.log."
}

function Stop-Sentinel {
    $pidPath = Join-Path $Root ".sentinel.pid"
    if (-not (Test-Path $pidPath)) {
        "No .sentinel.pid found"
        return
    }

    $pidText = (Get-Content $pidPath | Select-Object -First 1).Trim()
    if ($pidText -notmatch '^\d+$') {
        throw "Invalid PID file: $pidText"
    }

    $proc = Get-Process -Id ([int]$pidText) -ErrorAction SilentlyContinue
    if ($null -eq $proc) {
        "Sentinel is not running"
        return
    }

    Stop-Process -Id ([int]$pidText) -Force
    "Sentinel stopped: PID=$pidText"
}

function Update-RouterModel {
    param(
        [Parameter(Mandatory = $true)][string]$TargetModel,
        [Parameter(Mandatory = $true)][string]$TargetEffort
    )

    $modelsPath = Join-Path $Root "configs\models.json"
    $config = Get-Content $modelsPath -Raw | ConvertFrom-Json
    $target = $config.models | Where-Object { $_.id -eq $TargetModel } | Select-Object -First 1
    if ($null -eq $target) {
        throw "Model '$TargetModel' is not in configs/models.json. Run: .\tools\sentinelctl.ps1 models"
    }

    $router = $config.models | Where-Object { $_.id -eq "sentinel-router" } | Select-Object -First 1
    if ($null -eq $router) {
        throw "sentinel-router is missing from configs/models.json"
    }

    $router.provider = $target.provider
    $router.upstream = $target.upstream
    $router.upstream_model = $target.upstream_model

    $config | ConvertTo-Json -Depth 24 | Set-Content -Path $modelsPath
    Set-DotEnvValue -Key "DEFAULT_REASONING_EFFORT" -NewValue $TargetEffort

    "sentinel-router -> $TargetModel ($($target.upstream_model)); DEFAULT_REASONING_EFFORT=$TargetEffort"
    "Run '.\tools\sentinelctl.ps1 restart' to reload config."
}

function Get-CodexConfigPath {
    if ($env:CODEX_HOME) {
        return Join-Path $env:CODEX_HOME "config.toml"
    }
    return Join-Path (Join-Path $HOME ".codex") "config.toml"
}

function Remove-TomlManagedBlock {
    param([string]$Content)

    if ([string]::IsNullOrEmpty($Content)) {
        return ""
    }

    return [regex]::Replace(
        $Content,
        "(?ms)^\s*# BEGIN PROJECT SENTINEL MANAGED\r?\n.*?^\s*# END PROJECT SENTINEL MANAGED\r?\n?",
        ""
    )
}

function Remove-TomlRootKeys {
    param(
        [string]$Content,
        [string[]]$Keys
    )

    if ([string]::IsNullOrEmpty($Content)) {
        return ""
    }

    $keyPattern = ($Keys | ForEach-Object { [regex]::Escape($_) }) -join "|"
    $lines = $Content -split "\r?\n"
    $inSection = $false
    $out = New-Object System.Collections.Generic.List[string]

    foreach ($line in $lines) {
        if ($line -match '^\s*\[[^\]]+\]\s*(#.*)?$') {
            $inSection = $true
        }
        if (-not $inSection -and $line -match "^\s*($keyPattern)\s*=") {
            continue
        }
        [void]$out.Add($line)
    }

    return (($out.ToArray()) -join "`r`n").TrimEnd()
}

function Remove-TomlSection {
    param(
        [string]$Content,
        [string]$Section
    )

    if ([string]::IsNullOrEmpty($Content)) {
        return ""
    }

    $lines = $Content -split "\r?\n"
    $dropping = $false
    $out = New-Object System.Collections.Generic.List[string]

    foreach ($line in $lines) {
        if ($line -match '^\s*\[([^\]]+)\]\s*(#.*)?$') {
            $name = $matches[1].Trim()
            $dropping = [string]::Equals($name, $Section, [System.StringComparison]::OrdinalIgnoreCase)
            if ($dropping) {
                continue
            }
        }
        if ($dropping) {
            continue
        }
        [void]$out.Add($line)
    }

    return (($out.ToArray()) -join "`r`n").TrimEnd()
}

function New-CodexManagedBlock {
    param(
        [Parameter(Mandatory = $true)][string]$TargetModel,
        [Parameter(Mandatory = $true)][string]$TargetEffort,
        [Parameter(Mandatory = $true)][string]$BaseURL
    )

    if ($TargetModel -notmatch '^[A-Za-z0-9._:-]+$') {
        throw "Unsafe Codex model id: $TargetModel"
    }
    if ($BaseURL -notmatch '^https?://') {
        throw "Codex base URL must start with http:// or https://"
    }

    @"
# BEGIN PROJECT SENTINEL MANAGED
model = "$TargetModel"
model_provider = "sentinel"
model_reasoning_effort = "$TargetEffort"

[model_providers.sentinel]
name = "Project Sentinel"
base_url = "$BaseURL"
wire_api = "responses"
env_key = "CODEX_API_KEY"
# END PROJECT SENTINEL MANAGED
"@.TrimEnd()
}

function Install-CodexProvider {
    param(
        [Parameter(Mandatory = $true)][string]$TargetModel,
        [Parameter(Mandatory = $true)][string]$TargetEffort,
        [bool]$PersistEnv = $false
    )

    $modelsPath = Join-Path $Root "configs\models.json"
    $config = Get-Content $modelsPath -Raw | ConvertFrom-Json
    $target = $config.models | Where-Object { $_.id -eq $TargetModel } | Select-Object -First 1
    if ($null -eq $target) {
        throw "Model '$TargetModel' is not in configs/models.json. Run: .\tools\sentinelctl.ps1 models"
    }

    $apiKey = Get-CurrentAPIKey
    if ([string]::IsNullOrWhiteSpace($apiKey)) {
        throw "SENTINEL_API_KEY is empty. Run '.\tools\sentinelctl.ps1 key-new' first."
    }

    $baseURL = Get-OpenAICompatibleBaseURL
    $configPath = Get-CodexConfigPath
    $configDir = Split-Path -Parent $configPath
    New-Item -ItemType Directory -Path $configDir -Force | Out-Null

    $existing = ""
    $backupPath = ""
    if (Test-Path $configPath) {
        $existing = Get-Content -LiteralPath $configPath -Raw
        if (-not [string]::IsNullOrWhiteSpace($existing)) {
            $backupPath = "$configPath.bak-$(Get-Date -Format yyyyMMddHHmmss)"
            Copy-Item -LiteralPath $configPath -Destination $backupPath
        }
    }

    $clean = Remove-TomlManagedBlock -Content $existing
    $clean = Remove-TomlRootKeys -Content $clean -Keys @("model", "model_provider", "model_reasoning_effort")
    $clean = Remove-TomlSection -Content $clean -Section "model_providers.sentinel"
    $block = New-CodexManagedBlock -TargetModel $TargetModel -TargetEffort $TargetEffort -BaseURL $baseURL

    if ([string]::IsNullOrWhiteSpace($clean)) {
        $nextContent = "$block`r`n"
    } else {
        $nextContent = "$($clean.TrimEnd())`r`n`r`n$block`r`n"
    }
    Set-Content -LiteralPath $configPath -Value $nextContent

    $env:CODEX_API_KEY = $apiKey
    $env:CODEX_BASE_URL = $baseURL
    $env:CODEX_MODEL = $TargetModel

    if ($PersistEnv) {
        [System.Environment]::SetEnvironmentVariable("CODEX_API_KEY", $apiKey, "User")
        [System.Environment]::SetEnvironmentVariable("CODEX_BASE_URL", $baseURL, "User")
        [System.Environment]::SetEnvironmentVariable("CODEX_MODEL", $TargetModel, "User")
    }

    [pscustomobject]@{
        codex_config = $configPath
        backup = if ($backupPath) { $backupPath } else { "<none>" }
        provider = "sentinel"
        base_url = $baseURL
        model = $TargetModel
        reasoning_effort = $TargetEffort
        env_key = "CODEX_API_KEY"
        codex_api_key = Mask-Secret $apiKey
        persisted_user_env = $PersistEnv
    } | Format-List

    if ($PersistEnv) {
        "Codex provider installed. Reopen PowerShell if another terminal needs the persisted CODEX_API_KEY."
    } else {
        "Codex provider installed for this PowerShell session. Use -Persist to save CODEX_API_KEY in the Windows user environment."
    }
}

switch ($Command.ToLowerInvariant()) {
    { $_ -in @("help", "-h", "--help") } {
        Show-Help
        break
    }

    "start" {
        Start-Sentinel
        break
    }

    "stop" {
        Stop-Sentinel
        break
    }

    "restart" {
        Stop-Sentinel
        Start-Sleep -Milliseconds 500
        Start-Sentinel
        break
    }

    "status" {
        $health = Get-HealthOrNull
        if ($null -eq $health) {
            "Sentinel is not responding at $(Get-BaseURL)"
            break
        }
        $state = Invoke-Sentinel -Method Get -Path "/admin/state"
        $accounts = Invoke-Sentinel -Method Get -Path "/admin/accounts"
        $forcedAccount = ""
        if ($state.PSObject.Properties["forced_account_id"]) {
            $forcedAccount = $state.forced_account_id
        }

        [pscustomobject]@{
            server = $health.status
            url = Get-BaseURL
            rotation = $state.rotation_strategy
            force_mode = $state.force_mode_active
            forced_account = $forcedAccount
            accounts = $state.account_count
            active_accounts = $state.active_accounts
            active_leases = $state.active_leases
        } | Format-List

        $accounts.accounts |
            Select-Object account_id, provider, status, daily_usage_count, daily_limit, error_count, latency_ewma_ms, active_leases |
            Format-Table -AutoSize
        break
    }

    "state" {
        Invoke-Sentinel -Method Get -Path "/admin/state" | ConvertTo-Json -Depth 8
        break
    }

    "accounts" {
        $accounts = Invoke-Sentinel -Method Get -Path "/admin/accounts"
        $accounts.accounts |
            Select-Object account_id, provider, status, daily_usage_count, daily_limit, error_count, latency_ewma_ms, active_leases, cooldown_until |
            Format-Table -AutoSize
        break
    }

    "models" {
        $models = Invoke-Sentinel -Method Get -Path "/v1/models"
        $models.data | Select-Object id, owned_by | Format-Table -AutoSize
        break
    }

    "chat" {
        $body = @{
            model = $Model
            stream = $false
            reasoning_effort = $Effort
            messages = @(@{ role = "user"; content = $Prompt })
        }
        $resp = Invoke-Sentinel -Method Post -Path "/v1/chat/completions" -Body $body -TimeoutSec 180
        $resp.choices[0].message.content
        break
    }

    "test" {
        $body = @{
            model = "gpt-5.4"
            stream = $false
            reasoning_effort = $Effort
            messages = @(@{ role = "user"; content = $Prompt })
        }
        $resp = Invoke-Sentinel -Method Post -Path "/v1/chat/completions" -Body $body -TimeoutSec 180
        [pscustomobject]@{
            http = 200
            model = $resp.model
            content = $resp.choices[0].message.content
        } | Format-List
        break
    }

    "force" {
        if (-not $Value) { throw "Usage: .\tools\sentinelctl.ps1 force <account_id>" }
        Invoke-Sentinel -Method Post -Path "/admin/force" -Body @{ account_id = $Value } | ConvertTo-Json -Depth 8
        break
    }

    "unforce" {
        Invoke-Sentinel -Method Post -Path "/admin/force" -Body @{ enabled = $false } | ConvertTo-Json -Depth 8
        break
    }

    "disable" {
        if (-not $Value) { throw "Usage: .\tools\sentinelctl.ps1 disable <account_id>" }
        Invoke-Sentinel -Method Post -Path "/admin/accounts/$Value/disable" | ConvertTo-Json -Depth 8
        break
    }

    "enable" {
        if (-not $Value) { throw "Usage: .\tools\sentinelctl.ps1 enable <account_id>" }
        Invoke-Sentinel -Method Post -Path "/admin/accounts/$Value/enable" | ConvertTo-Json -Depth 8
        break
    }

    "use-model" {
        $target = $Value
        if (-not $target) {
            $target = $Model
        }
        Update-RouterModel -TargetModel $target -TargetEffort $Effort
        break
    }

    "codex-install" {
        $target = $Value
        if (-not $target) {
            if ($PSBoundParameters.ContainsKey("Model")) {
                $target = $Model
            } else {
                $target = Get-DefaultModel
            }
        }
        $targetEffort = $Effort
        if (-not $PSBoundParameters.ContainsKey("Effort")) {
            $targetEffort = Get-DefaultReasoningEffort
        }
        Install-CodexProvider -TargetModel $target -TargetEffort $targetEffort -PersistEnv $Persist
        break
    }

    "set-effort" {
        Set-DotEnvValue -Key "DEFAULT_REASONING_EFFORT" -NewValue $Effort
        "DEFAULT_REASONING_EFFORT=$Effort"
        "Run '.\tools\sentinelctl.ps1 restart' to reload config."
        break
    }

    "key-show" {
        [pscustomobject]@{
            sentinel_api_key = Mask-Secret (Get-CurrentAPIKey)
        } | Format-List
        break
    }

    "key-new" {
        Rotate-APIKey -ByteCount $Bytes -Restart (-not $NoRestart)
        break
    }

    "key-rotate" {
        Rotate-APIKey -ByteCount $Bytes -Restart (-not $NoRestart)
        break
    }

    "key-revoke" {
        Rotate-APIKey -ByteCount $Bytes -Restart (-not $NoRestart)
        break
    }

    "logs" {
        $logPath = Join-Path $Root "sentinel.err.log"
        if ($Watch) {
            Get-Content -Path $logPath -Tail 80 -Wait
        } else {
            Get-Content -Path $logPath -Tail 80
        }
        break
    }

    default {
        Show-Help
        throw "Unknown command: $Command"
    }
}
