# Instalacao, Configuracao e Operacao

Este guia e o manual completo para colocar o Project Sentinel no ar, configurar clientes e operar no dia a dia.

Se voce nunca mexeu com terminal, siga exatamente na ordem. Nao pule etapa.

## 0. Mapa rapido (2 minutos)

1. Abrir a pasta do projeto no PowerShell
2. Criar `.env` a partir de `.env.example`
3. Subir o Sentinel com Docker
4. Testar se respondeu `ok`
5. Cadastrar contas
6. Monitorar consumo
7. Rodar varredura de publicacao antes de `git push`

## 0.1 Cards de Stack (resumo visual)

| Card | O que e | Arquivo/Pasta principal | Comando util |
|---|---|---|---|
| Go API | Servidor HTTP e regras de roteamento | `cmd/sentinel`, `internal/` | `.\.tools\go\bin\go.exe test ./...` |
| SQLite | Estado de contas, leases e cooldown | `sessions/state.db`, `internal/infrastructure/state` | `.\tools\sentinelctl.ps1 accounts` |
| PowerShell CLI | Operacao diaria e suporte ao Codex | `tools/sentinelctl.ps1` | `.\tools\sentinelctl.ps1 status` |
| Docker | Execucao padrao local/VM | `deployments/docker-compose.yml` | `docker compose -f deployments/docker-compose.yml up --build -d` |
| Seguranca de publicacao | Bloqueio de arquivos/padroes sensiveis | `tools/publication_guard.ps1` | `.\tools\publication_guard.ps1` |

## 1. O que e o Project Sentinel

O Project Sentinel e um gateway OpenAI-compatible que:

- recebe requests em formato OpenAI
- roteia para contas ChatGPT/Codex
- controla rotacao, cooldown, force mode e estado de contas
- expone monitoramento operacional e consumo por conta

## 2. Pre-requisitos

### 2.1 Desenvolvimento local (Windows)

- PowerShell 5+ (ou PowerShell 7)
- Docker Desktop (recomendado)
- Git
- Opcional: Go 1.25+ (apenas se quiser build local sem Docker)

### 2.2 Servidor Linux (deploy)

- Docker + Docker Compose plugin (`docker compose`)
- Porta 8080 liberada na VM
- SSH com chave privada

## 3. Instalacao

### 3.1 Clonar o projeto

```powershell
git clone <URL_DO_REPO>
cd project-sentinel
```

### 3.2 Criar `.env`

```powershell
Copy-Item .env.example .env
```

Edite os campos obrigatorios em `.env`:

- `SENTINEL_API_KEY`
- `SENTINEL_ADMIN_API_KEY`
- `SESSION_ENCRYPTION_KEY` (32 bytes exatos)
- `HTTP_ADDR` (normalmente `:8080`)
- `DEFAULT_MODEL` (normalmente `sentinel-router`)

Checklist minimo do `.env`:

```txt
SENTINEL_API_KEY: preenchido com valor unico seu
SENTINEL_ADMIN_API_KEY: diferente da runtime key
SESSION_ENCRYPTION_KEY: 32 caracteres
HTTP_ADDR: :8080
DEFAULT_MODEL: sentinel-router
```

### 3.3 Gerar chave de sessao de 32 bytes (opcional)

```powershell
-join ((48..57) + (65..90) + (97..122) | Get-Random -Count 32 | ForEach-Object {[char]$_})
```

## 4. Subir o Sentinel

### 4.1 Opcao recomendada: Docker

```powershell
docker compose -f deployments/docker-compose.yml up --build -d
```

Verificar:

```powershell
curl http://127.0.0.1:8080/healthz
```

Se retornar `ok`, esta no ar.

### 4.2 Opcao via `sentinelctl`

Se voce tiver o binario em `.tools/sentinel.exe`:

```powershell
.\tools\sentinelctl.ps1 start
```

Status:

```powershell
.\tools\sentinelctl.ps1 status
```

