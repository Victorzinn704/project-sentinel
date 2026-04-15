$url = "http://localhost:8080/v1/chat/completions"

$apiKey = $env:SENTINEL_API_KEY
if (-not $apiKey -and (Test-Path ".env")) {
    $line = Get-Content ".env" | Where-Object { $_ -match '^\s*SENTINEL_API_KEY\s*=' } | Select-Object -First 1
    if ($line) {
        $apiKey = (($line -split "=", 2)[1]).Trim()
    }
}

if (-not $apiKey) {
    $apiKey = "replace-with-a-random-local-api-key"
    Write-Host "[WARN] SENTINEL_API_KEY não encontrada. Ajuste no ambiente ou no .env." -ForegroundColor Yellow
}

$headers = @{
    "Authorization" = "Bearer $apiKey"
    "Content-Type"  = "application/json"
}

Write-Host "===============================================" -ForegroundColor Cyan
Write-Host "      Iniciando Teste de Carga - Sentinel      " -ForegroundColor Cyan
Write-Host "===============================================" -ForegroundColor Cyan

for ($i = 1; $i -le 5; $i++) {
    $body = @{
        model = "sentinel-router"
        messages = @(
            @{ role = "user"; content = "Opa, esta é a requisição de teste número $i. Responda apenas com 'Recebido $i'." }
        )
        stream = $false
    } | ConvertTo-Json -Depth 5 -Compress

    Write-Host "[$i/5] Disparando request para o router... " -NoNewline
    
    try {
        $stopwatch = [System.Diagnostics.Stopwatch]::StartNew()
        $apiResponse = Invoke-RestMethod -Method Post -Uri $url -Headers $headers -Body $body -TimeoutSec 30
        $stopwatch.Stop()
        $ms = [math]::Round($stopwatch.Elapsed.TotalMilliseconds, 2)
        Write-Host "OK! (${ms}ms) " -ForegroundColor Green

        $content = "Sem resposta visível"
        if ($null -ne $apiResponse.choices -and $apiResponse.choices.Count -gt 0) {
            if ($null -ne $apiResponse.choices[0].message) {
                $content = $apiResponse.choices[0].message.content
            } elseif ($null -ne $apiResponse.choices[0].delta) {
                $content = $apiResponse.choices[0].delta.content
            }
        }
        Write-Host "   > Resposta do ChatGPT: " -NoNewline
        Write-Host "$content" -ForegroundColor Yellow
    } catch {
        Write-Host "ERRO!" -ForegroundColor Red
        
        $errMsg = $_.Exception.Message
        if ($_.ErrorDetails -and $_.ErrorDetails.Message) {
            $errMsg = $_.ErrorDetails.Message
        }
        Write-Host "   > Falha: $errMsg" -ForegroundColor Red
    }
    Start-Sleep -Seconds 1
}

Write-Host "===============================================" -ForegroundColor Cyan
Write-Host "            Testes Finalizados!                " -ForegroundColor Cyan
Write-Host "===============================================" -ForegroundColor Cyan
