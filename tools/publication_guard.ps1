param(
    [switch]$Strict
)

$ErrorActionPreference = "Stop"

function Write-Section {
    param([string]$Message)
    Write-Host "\n=== $Message ===" -ForegroundColor Cyan
}

function Is-ForbiddenTrackedPath {
    param([string]$Path)

    $p = $Path.Replace("\\", "/")

    if ($p -eq ".env") { return $true }
    if ($p -like ".env.*" -and $p -notlike "*.example") { return $true }

    if ($p -like "sessions/*" -and $p -ne "sessions/.gitkeep") { return $true }
    if ($p -like "*.json.enc") { return $true }

    if ($p -like "*.db" -or $p -like "*.db-shm" -or $p -like "*.db-wal") { return $true }

    if ($p -in @(
        "tools/auto-login/credentials.json",
        "accounts.json",
        "accounts.local.json",
        "accounts.csv",
        "accounts.local.csv",
        "deploy_quota_patch.tar"
    )) { return $true }

    return $false
}

function Is-BinaryContent {
    param([string]$Text)
    return $Text.IndexOf([char]0) -ge 0
}

$root = Split-Path -Parent $PSScriptRoot
Push-Location $root

try {
    Write-Section "Project Sentinel - Publication Guard"

    git rev-parse --is-inside-work-tree *> $null

    $tracked = @(git ls-files)
    if ($tracked.Count -eq 0) {
        Write-Host "Nenhum arquivo versionado encontrado. Abortando checagem." -ForegroundColor Yellow
        exit 2
    }

    $forbiddenTracked = @()
    foreach ($path in $tracked) {
        if (Is-ForbiddenTrackedPath -Path $path) {
            $forbiddenTracked += $path
        }
    }

    Write-Section "1) Arquivos proibidos versionados"
    if ($forbiddenTracked.Count -gt 0) {
        $forbiddenTracked | Sort-Object -Unique | ForEach-Object { Write-Host "[BLOCK] $_" -ForegroundColor Red }
    } else {
        Write-Host "OK: nenhum arquivo proibido versionado." -ForegroundColor Green
    }

    Write-Section "2) Padrões de segredo em arquivos versionados"

    $findings = New-Object System.Collections.Generic.List[object]

    $secretPatterns = @(
        @{ Name = "Private key PEM"; Regex = "-----BEGIN (RSA|OPENSSH|EC|DSA) PRIVATE KEY-----" },
        @{ Name = "Sentinel key real"; Regex = "sk-sentinel-[A-Za-z0-9_-]{20,}" },
        @{ Name = "Bearer token suspeito"; Regex = "Bearer\s+[A-Za-z0-9._-]{20,}" },
        @{ Name = "Access token suspeito"; Regex = "access_token\s*[:=]\s*[\x22\x27][A-Za-z0-9._-]{20,}[\x22\x27]" },
        @{ Name = "Session key suspeita"; Regex = "SESSION_ENCRYPTION_KEY\s*=\s*[^\r\n#]+" },
        @{ Name = "OpenAI resource key suspeita"; Regex = "OPENAI_RESOURCE_\d+_API_KEY\s*=\s*[^\r\n#]+" }
    )

    $safeLineRegex = @(
        "sk-sentinel-replace-with-a-random-local-api-key",
        "replace-with-a-random-local-api-key",
        "SESSION_ENCRYPTION_KEY=change-this-key-32-bytes-long!!!",
        "SESSION_ENCRYPTION_KEY=32-bytes-exatos-aqui",
        "YOUR_OUTLOOK_PASSWORD_",
        "YOUR_CHATGPT_PASSWORD_"
    )

    foreach ($path in $tracked) {
        if (-not (Test-Path $path -PathType Leaf)) { continue }

        $raw = ""
        try {
            $raw = Get-Content -LiteralPath $path -Raw -ErrorAction Stop
        } catch {
            continue
        }

        if ([string]::IsNullOrEmpty($raw)) { continue }
        if (Is-BinaryContent -Text $raw) { continue }

        $lines = $raw -split "`r?`n"
        for ($i = 0; $i -lt $lines.Count; $i++) {
            $line = $lines[$i]
            if ([string]::IsNullOrWhiteSpace($line)) { continue }

            $isSafeExample = $false
            foreach ($safeRegex in $safeLineRegex) {
                if ($line -match $safeRegex) {
                    $isSafeExample = $true
                    break
                }
            }
            if ($isSafeExample) { continue }

            foreach ($pattern in $secretPatterns) {
                if ($line -match $pattern.Regex) {
                    $findings.Add([pscustomobject]@{
                        File    = $path
                        Line    = $i + 1
                        Pattern = $pattern.Name
                        Snippet = ($line.Trim())
                    })
                    break
                }
            }
        }
    }

    if ($findings.Count -gt 0) {
        $findings |
            Sort-Object File, Line |
            Select-Object File, Line, Pattern, Snippet |
            Format-Table -AutoSize
    } else {
        Write-Host "OK: nenhum padrão de segredo real encontrado nos arquivos versionados." -ForegroundColor Green
    }

    Write-Section "3) Arquivos locais sensíveis (não versionados)"

    $localSensitive = @(
        ".env",
        "accounts.json",
        "tools/auto-login/credentials.json"
    )

    $localFound = @()
    foreach ($p in $localSensitive) {
        if (Test-Path $p) { $localFound += $p }
    }

    if ($localFound.Count -gt 0) {
        $localFound | ForEach-Object { Write-Host "[WARN] presente localmente: $_" -ForegroundColor Yellow }
        Write-Host "Isso é esperado para operação, mas confirme que não estão em staging." -ForegroundColor Yellow
    } else {
        Write-Host "OK: nenhum arquivo local sensível encontrado." -ForegroundColor Green
    }

    $hasBlockers = ($forbiddenTracked.Count -gt 0) -or ($findings.Count -gt 0)

    if ($Strict -and $localFound.Count -gt 0) {
        $hasBlockers = $true
    }

    Write-Section "Resultado"
    if ($hasBlockers) {
        Write-Host "FALHOU: publicação bloqueada por segurança." -ForegroundColor Red
        exit 1
    }

    Write-Host "PASSOU: check de publicação sem bloqueios." -ForegroundColor Green
    exit 0
}
finally {
    Pop-Location
}