## 5. Cadastro de chaves

## 5.1 Chave da API local (SENTINEL_API_KEY)

Ver chave mascarada:

```powershell
.\tools\sentinelctl.ps1 key-show
```

Rotacionar chave:

```powershell
.\tools\sentinelctl.ps1 key-new
```

Revogar chave anterior:

```powershell
.\tools\sentinelctl.ps1 key-revoke
```

## 5.2 Chave da API administrativa (SENTINEL_ADMIN_API_KEY)

Ver chave mascarada:

```powershell
.\tools\sentinelctl.ps1 admin-key-show
```

Rotacionar chave:

```powershell
.\tools\sentinelctl.ps1 admin-key-new
```

## 5.3 Chave de sessão (SESSION_ENCRYPTION_KEY)

Rotacionar a chave de sessão e recriptografar os arquivos `sessions/*.json.enc`:

```powershell
.\tools\sentinelctl.ps1 session-key-rotate
```

Rotacionar runtime key, admin key e session key de uma vez:

```powershell
.\tools\sentinelctl.ps1 secrets-rotate
```
## 5.4 Chave usada pelo Codex CLI (CODEX_API_KEY)

Instalar provider local do Sentinel no Codex:

```powershell
.\tools\sentinelctl.ps1 codex-install
```

Persistir no ambiente do usuario Windows:

```powershell
.\tools\sentinelctl.ps1 codex-install -Persist
```

Apontar para Sentinel remoto:

```powershell
.\tools\sentinelctl.ps1 codex-install -GlobalConfig -BaseURL https://sentinel.deskimperial.online/v1
```

## 6. Cadastro de contas

Voce tem dois fluxos principais.

### 6.1 Via arquivo `accounts.json`

Use `register_accounts.ps1` com payload contendo credenciais/sessao:

```powershell
.\register_accounts.ps1 -File .\accounts.json -BaseUrl http://127.0.0.1:8080
```

### 6.2 Via auto-login

Ajuste credenciais em `tools/auto-login/credentials.json` (nao versionar esse arquivo) e rode o fluxo Python.

Exemplo de arquivo base:

- `tools/auto-login/credentials.example.json`

## 7. Configuracao de cliente OpenAI-compatible

Use no cliente/IDE:

```txt
Base URL: http://127.0.0.1:8080/v1
API Key: <SENTINEL_API_KEY>
Model: sentinel-router
```

Para uso remoto, troque `127.0.0.1` pelo host da VM.

## 8. Comandos basicos (`sentinelctl`)

Regra simples para iniciantes:

- `status` para ver se esta vivo
- `test` para validar ponta a ponta
- `accounts` para ver saude das contas
- `consumo` para ver uso

## 8.1 Ciclo de vida

```powershell
.\tools\sentinelctl.ps1 start
.\tools\sentinelctl.ps1 status
.\tools\sentinelctl.ps1 restart
.\tools\sentinelctl.ps1 stop
```

## 8.2 Teste rapido

```powershell
.\tools\sentinelctl.ps1 test -Effort high
.\tools\sentinelctl.ps1 chat -Model gpt-5.4 -Effort high -Prompt "Responda apenas: ok"
```

## 8.3 Modelos e esforco

```powershell
.\tools\sentinelctl.ps1 models
.\tools\sentinelctl.ps1 use-model gpt-5.4 -Effort high
.\tools\sentinelctl.ps1 set-effort -Effort auto
```

## 8.4 Contas

```powershell
.\tools\sentinelctl.ps1 accounts
.\tools\sentinelctl.ps1 force <account_id>
.\tools\sentinelctl.ps1 unforce
.\tools\sentinelctl.ps1 disable <account_id>
.\tools\sentinelctl.ps1 enable <account_id>
```

## 8.5 Consumo e quota

Atualizar snapshot real de quota:

```powershell
.\tools\sentinelctl.ps1 quota-refresh
```

