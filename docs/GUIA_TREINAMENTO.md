# Guia de Treinamento (Train The Trainer)

Este guia ensina como ensinar o Project Sentinel para novas pessoas do time.

## 1. Objetivo do treinamento

Ao final, a pessoa deve conseguir:

- explicar o que o Sentinel resolve
- instalar e subir o projeto
- configurar cliente OpenAI-compatible
- cadastrar contas/chaves com seguranca
- operar o monitoramento e consumo
- fazer troubleshooting basico

## 2. Pitch de 60 segundos

Use este texto curto:

"O Project Sentinel e um gateway OpenAI-compatible que centraliza contas ChatGPT/Codex com controle operacional. Em vez de configurar cada ferramenta de forma isolada, voce aponta tudo para um endpoint unico. O Sentinel resolve modelo, escolhe conta, controla cooldown, registra consumo e expone comandos de operacao."

## 2.1 Cards de stack para explicacao rapida

| Card | Mensagem para o aluno |
|---|---|
| Go API | "Aqui vive a logica do roteamento e da compatibilidade OpenAI." |
| SQLite | "Aqui ficam estado das contas, cooldown e uso." |
| PowerShell CLI | "Aqui operamos o dia a dia sem abrir codigo." |
| Docker | "Aqui sobe igual em maquina local e servidor." |
| Publication Guard | "Aqui bloqueamos push com risco de segredo." |

## 3. Roteiro de aula de 15 minutos

## 3.1 Minuto 1-3: problema

- contas espalhadas em varias ferramentas
- falta de controle operacional
- baixa observabilidade de consumo e estado

## 3.2 Minuto 4-8: demonstracao

- subir Sentinel
- rodar teste `ok`
- abrir painel de consumo

## 3.3 Minuto 9-12: operacao

- `status`, `accounts`, `consumo-watch`
- `force/unforce`, `disable/enable`

## 3.4 Minuto 13-15: seguranca

- `SENTINEL_API_KEY` e rotacao
- `SESSION_ENCRYPTION_KEY`
- o que nunca commitar

## 4. Roteiro de aula de 60 minutos

## 4.1 Bloco 1: fundamentos (15 min)

- arquitetura de alto nivel
- fluxo da request
- estados de conta

## 4.2 Bloco 2: hands-on (20 min)

- instalacao e `.env`
- start/restart/status
- configuracao de cliente e codex

## 4.3 Bloco 3: operacao real (15 min)

- consumo em barra
- quota real vs fallback local
- diagnostico de conta com auth failure

## 4.4 Bloco 4: avaliacao (10 min)

- aluno sobe o ambiente sozinho
- aluno executa teste e leitura de consumo
- aluno resolve 1 incidente simples

## 5. Script de demo (copy/paste)

```powershell
.\tools\sentinelctl.ps1 status
.\tools\sentinelctl.ps1 test -Effort high
.\tools\sentinelctl.ps1 consumo
.\tools\sentinelctl.ps1 consumo-watch 5
```

## 6. Perguntas frequentes para instrutor

## 6.1 "A barra e consumo real ou so mensagem?"

Resposta:

- quota-aware: consumo real da conta (upstream)
- fallback: estimativa local por requests no Sentinel

## 6.2 "Por que uma conta esta sem quota real?"

Resposta:

- sessao da conta pode estar expirada/invalidada
- verificar logs e recuperar sessao da conta
- rodar `quota-refresh` depois

## 6.3 "Como deixar mais rapido?"

Resposta:

- usar effort `high` no Codex
- evitar `xhigh` para tarefas curtas
- monitorar latencia por conta em `accounts`

## 7. Checklist do instrutor antes da aula

- ambiente sobe com `healthz = ok`
- ao menos 1 conta funcional
- comando `test` retorna `ok`
- painel `consumo` responde
- exemplos e credenciais de treinamento separados do ambiente real

## 8. Materiais que o instrutor deve apontar

- README principal
- `docs/INSTALACAO_CONFIGURACAO_E_OPERACAO.md`
- `docs/OPERACAO_POWERSHELL.md`
- `docs/ARQUITETURA.md`
- `docs/CHECKLIST_PUBLICACAO_GITHUB.md`
