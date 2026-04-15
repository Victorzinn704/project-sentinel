# Checklist de Publicacao no GitHub

Use este checklist antes de abrir o repositorio para publico/equipe.

## 1. Higiene de segredos

Confirme que estes arquivos NAO serao publicados:

- `.env`
- `sessions/*`
- `*.db`, `*.db-wal`, `*.db-shm`
- `tools/auto-login/credentials.json`
- logs locais (`*.log`)

Valide rapidamente:

```powershell
git status --short
```

Rode a varredura automática (obrigatório):

```powershell
.\tools\publication_guard.ps1
```

Se falhar, NÃO publique. Corrija todos os itens `[BLOCK]` primeiro.

## 2. Revisao de `.gitignore`

Verifique se continua cobrindo:

- segredos (`.env`, sessions, DB)
- artefatos locais
- credenciais de auto-login

## 3. Sanidade tecnica minima

Rode pelo menos:

```powershell
.\tools\sentinelctl.ps1 status
.\tools\sentinelctl.ps1 test -Effort high
.\tools\sentinelctl.ps1 consumo
```

Se usar Go local:

```powershell
$env:GOCACHE = Join-Path (Get-Location) ".tools\gocache"
.\.tools\go\bin\go.exe test ./...
```

## 4. Documentacao obrigatoria

Confirme links e guias:

- README com quickstart
- guia de instalacao/configuracao
- guia de operacao
- guia de treinamento
- arquitetura

## 5. Revisao de exemplos

Confirme que exemplos usam valores fake/placeholder:

- API keys de exemplo
- emails de exemplo
- URLs de exemplo quando aplicavel

## 6. Qualidade de onboarding

Checklist de leitura para novo integrante:

1. README
2. Instalacao e configuracao
3. Operacao PowerShell
4. Treinamento

## 7. Release interna

Sugestao de fluxo:

1. Criar branch de release
2. Validar docs + comandos
3. Abrir PR com checklist preenchido
4. Taggear versao (`vX.Y.Z`) apos merge

## 8. Pos-publicacao

- monitorar issues de setup
- corrigir ambiguidades do README
- manter changelog de comandos e comportamento

## 9. Regra de ouro

- Sem `publication_guard.ps1` passando, sem publicação.
