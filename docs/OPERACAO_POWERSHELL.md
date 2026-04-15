# Operação No PowerShell

Guia rápido para abrir, rodar, monitorar e controlar o Project Sentinel no Windows.

## 1. Abrir O Projeto

Abra o PowerShell e entre na pasta:

```powershell
cd C:\Users\Desktop\Documents\project-sentinel
```

Se quiser abrir a pasta no Explorer:

```powershell
explorer .
```

Se tiver VS Code instalado:

```powershell
code .
```

Para abrir a documentação no Bloco de Notas:

```powershell
notepad README.md
notepad docs\OPERACAO_POWERSHELL.md
```

## 2. Liberar Scripts Nesta Sessão

Se o Windows bloquear `.ps1`, rode:

```powershell
Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass
```

Isso vale só para a janela atual do PowerShell.

## 3. Subir O Sentinel

```powershell
.\tools\sentinelctl.ps1 start
```

Ver status:

```powershell
.\tools\sentinelctl.ps1 status
```

Reiniciar:

```powershell
.\tools\sentinelctl.ps1 restart
```

Parar:

```powershell
.\tools\sentinelctl.ps1 stop
```

## 4. Testar Se Está Funcionando

Teste padrão com `gpt-5.4`:

```powershell
.\tools\sentinelctl.ps1 test -Effort high
```

Teste em modo altíssimo:

```powershell
.\tools\sentinelctl.ps1 chat -Model gpt-5.4 -Effort xhigh -Prompt "Responda apenas: ok"
```

Se retornar `ok`, o caminho real funcionou:

```txt
PowerShell -> Sentinel -> conta ChatGPT -> upstream ChatGPT/Codex -> resposta OpenAI-compatible
```

Para reduzir latência, prefira `-Effort high`. O modo `xhigh` é mais forte, mas naturalmente demora mais para começar a devolver texto.

Se quiser reduzir custo médio sem sacrificar tarefas realmente difíceis, use `-Effort auto`. Nesse modo, o Sentinel mantém o piso em `high` e sobe para `xhigh` só quando o contexto parece pesado.

## 5. Monitorar Consumo

Resumo geral:

```powershell
.\tools\sentinelctl.ps1 status
```

Forçar refresh manual da quota:

```powershell
.\tools\sentinelctl.ps1 quota-refresh
```

Tabela de contas:

```powershell
.\tools\sentinelctl.ps1 accounts
```

Consumo em barra (snapshot):

```powershell
.\tools\sentinelctl.ps1 consumo
```

Consumo em barra (ao vivo):

```powershell
.\tools\sentinelctl.ps1 consumo-watch 5
```

Painel contínuo no terminal:

```powershell
.\tools\sentinelctl.ps1 watch
```

Painel com intervalo de 3 segundos:

```powershell
.\tools\sentinelctl.ps1 watch 3
```

Campos mais importantes:

```txt
status             active, cooldown, disabled, attention_required
daily_usage_count  quantas requests deram sucesso hoje
daily_limit        limite local configurado
error_count        falhas recentes registradas
latency_ewma_ms    latência média suavizada
active_leases      requests em andamento naquela conta
```

Leitura da barra de consumo:

```txt
src=chatgpt_wham_usage  consumo real da conta no upstream (5h/7d)
src=daily_usage         fallback local por requests processadas no Sentinel
```

Logs ao vivo:

```powershell
.\tools\sentinelctl.ps1 logs -Watch
```

## 6. Trocar Modelo

Roteador padrão para `gpt-5.4`:

```powershell
.\tools\sentinelctl.ps1 use-model gpt-5.4 -Effort high
.\tools\sentinelctl.ps1 restart
```

Roteador padrão para `gpt-5.4` com esforço altíssimo:

```powershell
.\tools\sentinelctl.ps1 use-model gpt-5.4 -Effort xhigh
.\tools\sentinelctl.ps1 restart
```

Roteador padrão para `gpt-5.4` com esforço adaptativo:

```powershell
.\tools\sentinelctl.ps1 use-model gpt-5.4 -Effort auto
.\tools\sentinelctl.ps1 restart
```

Listar modelos carregados:

```powershell
.\tools\sentinelctl.ps1 models
```

## 7. Gerenciar API Key

Ver key atual mascarada:

```powershell
.\tools\sentinelctl.ps1 key-show
```

Criar key nova e revogar a antiga:

```powershell
.\tools\sentinelctl.ps1 key-new
```

Revogar por rotação:

```powershell
.\tools\sentinelctl.ps1 key-revoke
```

Ver admin key mascarada:

```powershell
.\tools\sentinelctl.ps1 admin-key-show
```

