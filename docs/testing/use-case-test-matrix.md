# Matriz De Casos De Uso, Testes E Evidencias

Data da revisao: 2026-06-16.

## Objetivo

Este documento transforma os casos de uso de `docs/product/use-cases.md` em uma matriz operacional de teste. A leitura correta e:

```text
caso de uso -> regra critica -> arquivo -> validacao -> evidencia -> limite
```

O Project Sentinel deve ser usado apenas com contas, sessoes e provedores autorizados. A documentacao publica usa placeholders para host, conta, chave e estado local.

## Casos De Uso

| ID | Caso de uso | Regra critica | Arquivos | Validacao | Limite |
| --- | --- | --- | --- | --- | --- |
| UC-01 | Consultar modelos OpenAI-compatible | Modelo logico precisa resolver para provider configurado | `internal/delivery/http`, `configs/models.json` | testes Go de registry/handlers + smoke `GET /v1/models` | Smoke exige servidor local rodando |
| UC-02 | Enviar chat completion pelo roteador | Request nao pode vazar header, cookie ou sessao no erro | adapters, sanitizers, router | testes de adapter/sanitizer + smoke `sentinelctl test` | Upstream depende de sessao autorizada |
| UC-03 | Atender Responses API | `/v1/responses` precisa rotear conversao ou passthrough sem duplicar `/v1/v1` | `openai_responses_handlers.go` | `openai_responses_*_test.go` | Cliente real pode exigir fixture local |
| UC-04 | Forcar conta especifica | Conta forcada deve falhar claramente se indisponivel | state store, router, admin API | testes de force mode + smoke com header | Nao deve haver fallback silencioso |
| UC-05 | Operar saude, consumo e logs | CLI nao deve exibir segredo completo | `tools/sentinelctl.ps1`, admin endpoints | parser PowerShell + operacao manual | Prints publicos precisam mascarar conta/key |
| UC-06 | Publicar sem vazar segredo | Guard deve bloquear `.env`, sessao, banco e key real versionada | `tools/publication_guard.ps1`, `.gitignore` | `publication_guard.ps1` | Strict mode pode acusar arquivos locais nao versionados |

## Regras Que Nao Podem Quebrar

| ID | Regra | Onde aparece | Validacao |
| --- | --- | --- | --- |
| REG-01 | API key local protege endpoints sensiveis | router/admin API | testes Go e smoke com header |
| REG-02 | Sessao fica criptografada em disco | crypto/storage | `encryptor_test.go` |
| REG-03 | Header interno nao deve seguir para upstream | adapter/sanitizer | `internal_trace_sanitizer_test.go` |
| REG-04 | Force mode nao faz fallback silencioso | state store/router | testes de state store e comando `force` |
| REG-05 | Publicacao nao pode conter segredo real | guard/.gitignore | `tools/publication_guard.ps1` |
| REG-06 | Docs publicas usam placeholders | README/docs/examples | varredura de higiene publica |

## Matriz De Validacao

| Camada | Comando | O que comprova |
| --- | --- | --- |
| Go | `go test ./...` | Regressao de router, state, crypto, adapters e handlers |
| Go local do repo | `.tools/go/bin/go.exe test ./...` | Mesmo teste quando o runtime local existir |
| PowerShell | parser de `tools/sentinelctl.ps1` | Script operacional segue parseavel |
| Guard | `tools/publication_guard.ps1` | Sem segredo real versionado |
| Guard strict | `tools/publication_guard.ps1 -Strict` | Ambiente local nao carrega arquivo sensivel solto |
| Smoke API | `curl /healthz`, `GET /v1/models`, Postman | Servidor local responde |
| Smoke CLI | `sentinelctl status`, `accounts`, `test` | Operacao local esta funcional |

## Evidencias

| Evidencia | Caminho | O que prova |
| --- | --- | --- |
| Casos de uso | `docs/product/use-cases.md` | Fluxos P0, falhas aceitaveis e falhas inaceitaveis |
| Guia de testes | `docs/testing/testing-guide.md` | Comandos por camada e interpretacao de falhas |
| Validacao local | `docs/testing/local-validation.md` | Checks executados nesta maquina e limitacoes reais |
| Operacao PowerShell | `docs/OPERACAO_POWERSHELL.md` | Comandos de status, contas, consumo, logs e controle |
| Checklist publico | `docs/CHECKLIST_PUBLICACAO_GITHUB.md` | Revisao antes de PR/publicacao |

## Gaps Tecnicos

| Gap | Impacto | Acao recomendada |
| --- | --- | --- |
| Go ausente nesta maquina de revisao | Go tests nao foram repetidos localmente neste ciclo | Rodar em ambiente com Go 1.25+ ou `.tools/go` |
| Smoke ponta a ponta depende de sessao autorizada | Nao deve ir para CI publico | Manter como validacao local controlada |
| Screenshots operacionais ainda nao existem | README fica menos visual | Capturar terminal com account IDs e keys mascarados |
| Strict mode pode bloquear arquivos locais do operador | Ambiente pode estar correto no Git mas sujo localmente | Rodar `publication_guard.ps1 -Strict` antes de release |
