# Guia De Testes

Este guia descreve quais validações existem e quando usar cada uma. Não use sessão real, API key real ou banco local como fixture pública.

A matriz operacional que liga casos de uso, regras, arquivos, testes, evidências e limites fica em `docs/testing/use-case-test-matrix.md`.

## 1. Pré-requisitos

| Item | Uso |
| --- | --- |
| Go 1.25+ | Rodar a suíte Go. |
| PowerShell | Rodar `sentinelctl.ps1` e `publication_guard.ps1`. |
| Docker | Subir o gateway local para smoke/API, se necessário. |
| `.env` local | Necessário apenas para execução real; não versionar. |

## 2. Testes Go

Com Go no PATH:

```powershell
go test ./...
```

Com runtime Go local do repositório:

```powershell
$env:GOCACHE = Join-Path (Get-Location) ".tools\gocache"
.\.tools\go\bin\go.exe test ./...
```

Áreas cobertas hoje:

- criptografia de sessão;
- state store SQLite;
- routing de `/v1/responses`;
- passthrough Codex quando aplicável;
- sanitização de headers internos;
- quota/runtime quota;
- memória e parsing do adapter ChatGPT.

## 3. Guard de publicação

Obrigatório antes de PR ou push:

```powershell
.\tools\publication_guard.ps1
```

Modo mais rígido, bloqueando também arquivos sensíveis presentes localmente:

```powershell
.\tools\publication_guard.ps1 -Strict
```

O guard verifica:

- `.env` e arquivos de sessão versionados;
- bancos SQLite versionados;
- credentials de auto-login;
- chaves `sk-sentinel-*` reais;
- bearer/access tokens suspeitos;
- `SESSION_ENCRYPTION_KEY` real em arquivo versionado.

## 4. Smoke local

Subir com Docker:

```powershell
docker compose -f deployments/docker-compose.yml up --build -d
```

Health:

```powershell
curl http://127.0.0.1:8080/healthz
curl http://127.0.0.1:8080/readyz
```

Teste pelo CLI:

```powershell
.\tools\sentinelctl.ps1 status
.\tools\sentinelctl.ps1 models
.\tools\sentinelctl.ps1 test -Effort high
```

O teste ponta a ponta com upstream depende de sessão válida e autorizada. Não deve rodar em CI público.

## 5. Postman

A coleção `Sentinel_Tests.postman_collection.json` pode validar rotas locais quando o servidor estiver de pé.

Variáveis recomendadas:

| Variável | Valor local |
| --- | --- |
| `baseUrl` | `http://127.0.0.1:8080` |
| `apiKey` | valor local de `SENTINEL_API_KEY` |

Não exporte coleção com key real preenchida.

## 6. Interpretação de falhas

| Falha | Leitura provável |
| --- | --- |
| `missing_model` | Cliente não enviou model e `DEFAULT_MODEL` não foi aplicado como esperado. |
| `401/403` | API key local ausente/incorreta ou sessão upstream exige atenção. |
| `429` | Upstream aplicou rate limit; conta deve entrar em cooldown. |
| `attention_required` | Sessão inválida ou ação manual necessária. |
| Guard bloqueou publicação | Corrigir arquivo/padrão antes de qualquer push. |