Rotacionar admin key:

```powershell
.\tools\sentinelctl.ps1 admin-key-new
```

Rotacionar a chave de sessão com recriptografia das sessões atuais:

```powershell
.\tools\sentinelctl.ps1 session-key-rotate
```

Rotacionar runtime key, admin key e session key em uma passada:

```powershell
.\tools\sentinelctl.ps1 secrets-rotate
```

Gerar sem reiniciar:

```powershell
.\tools\sentinelctl.ps1 key-new -NoRestart
.\tools\sentinelctl.ps1 restart
```

## 8. Controlar Conta

Forçar conta específica:

```powershell
.\tools\sentinelctl.ps1 force acc_contato_deskimperial_online
```

Limpar force mode:

```powershell
.\tools\sentinelctl.ps1 unforce
```

Desabilitar:

```powershell
.\tools\sentinelctl.ps1 disable acc_suporte_deskimperial_online
```

Habilitar:

```powershell
.\tools\sentinelctl.ps1 enable acc_suporte_deskimperial_online
```

## 9. Usar Em IDE Ou App

Configure:

```txt
Base URL: http://127.0.0.1:8080/v1
API Key: valor de SENTINEL_API_KEY no .env
Model: sentinel-router
```

Se quiser chamar direto:

```txt
Model: gpt-5.4
```

Importante:

```txt
Mesmo PC: http://127.0.0.1:8080/v1 funciona.
Outro PC/servidor/cloud: 127.0.0.1 aponta para a máquina do cliente, não para seu Sentinel.
```

Para outro dispositivo enxergar, rode o Sentinel em um servidor acessível ou use um túnel seguro com HTTPS.

Se o cliente não enviar `model`, o Sentinel usa `DEFAULT_MODEL=sentinel-router`. Isso evita erro `missing_model` em clientes que separam o model em outra configuração ou deixam o campo vazio.

## 10. Usar No Codex CLI

Instalar o provider local do Sentinel no Codex:

```powershell
.\tools\sentinelctl.ps1 codex-install
```

O comando escreve um bloco gerenciado em:

```txt
.\.codex\config.toml
```

Configuração aplicada:

```toml
model = "sentinel-router"
model_provider = "sentinel"
model_reasoning_effort = "medium"

[model_providers.sentinel]
name = "Project Sentinel"
base_url = "http://127.0.0.1:8080/v1"
wire_api = "responses"
env_key = "CODEX_API_KEY"
```

Valores aceitos pelo Codex para `model_reasoning_effort`: `minimal`, `low`, `medium`, `high`, `xhigh`.

Sem `-Model`, o comando usa `DEFAULT_MODEL` do `.env`. Para effort no Codex, se `DEFAULT_REASONING_EFFORT=auto`, o `sentinelctl` converte para `medium` para manter o `config.toml` válido.

Segurança operacional:

```txt
O config.toml não recebe a chave real.
O Codex lê a key pela variável CODEX_API_KEY.
Headers locais do Sentinel/Codex são bloqueados antes do upstream ChatGPT.
```

Para gravar `CODEX_API_KEY` no ambiente de usuário do Windows:

```powershell
.\tools\sentinelctl.ps1 codex-install -Persist
```

Sem `-Persist`, a variável vale só para a sessão atual do PowerShell.

Se você quiser alterar o Codex global da máquina em vez do projeto atual:

```powershell
.\tools\sentinelctl.ps1 codex-install -GlobalConfig
```

Se quiser apontar o Codex global para um Sentinel remoto:

```powershell
.\tools\sentinelctl.ps1 codex-install -GlobalConfig -BaseURL https://app.deskimperial.online/suporte/v1
```

Como o config padrão agora é local ao projeto, rode o `codex` a partir da pasta do projeto para ele usar `.\.codex\config.toml`.

## 11. Diagnóstico Rápido

Servidor não responde:

```powershell
.\tools\sentinelctl.ps1 restart
.\tools\sentinelctl.ps1 logs
```

Conta caiu em `attention_required`:

```powershell
.\tools\sentinelctl.ps1 accounts
```

Se for `403`, a sessão provavelmente precisa login/refresh.

Key antiga ainda funciona:

```powershell
.\tools\sentinelctl.ps1 restart
```

Modelo não apareceu:

```powershell
.\tools\sentinelctl.ps1 restart
.\tools\sentinelctl.ps1 models
```

## 12. Pré-publicação segura

Antes de `git push` ou PR:

```powershell
.\tools\publication_guard.ps1
```

Se o resultado for `FALHOU`, não publique até corrigir os itens `[BLOCK]`.
