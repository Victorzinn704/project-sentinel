# Project Sentinel

Gateway local OpenAI-compatible para usar contas ChatGPT/Codex com rotação, controle operacional, monitoramento por terminal e endpoints de administração.

O Sentinel fica entre seu app/IDE e o upstream ChatGPT. Você aponta o cliente para `http://127.0.0.1:8080/v1`, usa a `SENTINEL_API_KEY`, escolhe `sentinel-router` ou `gpt-5.4`, e o projeto cuida de resolver modelo, escolher conta, criar lease, chamar o provider e registrar consumo.

## Stack

<table>
  <tr>
    <td><b>Go</b><br>Servidor HTTP, adapters, scheduler e estado.</td>
    <td><b>SQLite</b><br>Contas, cooldowns, leases, uso diário e force mode.</td>
    <td><b>PowerShell</b><br>CLI operacional em <code>tools/sentinelctl.ps1</code>.</td>
  </tr>
  <tr>
    <td><b>OpenAI-compatible API</b><br><code>/v1/models</code> e <code>/v1/chat/completions</code>.</td>
    <td><b>ChatGPT/Codex</b><br>Adapter principal usando o endpoint Codex Responses.</td>
    <td><b>AES-GCM</b><br>Sessões criptografadas em disco.</td>
  </tr>
  <tr>
    <td><b>Admin API</b><br>Contas, estado global, disable/enable e force mode.</td>
    <td><b>Rotation</b><br>Round-robin, least-used, random e weighted round-robin.</td>
    <td><b>GPT-5.4</b><br>Modelo padrão com esforço mínimo <code>high</code>.</td>
  </tr>
</table>

## O Que Ele Faz

- Expõe uma API local compatível com OpenAI para IDEs, CLIs e ferramentas que aceitam `base_url`.
- Roteia `sentinel-router` para `gpt-5.4` por padrão.
- Força `reasoning_effort` mínimo `high`; você pode subir para `xhigh`.
- Rotaciona contas ChatGPT/Codex ativas.
- Registra consumo diário por conta.
- Marca contas com falha de autenticação como `attention_required`.
- Respeita cooldown em `429` usando `Retry-After` real quando disponível.
- Permite fixar uma conta por request com `X-Sentinel-Force-Account`.
- Permite gerar, revogar e rotacionar a API key local via terminal.

## Estrutura Mental

1. **API** recebe requests OpenAI-compatible e endpoints admin.
2. **Model Registry** resolve modelo lógico para provider/upstream.
3. **Scheduler** escolhe a melhor conta disponível.
4. **Lease Manager** reserva a conta durante a request.
5. **Adapter** chama o upstream ChatGPT/Codex.
6. **State Store** grava uso, status, cooldown, latência e force mode.

## Primeira Execução No PowerShell

Abra o PowerShell e entre na pasta do projeto:

```powershell
cd C:\Users\Desktop\Documents\project-sentinel
```

Se o Windows bloquear scripts só nesta sessão:

```powershell
Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass
```

Verifique o status:

```powershell
.\tools\sentinelctl.ps1 status
```

Se não estiver rodando:

```powershell
.\tools\sentinelctl.ps1 start
```

Para reiniciar:

```powershell
.\tools\sentinelctl.ps1 restart
```

Para parar:

```powershell
.\tools\sentinelctl.ps1 stop
```

## Como Usar Em Um Cliente OpenAI-compatible

Configure seu app/IDE/CLI assim:

```txt
Base URL: http://127.0.0.1:8080/v1
API Key: valor de SENTINEL_API_KEY no .env
Model: sentinel-router
```

Se o cliente roda no mesmo computador do Sentinel, use `127.0.0.1` ou `localhost`. Se o cliente roda em outro computador, container, celular, servidor cloud ou ferramenta hospedada fora da sua máquina, ele não consegue enxergar `127.0.0.1`; nesse caso rode o Sentinel em uma VPS/servidor ou exponha por um túnel seguro com HTTPS.

Modelos principais:

```txt
sentinel-router -> gpt-5.4
gpt-5.4        -> gpt-5.4
gpt-5.4-pro    -> gpt-5.4-pro
gpt-5-codex    -> gpt-5-codex
codex-latest   -> gpt-5.4
```

## Usar No Codex CLI

O jeito mais limpo é deixar o Codex enxergar só um provider local chamado `sentinel`. A chave real não fica gravada no `config.toml`; o arquivo aponta para `env_key = "CODEX_API_KEY"` e o `sentinelctl` exporta essa variável.

Instalar/atualizar o provider do Codex:

```powershell
.\tools\sentinelctl.ps1 codex-install
```

Isso cria ou atualiza:

```txt
%USERPROFILE%\.codex\config.toml
```

Bloco gerenciado criado:

```toml
model = "sentinel-router"
model_provider = "sentinel"
model_reasoning_effort = "xhigh"

[model_providers.sentinel]
name = "Project Sentinel"
base_url = "http://127.0.0.1:8080/v1"
wire_api = "responses"
env_key = "CODEX_API_KEY"
```

O comando usa `DEFAULT_MODEL` e `DEFAULT_REASONING_EFFORT` do `.env` quando você não passa `-Model` ou `-Effort`. No seu setup atual, isso fica `sentinel-router` com `xhigh`.

O comando também define na sessão atual do PowerShell:

```txt
CODEX_API_KEY=<mesmo valor de SENTINEL_API_KEY>
CODEX_BASE_URL=http://127.0.0.1:8080/v1
CODEX_MODEL=sentinel-router
```

Se quiser que outro PowerShell também enxergue a key sem rodar o comando de novo:

```powershell
.\tools\sentinelctl.ps1 codex-install -Persist
```

