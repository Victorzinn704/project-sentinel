# Como Contribuir

Este repositorio prioriza seguranca local, previsibilidade e auditoria. Mudancas devem preservar o comportamento explicito das politicas e manter o projeto simples de executar em maquina limpa.

## Fluxo recomendado

1. Abra PR pequeno com objetivo claro.
2. Explique impacto em politicas, storage, API local ou CLI.
3. Rode os checks antes de pedir revisao.

## Checks locais

```bash
go test ./...
go build -o /tmp/sentinel ./cmd/sentinel
```

No Windows, rode tambem:

```powershell
.\tools\publication_guard.ps1
```

## Regras de seguranca

- nao publique API keys, tokens, bancos SQLite locais ou logs reais;
- trate exemplos de configuracao como placeholders;
- documente qualquer mudanca que altere autorizacao, auditoria ou retencao.
