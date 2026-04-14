$url = "http://localhost:8080/v1/chat/completions"
$headers = @{
    "Authorization" = "Bearer super-senha-sentinel-123"
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
        $measure = Measure-Command {
            $response = Invoke-RestMethod -Method Post -Uri $url -Headers $headers -Body $body -TimeoutSec 30
        }
        $ms = [math]::Round($measure.TotalMilliseconds, 2)
        Write-Host "OK! (${ms}ms) " -ForegroundColor Green

        $content = "Sem resposta visível"
        if ($null -ne $response.choices -and $response.choices.Count -gt 0) {
            if ($null -ne $response.choices[0].message) {
                $content = $response.choices[0].message.content
            } elseif ($null -ne $response.choices[0].delta) {
                $content = $response.choices[0].delta.content
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
