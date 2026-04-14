# Arquitetura

O Project Sentinel é um gateway local OpenAI-compatible com foco em operação de contas ChatGPT/Codex. Ele separa entrada HTTP, resolução de modelo, escolha de conta, lease, chamada upstream e persistência de estado.

## Visão Geral

```txt
Cliente OpenAI-compatible
        |
        v
Sentinel HTTP API
        |
        v
Model Registry
        |
        v
Scheduler + Lease Manager
        |
        v
Provider Adapter
        |
        v
ChatGPT/Codex upstream
```

## Componentes

<table>
  <tr>
    <td><b>HTTP Delivery</b><br><code>internal/delivery/http</code><br>Handlers OpenAI-compatible, admin e health.</td>
    <td><b>Model Registry</b><br><code>internal/registry</code><br>Mapeia modelo lógico para provider e upstream.</td>
  </tr>
  <tr>
    <td><b>State Store</b><br><code>internal/infrastructure/state</code><br>SQLite com contas, leases, cooldowns e uso.</td>
    <td><b>Session Store</b><br><code>internal/infrastructure/storage</code><br>Sessões criptografadas em arquivos.</td>
  </tr>
  <tr>
    <td><b>Adapters</b><br><code>internal/adapter</code><br>ChatGPT/Codex, Claude e Gemini.</td>
    <td><b>Usecases</b><br><code>internal/usecase</code><br>Registro e validação de contas.</td>
  </tr>
</table>

## Fluxo De Uma Request

1. O cliente chama `POST /v1/chat/completions`.
2. O handler valida a API key local.
3. O registry resolve `model` para `provider` e `upstream_model`.
4. O scheduler escolhe uma conta elegível daquele provider.
5. O lease manager reserva a conta para a request.
6. O session store carrega a sessão criptografada.
7. O adapter traduz OpenAI Chat Completions para Codex Responses.
8. O adapter chama o upstream ChatGPT/Codex.
9. A resposta é convertida de volta para OpenAI-compatible.
10. O state store registra sucesso, latência, uso diário ou falha.

## Superfície OpenAI-compatible

Rotas principais:

```txt
GET  /v1/models
POST /v1/chat/completions
POST /v1/responses
```

Aliases aceitos para reduzir erro de configuração em clientes:

```txt
GET  /models
GET  /v1/v1/models
POST /chat/completions
POST /v1/v1/chat/completions
POST /responses
POST /v1/v1/responses
```

`/v1/responses` converte payload básico da Responses API para Chat Completions internamente, executa pelo mesmo scheduler/adapters e devolve formato Responses API.

## Modelo E Esforço

`configs/models.json` contém os modelos lógicos:

```json
{
  "id": "sentinel-router",
  "provider": "chatgpt",
  "upstream": "gpt-5.4",
  "upstream_model": "gpt-5.4"
}
```

`DEFAULT_REASONING_EFFORT` aceita:

```txt
high
xhigh
```

O adapter ChatGPT promove qualquer request abaixo de `high` para `high`. Isso evita que clientes externos reduzam a qualidade sem querer.

`DEFAULT_MODEL=sentinel-router` é aplicado quando uma request OpenAI-compatible chega sem `model`. O handler injeta esse modelo no payload interno antes de chamar o adapter, então clientes mal configurados deixam de falhar com `missing_model`.

## Estado Das Contas

Estados principais:

```txt
active              elegível para rotação
cooldown            pausada temporariamente por rate limit
disabled            desligada manualmente
attention_required  sessão inválida, 401/403 ou ação manual necessária
```

Campos importantes:

```txt
daily_usage_count   sucessos no dia
daily_limit         limite operacional local
latency_ewma_ms     média móvel de latência
error_count         falhas consecutivas/recentes
active_leases       requests em andamento
cooldown_until      fim do cooldown
```

## Rotação

Estratégias:

```txt
round_robin
least_used
random
weighted_round_robin
```

Configuração:

```env
ROTATION_STRATEGY=round_robin
```

## Force Mode

Force mode global:

```powershell
.\tools\sentinelctl.ps1 force acc_contato_deskimperial_online
```

Header por request:

```txt
X-Sentinel-Force-Account: acc_contato_deskimperial_online
```

O Sentinel não faz fallback silencioso se a conta forçada estiver indisponível.

## Falhas E Política

`429`:

```txt
Registra cooldown na conta selecionada.
Usa Retry-After real quando o upstream envia.
```

`401/403`:

```txt
Marca falha de autenticação.
Pode levar a attention_required.
Não tenta esconder o erro.
```

`5xx/rede`:

```txt
Registra falha transitória.
Não marca a conta como inválida automaticamente.
```

## Segurança

Arquivos sensíveis:

```txt
.env
sessions/*.json.enc
sessions/state.db
```

Boas práticas:

```txt
Não commitar secrets.
Não imprimir tokens de sessão.
Rotacionar SENTINEL_API_KEY quando necessário.
Manter HTTP_ADDR local para uso pessoal.
Guardar SESSION_ENCRYPTION_KEY em local seguro.
```

## Pontos De Extensão

Adicionar provider:

```txt
1. Implementar adapter em internal/adapter.
2. Registrar no ProviderAdapterRegistry.
3. Adicionar modelos em configs/models.json.
4. Registrar contas com provider correspondente.
```

Migrar SQLite para Postgres:

```txt
1. Manter interfaces consumer-side dos handlers.
2. Implementar store compatível.
3. Migrar accounts, leases e routing_settings.
```