Use `-Persist` com critério: ele grava `CODEX_API_KEY` no ambiente de usuário do Windows. É prático, mas continua sendo segredo local.

## Teste Rápido

Teste o caminho completo local -> Sentinel -> ChatGPT:

```powershell
.\tools\sentinelctl.ps1 test -Effort high
```

Teste com esforço altíssimo:

```powershell
.\tools\sentinelctl.ps1 chat -Model gpt-5.4 -Effort xhigh -Prompt "Responda apenas: ok"
```

Resposta esperada:

```txt
ok
```

Para menor latência, use `-Effort high`. `xhigh` tende a demorar mais porque o upstream pensa mais antes de emitir texto. O Sentinel envia um chunk inicial no streaming para o cliente não ficar sem evento enquanto o upstream prepara a resposta.

## Monitoramento

Ver visão geral:

```powershell
.\tools\sentinelctl.ps1 status
```

Ver consumo por conta:

```powershell
.\tools\sentinelctl.ps1 accounts
```

Ver modelos carregados:

```powershell
.\tools\sentinelctl.ps1 models
```

Ver logs ao vivo:

```powershell
.\tools\sentinelctl.ps1 logs -Watch
```

## Trocar Modelo E Esforço

Usar GPT-5.4 com esforço alto:

```powershell
.\tools\sentinelctl.ps1 use-model gpt-5.4 -Effort high
.\tools\sentinelctl.ps1 restart
```

Usar GPT-5.4 com esforço altíssimo:

```powershell
.\tools\sentinelctl.ps1 use-model gpt-5.4 -Effort xhigh
.\tools\sentinelctl.ps1 restart
```

Alterar só o esforço padrão:

```powershell
.\tools\sentinelctl.ps1 set-effort -Effort xhigh
.\tools\sentinelctl.ps1 restart
```

O adapter promove qualquer request abaixo de `high` para `high`. Se quiser máximo, mande `xhigh`.

## Gerenciar API Key Local

Ver key mascarada:

```powershell
.\tools\sentinelctl.ps1 key-show
```

Gerar key nova e revogar a antiga imediatamente:

```powershell
.\tools\sentinelctl.ps1 key-new
```

Revogar por rotação:

```powershell
.\tools\sentinelctl.ps1 key-revoke
```

Gerar com mais bytes:

```powershell
.\tools\sentinelctl.ps1 key-new -Bytes 48
```

Gerar agora e reiniciar depois:

```powershell
.\tools\sentinelctl.ps1 key-new -NoRestart
.\tools\sentinelctl.ps1 restart
```

Formato gerado:

```txt
sk-sentinel-<random-base64url>
```

## Controlar Contas

Forçar uma conta específica:

```powershell
.\tools\sentinelctl.ps1 force acc_contato_deskimperial_online
```

Limpar force mode:

```powershell
.\tools\sentinelctl.ps1 unforce
```

Desabilitar uma conta:

```powershell
.\tools\sentinelctl.ps1 disable acc_suporte_deskimperial_online
```

Reabilitar uma conta:

```powershell
.\tools\sentinelctl.ps1 enable acc_suporte_deskimperial_online
```

## Configuração

Arquivo principal:

```txt
.env
```

Variáveis importantes:

```env
HTTP_ADDR=:8080
SESSION_STORE_PATH=./sessions
STATE_DB_PATH=./sessions/state.db
MODELS_CONFIG_PATH=./configs/models.json
ROTATION_STRATEGY=round_robin
DEFAULT_MODEL=sentinel-router
DEFAULT_REASONING_EFFORT=high
REQUEST_TIMEOUT_SECONDS=120
SENTINEL_API_KEY=sk-sentinel-sua-key
SESSION_ENCRYPTION_KEY=32-bytes-exatos-aqui
```

Estratégias de rotação aceitas:

```txt
round_robin
least_used
random
weighted_round_robin
```

`DEFAULT_MODEL` é usado quando algum cliente manda request sem `model`. Mantenha como `sentinel-router` para cair no roteador padrão, que atualmente aponta para `gpt-5.4`.

## Endpoints

Health:

```txt
GET /healthz
GET /readyz
```

OpenAI-compatible:

```txt
GET  /models
GET  /v1/models
GET  /v1/v1/models
POST /chat/completions
POST /v1/chat/completions
POST /v1/v1/chat/completions
POST /responses
POST /v1/responses
POST /v1/v1/responses
```

Admin:

```txt
GET  /admin/accounts
GET  /admin/state
POST /admin/force
POST /admin/accounts/:id/disable
POST /admin/accounts/:id/enable
```

Registro de conta:

```txt
POST /accounts
```

Todos os endpoints protegidos aceitam:

```txt
X-API-Key: <SENTINEL_API_KEY>
Authorization: Bearer <SENTINEL_API_KEY>
```

## Build E Testes

Use o Go local do repo:

```powershell
$env:GOCACHE = Join-Path (Get-Location) ".tools\gocache"
.\.tools\go\bin\go.exe test ./...
.\.tools\go\bin\go.exe build -o .\.tools\sentinel.exe .\cmd\sentinel
```

## Segurança

- Não commite `.env`.
- Não commite `sessions/*.json.enc`.
- Guarde `SESSION_ENCRYPTION_KEY`; sem ela as sessões criptografadas não abrem.
- Rode em `127.0.0.1` se for uso local.
- Troque `SENTINEL_API_KEY` com `key-new` quando compartilhar tela, logs ou ambiente.

## Documentação Adicional

- [Operação no PowerShell](docs/OPERACAO_POWERSHELL.md)
- [Arquitetura](docs/ARQUITETURA.md)

## Licença

MIT. Veja [LICENSE](LICENSE).
