# Casos De Uso E Matriz De Testes

Data da revisão: 2026-06-16.

O Project Sentinel é um gateway local OpenAI-compatible. Ele deve ser usado apenas com contas, sessões e provedores que o operador tem autorização para usar. A documentação pública evita exemplos com conta real, host real ou segredo.

A versão operacional da matriz, focada em comandos de validação e evidências, fica em `docs/testing/use-case-test-matrix.md`.

## 1. Público e contexto

| Público | Situação real de uso | Problema que tenta resolver | Criticidade |
| --- | --- | --- | --- |
| Pessoa desenvolvedora | Usa IDE/CLI/app compatível com OpenAI e quer um endpoint local padronizado | Evitar configurar cada ferramenta separadamente | P0 |
| Operador do gateway | Precisa ver saúde de contas, cooldown, consumo e logs | Diagnosticar falha sem abrir arquivos sensíveis | P0 |
| Mantenedor do repositório | Precisa publicar código sem vazar `.env`, sessão ou banco local | Reduzir risco de exposição no GitHub | P0 |
| Revisor técnico | Precisa entender roteamento, leases, adapters e estado | Avaliar manutenção e riscos do gateway | P1 |

## 2. Casos de uso principais

| ID | Caso de uso | Entrada | Saída esperada | Falha aceitável | Falha inaceitável |
| --- | --- | --- | --- | --- | --- |
| UC-01 | Consultar modelos via API OpenAI-compatible | `GET /v1/models` com API key local | Lista de modelos lógicos configurados | Modelo ausente aparecer como erro claro | Retornar modelo não configurado sem rastreio |
| UC-02 | Enviar chat completion por `sentinel-router` | Request OpenAI-compatible com `model=sentinel-router` | Resposta convertida para formato esperado pelo cliente | Upstream retornar erro e o Sentinel registrar estado | Vazar cookie, access token ou header interno no erro |
| UC-03 | Atender cliente Responses API | `POST /v1/responses` | Roteamento correto para conversão ou passthrough conforme headers | Payload incompatível gerar erro controlado | Quebrar cliente por rota duplicada `/v1/v1` sem fallback |
| UC-04 | Forçar conta específica para diagnóstico | Header ou comando com `<ACCOUNT_ID>` | Request usa a conta forçada ou falha claramente | Conta forçada indisponível gerar erro | Fallback silencioso para outra conta |
| UC-05 | Operar saúde e consumo no PowerShell | `sentinelctl status`, `accounts`, `consumo`, `logs` | Estado legível sem segredo em tela | Conta sem quota real cair em fallback local | Mostrar token, sessão ou key completa |
| UC-06 | Publicar repositório com segurança | `publication_guard.ps1` | Guard passa ou bloqueia com motivo | Avisar arquivo local sensível não versionado | Permitir `.env`, sessão, banco ou key real versionados |

## 3. Regras e invariantes

| ID | Regra | Onde aparece | Teste/evidência |
| --- | --- | --- | --- |
| REG-01 | API key local deve proteger rotas sensíveis | `internal/delivery/http/router.go` | Testes de handlers e smoke com header |
| REG-02 | Sessões devem ficar criptografadas em disco | `internal/infrastructure/crypto` e `storage` | `encryptor_test.go` |
| REG-03 | Header interno do Sentinel não deve ir para upstream | adapters e sanitizers | `internal_trace_sanitizer_test.go` |
| REG-04 | Conta forçada não deve cair em fallback silencioso | state store, router e docs de operação | testes de force mode no state store |
| REG-05 | `/v1/responses` precisa rotear cliente Codex para passthrough quando aplicável | `openai_responses_handlers.go` | `openai_responses_routing_test.go` |
| REG-06 | Publicação pública não pode conter segredo ou estado local | `publication_guard.ps1` e `.gitignore` | execução do guard |

## 4. Matriz de testes por caso de uso

| Caso de uso | Unitário | Integração/local | Smoke/manual | Evidência atual |
| --- | --- | --- | --- | --- |
| UC-01 | registry/handlers | coleção Postman com servidor rodando | `GET /v1/models` | Postman collection + docs |
| UC-02 | adapter/chatgpt tests | depende de sessão autorizada e ambiente local | `sentinelctl test -Effort high` | documentado; não executado nesta revisão |
| UC-03 | `openai_responses_*_test.go` | server local | request Responses API | testes versionados |
| UC-04 | state store force mode | server local | `force`, `unforce`, request com header | testes versionados |
| UC-05 | scripts PowerShell | operação local | `status`, `accounts`, `logs` | docs de operação |
| UC-06 | publication guard | execução local | `.\tools\publication_guard.ps1` | registrado em `docs/testing/local-validation.md` |

## 5. Evidências sem dado sensível

| Evidência | O que prova | Pode publicar? |
| --- | --- | --- |
| Saída `publication_guard.ps1` sem bloqueio | Repositório não tem segredo real versionado | Sim, se não mostrar caminhos privados |
| Saída `go test ./...` | Regressão Go coberta | Sim |
| Screenshot de `sentinelctl status` com dados mascarados | Operação está legível | Sim, se account IDs e tokens estiverem ocultos |
| Postman com placeholders | Rotas existem e podem ser testadas | Sim |

## 6. Lacunas atuais

| Lacuna | Impacto | Próxima ação |
| --- | --- | --- |
| Go não estava disponível no ambiente local desta revisão | Testes Go não puderam ser executados aqui | Rodar `go test ./...` em máquina com Go 1.25+ ou runtime `.tools/go` |
| Smoke ponta a ponta depende de sessão autorizada | Não é seguro nem reproduzível no CI público | Manter como validação local documentada |
| Screenshots operacionais ainda não existem | README fica menos visual | Criar imagens com account IDs e keys mascarados |

