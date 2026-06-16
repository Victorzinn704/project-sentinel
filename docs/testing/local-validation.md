# Validação Local

Data da revisão: 2026-06-16.

## Ambiente

| Item | Resultado |
| --- | --- |
| PowerShell | disponível |
| Go no PATH | não encontrado |
| Runtime `.tools/go` | não encontrado |
| Arquivos sensíveis locais | não encontrados pelo guard |

## Comandos executados

### Publication guard

```powershell
.\tools\publication_guard.ps1
```

Resultado:

```text
PASSOU: check de publicação sem bloqueios.
```

O guard confirmou:

- nenhum arquivo proibido versionado;
- nenhum padrão de segredo real em arquivos versionados;
- nenhum `.env`, `accounts.json` ou `tools/auto-login/credentials.json` presente localmente.

### Sintaxe do script operacional

```powershell
$errors = $null
[System.Management.Automation.PSParser]::Tokenize((Get-Content -Raw tools\sentinelctl.ps1), [ref]$errors) > $null
```

Resultado:

```text
sentinelctl parse OK
```

Esse check valida se o script operacional continua parseável pelo PowerShell após alterações em exemplos, mensagens de ajuda ou comandos.

### Integridade do diff

```powershell
git diff --check
```

Resultado: sem erro de whitespace. O Git exibiu apenas avisos de normalização `LF -> CRLF` esperados no ambiente Windows.

### Busca por exemplos concretos sensíveis

```powershell
rg -n "<PADROES_SENSIVEIS_CONCRETOS>" -S .
```

Resultado:

```text
sensitive concrete examples scan OK
```

Essa busca confirma que os exemplos públicos desta revisão não mantêm IP remoto específico, account IDs operacionais, caminho local do autor ou token GitHub. Os padrões concretos usados localmente não são repetidos aqui para evitar que a própria documentação publique os exemplos que ela pretende bloquear.

### Go

```powershell
go version
```

Resultado:

```text
go: The term 'go' is not recognized
```

Também foi verificado:

```powershell
.\.tools\go\bin\go.exe version
```

Resultado:

```text
NO_LOCAL_GO
```

Conclusão: a suíte Go não foi rodada nesta máquina. O comando esperado continua documentado em `docs/testing/testing-guide.md` e deve ser executado em ambiente com Go 1.25+ ou runtime local `.tools/go`.

## Higiene documental feita nesta revisão

| Ajuste | Motivo |
| --- | --- |
| IP remoto de exemplo trocado por `<SENTINEL_HOST>` | Evitar expor host específico como padrão público. |
| Account IDs específicos trocados por `<ACCOUNT_ID>` | Evitar parecer dado operacional real. |
| Caminho local absoluto trocado por `<CAMINHO_DO_REPOSITORIO>` | Tornar o guia portável. |
| Casos de uso e matriz de testes adicionados | Ligar promessa pública a validação. |