Painel de consumo (snapshot):

```powershell
.\tools\sentinelctl.ps1 consumo
```

Painel de consumo continuo:

```powershell
.\tools\sentinelctl.ps1 consumo-watch 5
```

## 8.6 Logs

```powershell
.\tools\sentinelctl.ps1 logs
.\tools\sentinelctl.ps1 logs -Watch
```

## 9. Como ler o consumo

No painel de consumo:

- `src=chatgpt_wham_usage`: consumo real da conta no upstream
- `src=daily_usage`: fallback local por requests no Sentinel

Quando uma conta nao consegue atualizar quota (por exemplo, sessao expirada), ela cai em fallback local.

## 10. Diagnostico rapido

Servidor nao responde:

```powershell
.\tools\sentinelctl.ps1 restart
.\tools\sentinelctl.ps1 logs
```

Conta em `attention_required`:

```powershell
.\tools\sentinelctl.ps1 accounts
```

Depois de recuperar sessao da conta, rode:

```powershell
.\tools\sentinelctl.ps1 quota-refresh
```

Codex sem resposta:

- valide `.codex/config.toml`
- confirme `wire_api = "responses"`
- rode `codex exec "Responda apenas: ok"`

## 13. Publicar sem vazar segredo (obrigatorio)

Antes de abrir PR ou fazer `git push`, rode:

```powershell
.\tools\publication_guard.ps1
```

Interpretacao do resultado:

- `PASSOU`: seguro para seguir
- `FALHOU`: pare e corrija

Fluxo recomendado de publicacao segura:

```powershell
git status --short
.\tools\publication_guard.ps1
.\.tools\go\bin\go.exe test ./...
git add .
git commit -m "docs: harden publication workflow"
```

Se o guard mostrar `[BLOCK]`:

1. remover o arquivo proibido do versionamento
2. ajustar placeholders em exemplos
3. rodar o guard de novo ate passar

## 11. Deploy remoto (Oracle VM exemplo)

Pré-requisitos do caminho principal:

- subdomínio dedicado (ex.: `sentinel.seudominio.com`) apontado por `A record` para o IP público da VM (no Cloudflare, mantenha o registro como *DNS only* / nuvem cinza, senão o proxy intercepta o desafio HTTP-01 do Let's Encrypt)
- portas `80` e `443` liberadas na OCI Security List/NSG **e** no firewall local da VM (`iptables`/`ufw`)
- variáveis `SENTINEL_PUBLIC_HOST` e `LETSENCRYPT_EMAIL` preenchidas no `.env`
- `SENTINEL_ADMIN_API_KEY` diferente da runtime key

Observacao importante:

- o `docker-compose.oracle.yml` assume `80/443` no host. Não suba em uma máquina que já termine TLS para outro app nessas portas
- se quiser publicar sob um caminho (ex.: `/suporte`) no proxy de um site existente, não use este compose; configure `proxy_pass http://<vm_ip>:8080/` no nginx/Caddy principal

Exemplo minimo no `.env` do servidor:

```txt
SENTINEL_PUBLIC_HOST=sentinel.seudominio.com
LETSENCRYPT_EMAIL=ops@seudominio.com
CODEX_BASE_URL=https://sentinel.seudominio.com/v1
```

Deploy tipico com HTTPS terminado em Caddy:

```bash
cd /opt/project-sentinel
docker compose -f deployments/docker-compose.oracle.yml up --build -d
```

Validacao pos-deploy:

```bash
docker ps | grep sentinel
curl -sS https://sentinel.seudominio.com/healthz
```

## 12. Boas praticas de seguranca

- nunca commitar `.env`
- nunca commitar `sessions/*.json.enc`
- nunca commitar `tools/auto-login/credentials.json`
- rotacionar `SENTINEL_API_KEY` e `SENTINEL_ADMIN_API_KEY` periodicamente
- proteger `SESSION_ENCRYPTION_KEY` em cofre seguro
