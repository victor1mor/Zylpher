<p align="center">
  <img src="assets/zylpher-mascot.png" alt="Mascote Zylpher" width="280">
</p>

# Zylpher

Motor local em Go para controlar sessoes e mensagens por uma API HTTP, pensado
para integracao com agentes de IA. A API usa a biblioteca padrao do Go e as
credenciais WhatsApp ficam em um SQLite local.

O Zylpher gerencia várias sessões, envia e recebe texto e mídia e entrega
eventos por webhooks assinados. O WhatsApp é acessado por um cliente
independente baseado no `whatsmeow`.

## Requisitos

Para instalar a partir dos binários, baixe um pacote da
[release v1.2.3](https://github.com/victor1mor/Zylpher/releases/tag/v1.2.3)
para o seu sistema e arquitetura. Os pacotes incluem `zylpher`,
`zylpher-probe` e `zylpher-webhook-receiver`.

Para compilar o motor e o receptor, instale Go 1.27.4 ou uma versão mais
recente seguindo as [instruções oficiais de instalação do Go](https://go.dev/doc/install).
O [go.mod](go.mod) indica a toolchain Go 1.27.4 e declara Go 1.26.0
como versão mínima do módulo.

## Instalar como serviço no Debian 13

Em um Debian 13 com `systemd`, use uma conta com `sudo`.

### Baixar e conferir os binários

Em uma pasta de trabalho, instale os utilitários e baixe o pacote `amd64`
ou `arm64` correspondente ao Debian. O comando só extrai o arquivo depois
de conferir o SHA-256.

```bash
sudo apt update
sudo apt install ca-certificates curl openssl

arquitetura=$(dpkg --print-architecture)
case "$arquitetura" in
  amd64|arm64) ;;
  *) printf 'Arquitetura sem pacote: %s\n' "$arquitetura" >&2; exit 1 ;;
esac

versao=v1.2.3
pacote="zylpher-$versao-linux-$arquitetura.tar.gz"
release="https://github.com/victor1mor/Zylpher/releases/download/$versao"
curl -fLO "$release/$pacote" &&
  curl -fLO "$release/SHA256SUMS" &&
  awk -v pacote="$pacote" '$2 == pacote' SHA256SUMS | sha256sum --check - &&
  tar -xzf "$pacote" &&
  cd "${pacote%.tar.gz}"
```

### Compilar no Debian 13

Para instalar a partir do código-fonte, clone a tag e compile na raiz do
repositório. Depois, siga o mesmo procedimento de configuração do serviço.

```bash
sudo apt update
sudo apt install ca-certificates curl git openssl
git clone --branch v1.2.3 --depth 1 https://github.com/victor1mor/Zylpher.git
cd Zylpher
go build -mod=readonly -o zylpher ./cmd/zylpher
go build -mod=readonly -o zylpher-probe ./cmd/zylpher-probe
```

### Configurar e iniciar o serviço

Execute os comandos a partir da pasta extraída ou do clone após a compilação.

Crie o usuário do serviço, instale os binários e gere a chave da API. O bloco
preserva o arquivo de ambiente se ele já existir. Para configurar webhooks,
edite `/etc/zylpher/zylpher.env` como administrador.

```bash
if ! id -u zylpher >/dev/null 2>&1; then
  sudo useradd --system --user-group --home-dir /var/lib/zylpher \
    --shell /usr/sbin/nologin zylpher
fi
sudo install -d -o zylpher -g zylpher -m 0700 /var/lib/zylpher
sudo install -d -o root -g zylpher -m 0750 /etc/zylpher
sudo install -o root -g root -m 0755 zylpher zylpher-probe /usr/local/bin/
if ! sudo test -e /etc/zylpher/zylpher.env; then
  chave="$(openssl rand -hex 32)" &&
    { sed '/^ZYLPHER_API_KEY=/d' deploy/zylpher.env.example
      printf 'ZYLPHER_API_KEY=%s\n' "$chave"
    } | sudo install -o root -g zylpher -m 0640 /dev/stdin /etc/zylpher/zylpher.env
  unset chave
fi
sudo install -o root -g root -m 0644 deploy/zylpher.service /etc/systemd/system/zylpher.service
```

Valide e inicie o serviço. Carregue a mesma chave no terminal para testar a
API e parear a sessão.

```bash
sudo systemd-analyze verify /etc/systemd/system/zylpher.service
sudo systemctl daemon-reload
sudo systemctl enable --now zylpher
export ZYLPHER_API_KEY="$(sudo sed -n 's/^ZYLPHER_API_KEY=//p' /etc/zylpher/zylpher.env)"
curl -fsS --retry 30 --retry-delay 1 --retry-connrefused \
  -H "Authorization: Bearer $ZYLPHER_API_KEY" http://127.0.0.1:8080/health
```

Se o serviço falhar ao iniciar, consulte `sudo systemctl status --no-pager zylpher`
e `sudo journalctl -u zylpher -n 50 --no-pager`.

### Parear uma sessão pela API

No mesmo terminal, crie a sessão `default` uma vez, inicie-a e abra o QR no
terminal local.

```bash
base=http://127.0.0.1:8080
auth="Authorization: Bearer $ZYLPHER_API_KEY"

curl -fsS -H "$auth" -H 'Content-Type: application/json' \
  -d '{"name":"default"}' "$base/sessions"
curl -fsS -H "$auth" -X POST "$base/sessions/default/start"
zylpher-probe -qr-url "$base/sessions/default/qr"
```

No WhatsApp, abra **Dispositivos conectados → Conectar dispositivo** e
escaneie o QR. Se ele ainda estiver indisponível ou tiver expirado, confira
o estado da sessão. Em `STARTING`, repita o comando do QR. Em `STOPPED` ou
`FAILED`, solicite `start` de novo. Depois do pareamento, a consulta abaixo
deve retornar `WORKING`.

```bash
curl -fsS -H "$auth" "$base/sessions/default"
```

Para confirmar que as credenciais foram salvas, deixe a sessão ativa,
reinicie o serviço e consulte o estado novamente. Espere `WORKING` sem
escanear outro QR.

```bash
sudo systemctl restart zylpher
curl -fsS --retry 30 --retry-delay 1 --retry-connrefused \
  -H "$auth" "$base/sessions/default"
```

`sudo systemctl stop zylpher` envia `SIGINT` para encerrar o processo de forma
graciosa. Para parar uma sessão pela API, use
`POST /sessions/default/stop`. O QR permite vincular a conta e deve ser
exibido apenas localmente.

## Executar no Windows

### Baixar e conferir os binários no Windows

Abra o PowerShell em uma pasta de trabalho. Baixe o ZIP da arquitetura do
Windows e confira o SHA-256 antes de extrair.

```powershell
$arquitetura = switch ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture) {
    'X64'   { 'amd64' }
    'Arm64' { 'arm64' }
    default { throw 'Esta arquitetura do Windows não tem pacote na release.' }
}

$versao = 'v1.2.3'
$pasta = "zylpher-$versao-windows-$arquitetura"
$pacote = "$pasta.zip"
$release = "https://github.com/victor1mor/Zylpher/releases/download/$versao"
Invoke-WebRequest -UseBasicParsing -Uri "$release/$pacote" -OutFile $pacote -ErrorAction Stop
Invoke-WebRequest -UseBasicParsing -Uri "$release/SHA256SUMS" -OutFile SHA256SUMS -ErrorAction Stop

$linhas = @(Get-Content -LiteralPath SHA256SUMS | Where-Object { $_.EndsWith("  $pacote") })
if ($linhas.Count -ne 1) { throw 'O SHA256SUMS não contém uma entrada única para o pacote.' }
$esperado = ($linhas[0] -split '\s+', 2)[0]
if ((Get-FileHash -LiteralPath $pacote -Algorithm SHA256).Hash -ne $esperado) {
    throw 'A verificação SHA-256 do pacote falhou.'
}

Expand-Archive -LiteralPath $pacote -DestinationPath . -ErrorAction Stop
Set-Location -LiteralPath $pasta -ErrorAction Stop
```

### Iniciar o Zylpher no Windows

Na pasta dos executáveis, gere a chave de API e inicie o servidor em uma
janela separada. Use o mesmo PowerShell para as chamadas à API.

```powershell
$bytes = New-Object byte[] 32
$rng = [Security.Cryptography.RandomNumberGenerator]::Create()
try { $rng.GetBytes($bytes) } finally { $rng.Dispose() }
$env:ZYLPHER_API_KEY = [BitConverter]::ToString($bytes).Replace('-', '').ToLowerInvariant()

Start-Process -FilePath .\zylpher.exe -WorkingDirectory (Get-Location).Path
```

Quando o servidor iniciar, crie e inicie a sessão no PowerShell. O mesmo
procedimento de leitura do QR pelo WhatsApp se aplica.

```powershell
$base = 'http://127.0.0.1:8080'
$headers = @{ Authorization = "Bearer $env:ZYLPHER_API_KEY" }
Invoke-RestMethod -Headers $headers -Uri "$base/health"
Invoke-RestMethod -Method Post -Headers $headers -ContentType 'application/json' -Body '{"name":"default"}' -Uri "$base/sessions"
Invoke-RestMethod -Method Post -Headers $headers -Uri "$base/sessions/default/start"
.\zylpher-probe.exe -qr-url "$base/sessions/default/qr"
```

### Compilar no Windows

Para instalar a partir do código-fonte, clone a tag e compile os dois
executáveis na raiz do repositório. Em seguida, siga as instruções em
[Iniciar o Zylpher no Windows](#iniciar-o-zylpher-no-windows).

```powershell
git clone --branch v1.2.3 --depth 1 https://github.com/victor1mor/Zylpher.git
Set-Location -LiteralPath Zylpher
go build -mod=readonly -o zylpher.exe ./cmd/zylpher
go build -mod=readonly -o zylpher-probe.exe ./cmd/zylpher-probe
```

## Variaveis de ambiente

| Variavel | Padrao | Uso |
| --- | --- | --- |
| `ZYLPHER_ADDR` | `127.0.0.1:8080` | Endereco do servidor HTTP. |
| `ZYLPHER_API_KEY` | obrigatoria | Chave enviada como `Authorization: Bearer`. |
| `ZYLPHER_DATA_DIR` | `data` | Pasta dos dados de sessão, mídia e fila de webhooks. |
| `ZYLPHER_LOG_LEVEL` | `info` | `debug`, `info`, `warn` ou `error`. |
| `ZYLPHER_WEBHOOK_URL` | vazio | URL opcional para os webhooks. |
| `ZYLPHER_WEBHOOK_SECRET` | condicional | Obrigatoria quando o webhook estiver ativo. |
| `ZYLPHER_MEDIA_MAX_BYTES` | `16777216` | Maior anexo aceito, nos dois sentidos (1 a 104857600). |
| `ZYLPHER_MEDIA_TTL` | `24h` | Idade a partir da qual a limpeza remove anexos recebidos (minimo `1m`). |

## API

O contrato [OpenAPI 3.1](openapi.json) descreve as rotas HTTP, os corpos e
códigos de resposta e os eventos enviados ao receptor de webhooks. Importe
o arquivo na ferramenta de sua preferência e use o endereço da sua instância.

| Metodo | Rota | Descricao |
| --- | --- | --- |
| `GET` | `/health` | Verifica se o servidor esta ativo. |
| `GET` | `/metrics` | Expoe as metricas da fila de webhooks no formato Prometheus. |
| `GET` | `/sessions` | Lista as sessoes. |
| `GET` | `/sessions/{name}` | Consulta uma sessao e atualiza seu estado real. |
| `GET` | `/sessions/{name}/qr` | Retorna o QR textual atual durante o pareamento. |
| `POST` | `/sessions` | Cria uma sessao. |
| `POST` | `/sessions/{name}/start` | Inicia uma sessao. |
| `POST` | `/sessions/{name}/stop` | Para uma sessao. |
| `POST` | `/sessions/{name}/messages/text` | Envia texto pela sessao `WORKING`. |
| `POST` | `/sessions/{name}/messages/media` | Envia foto, audio (inclusive mensagem de voz), video ou documento. |
| `GET` | `/sessions/{name}/media/{id}` | Baixa um anexo recebido. |
| `DELETE` | `/sessions/{name}/media/{id}` | Apaga um anexo recebido depois de buscado. |

Com uma sessão em `WORKING`, envie um texto para um número autorizado.
Use o número com DDI e DDD, com ou sem o prefixo `+`. Para responder a um
contato individual, use o `payload.from` do evento `message.received`
diretamente em `to`, na sessão indicada pelo evento. Os JIDs de contato
`@s.whatsapp.net` e `@lid` são aceitos. Se o JID inclui um identificador
de dispositivo, o envio usa o JID do contato.

```bash
curl -fsS http://127.0.0.1:8080/sessions/default/messages/text \
  -H "Authorization: Bearer $ZYLPHER_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"to":"NUMERO_AUTORIZADO","text":"Teste do Zylpher"}'
```

Todas as rotas, inclusive `/health`, exigem o header Bearer. Bodies JSON tem
limite de 64 KiB e nomes de sessao aceitam ate 64 caracteres alfanumericos,
`_` ou `-`.

### Cliente Go gerado

O pacote [`github.com/victor1mor/Zylpher/client`](client) oferece as 12
operações HTTP do `openapi.json` e usa apenas a biblioteca padrão.

```go
c, err := client.New("http://127.0.0.1:8080", os.Getenv("ZYLPHER_API_KEY"), nil)
if err != nil {
    return err
}
health, err := c.GetHealth(context.Background())
if err != nil {
    return err
}
fmt.Println(health.Status)
```

`SendMedia` recebe um `io.Reader` e o Content-Type do arquivo. Leia e feche
o `response.Body` retornado por `DownloadMedia`. Use o header
`X-Zylpher-SHA256` para verificar os bytes. As respostas de erro HTTP são
`*client.APIError`, com status, código e mensagem quando enviados pela API.

Após editar `openapi.json`, rode `go generate ./client` e `go test ./...`.
Os testes verificam se o cliente gerado corresponde ao contrato e executam as
operações contra o roteador do Zylpher por HTTP, com um motor WhatsApp de teste.

#### Instalar o cliente Go

O caminho de importação do cliente é `github.com/victor1mor/Zylpher/client`.
Para testar o código de um clone local, execute no módulo consumidor e
ajuste `../Zylpher` para o caminho do clone.

```bash
go mod edit -replace=github.com/victor1mor/Zylpher=../Zylpher
go mod tidy
```

Para instalar a v1.2.3 pública, remova a diretiva local `replace` caso
tenha configurado uma e execute o comando abaixo.

```bash
go get github.com/victor1mor/Zylpher/client@v1.2.3
```

#### Prova contra o serviço no Debian 13

Use uma instalação de teste com diretório de dados separado e sem sessões.
Exporte `ZYLPHER_SERVICE_TEST_URL` com o endereço HTTP da instância.
Exporte também `ZYLPHER_API_KEY` e `ZYLPHER_DATA_DIR` com os valores do
serviço. Rode o teste com um usuário que possa gravar no diretório de dados.

```bash
go test -count=1 -v ./internal/api -run '^TestGeneratedClientAgainstService$/^requests$'
```

A prova cria a sessão `sdk-proof` em `STOPPED`, testa as respostas de erro
de pareamento e envio e grava um anexo sintético para baixar, conferir e
excluir. Reinicie a unidade de teste com systemd. Quando a API voltar a
responder, confira a sessão persistida e a exclusão do anexo.

```bash
go test -count=1 -v ./internal/api -run '^TestGeneratedClientAgainstService$/^after_restart$'
```

## Midia

Desde a `v1.1.0`, o Zylpher recebe e envia foto, audio, video e documento.

**Receber.** Com `ZYLPHER_WEBHOOK_URL` configurada, quando chega um anexo o Zylpher baixa e guarda em
`<ZYLPHER_DATA_DIR>/media/<sessao>/` (pasta `0700`, arquivos `0600`) e manda o
webhook `message.received` com `text` igual a legenda e o campo `media`:

```json
{
  "id": "3EB0...",
  "kind": "audio",
  "mime_type": "audio/ogg; codecs=opus",
  "size": 18234,
  "sha256": "9f2c...",
  "voice": true
}
```

`kind` e `image`, `audio`, `video` ou `document` (com `file_name`). O consumidor
busca os bytes com `GET /sessions/{name}/media/{id}`, confere o
`X-Zylpher-SHA256` e apaga com `DELETE`. Anexos que ainda estiverem no disco
depois de `ZYLPHER_MEDIA_TTL` sao removidos na proxima limpeza, feita ao iniciar
e a cada hora. O `GET` preserva o arquivo. Se o anexo nao puder ser guardado, o
evento segue sem `media` e com `media_error`: `too_large`, `download_failed` ou
`no_store`. Ha no maximo dois recebimentos simultaneos no processo. Se ambos
estiverem ocupados, o evento segue com `download_failed`.

Com o webhook desativado, o Zylpher deixa de baixar novos anexos. O envio de
midia pela API continua disponivel.

**Enviar.** O corpo e o arquivo cru; o resto vai na URL. O parâmetro `to`
aceita os mesmos destinos do envio de texto. Codifique o valor para uso na
query string ao montar a URL a partir do webhook.

No exemplo, defina `DESTINO` com o número autorizado, incluindo o DDI e
usando apenas dígitos.

```bash
curl -X POST "http://localhost:8080/sessions/default/messages/media?to=$DESTINO&kind=audio&voice=true" \
  -H "Authorization: Bearer $ZYLPHER_API_KEY" \
  -H "Content-Type: audio/ogg; codecs=opus" \
  --data-binary @resposta.ogg
```

| `kind` | `Content-Type` aceito | Parametros opcionais |
| --- | --- | --- |
| `image` | `image/jpeg`, `image/png`, `image/webp` | `caption` |
| `audio` | `audio/ogg`, `audio/mpeg`, `audio/mp4`, `audio/aac`, `audio/amr` | `voice=true` (so `audio/ogg`, Opus) |
| `video` | `video/mp4`, `video/3gpp` | `caption` |
| `document` | qualquer | `file_name`, `caption` |

Acima de `ZYLPHER_MEDIA_MAX_BYTES` a resposta e `413`. A legenda vai ate 1024
caracteres; audio nao tem legenda.

## Webhooks

Quando `ZYLPHER_WEBHOOK_URL` estiver configurada, o Zylpher envia `POST` com
`Content-Type: application/json`. O envelope usa timestamp Unix em
milissegundos:

```json
{
  "id": "message-1",
  "event": "message.received",
  "session": "default",
  "timestamp": 1783728000000,
  "payload": {}
}
```

Eventos atuais:

| Evento | Payload |
| --- | --- |
| `message.sent` | Resultado do envio: ID, sessao, destino, status e timestamp. |
| `message.received` | ID, sessao, remetente, destinatario, texto e timestamp. |
| `session.status` | Sessao completa depois de cada mudanca de estado persistida. |

Mensagens sem texto e sem anexo suportado são ignoradas.
O JSON do evento entra em `<ZYLPHER_DATA_DIR>/webhooks.db` antes da primeira
tentativa. Um processo entrega um evento por vez, com timeout de cinco
segundos por tentativa. Um `2xx` confirma a entrega e remove o evento da
fila. Falhas mantêm o evento no SQLite, com uma espera que começa em um
segundo e dobra até o máximo de cinco minutos. O processo recupera os
eventos pendentes e seus prazos após reiniciar.

Use a URL final do receptor. Respostas `3xx` entram em retentativa.
Pendências só são enviadas para a mesma URL configurada quando o evento
entrou na fila. Se o webhook for desativado, elas permanecem no banco e
retomam quando o mesmo endereço voltar a ser configurado.

A entrega é **ao menos uma vez** após a gravação no SQLite. Uma queda do
processo depois do `2xx` e antes da remoção da fila pode repetir o evento.
O consumidor pode deduplicar pelo SHA-256 dos bytes originais do JSON,
preservados em cada retentativa. Isso também vale para `session.status`,
cujo campo `id` é o nome da sessão e se repete a cada mudança.

As falhas são registradas sem o payload. Se a gravação da fila falhar
depois que o WhatsApp aceitou um envio, o endpoint retorna o resultado do
envio e a falha de gravação aparece no log.

### Métricas da fila

Consulte `GET /metrics` com a mesma chave da API.

```bash
curl -fsS http://127.0.0.1:8080/metrics \
  -H "Authorization: Bearer $ZYLPHER_API_KEY"
```

| Métrica | O que mede |
| --- | --- |
| `zylpher_webhook_pending` | Eventos ainda no SQLite para a URL atual, inclusive durante uma tentativa de entrega. |
| `zylpher_webhook_delivery_failures_total` | Tentativas com erro de transporte, timeout ou resposta fora de `2xx`. |
| `zylpher_webhook_retries_total` | Novas tentativas iniciadas após uma falha registrada na fila. |
| `zylpher_webhook_oldest_pending_age_seconds` | Segundos desde que o evento pendente mais antigo entrou na fila. Vale zero com a fila vazia. |

Pendências e idade são lidas do SQLite e recuperadas após reiniciar. Para
eventos gravados antes desta atualização, a idade usa o timestamp do evento
quando disponível. Os contadores de falhas e retentativas começam em zero
a cada início do processo. Com o webhook desativado, as quatro métricas
retornam zero.

Cada corpo é assinado com HMAC-SHA256 usando `ZYLPHER_WEBHOOK_SECRET`. O header
`X-Zylpher-Signature` tem o formato `sha256=<hex>` e deve ser comparado em
tempo constante contra o HMAC calculado sobre os bytes exatos recebidos.

### Receptor local em systemd

`cmd/zylpher-webhook-receiver` escuta em `127.0.0.1:18441`. Ele verifica a
assinatura e grava o JSON original em SQLite antes de confirmar a entrega.
O SHA-256 do corpo evita duplicatas em retentativas. Cada corpo pode ter até
1 MiB, e o banco retém os eventos até que o operador faça a limpeza.

Use o `zylpher-webhook-receiver` do pacote para Linux. Em um clone do
código-fonte, compile com o comando abaixo.

```bash
go build -mod=readonly -o zylpher-webhook-receiver ./cmd/zylpher-webhook-receiver
```

Instale o binário em `/usr/local/bin` e a unidade
`deploy/zylpher-webhook-receiver.service` em `/etc/systemd/system`. Em
`/etc/zylpher/webhook-receiver.env`, defina `ZYLPHER_WEBHOOK_SECRET` com o
mesmo valor usado pelo Zylpher e restrinja o arquivo a `root` com modo `0600`.

Ative a unidade com `systemctl daemon-reload` e
`systemctl enable --now zylpher-webhook-receiver.service`. A rota `/ready`
retorna HTTP 204, e o banco fica em
`/var/lib/zylpher-webhook-receiver/events.db`. Configure
`ZYLPHER_WEBHOOK_URL=http://127.0.0.1:18441/webhook` no Zylpher e reinicie
`zylpher.service`.

## Seguranca

A API usa chave Bearer e escuta apenas no loopback por padrao. Nao coloque TLS
dentro do Zylpher: para acesso remoto, mantenha o processo no loopback e use um
proxy reverso com TLS, autenticacao e rate limit. Enquanto a API permanecer
local, um rate limiter no processo apenas adicionaria estado sem reduzir o
risco real.

No Debian, o diretorio de dados e restringido a `0700` e os bancos SQLite a
`0600`. No Windows, `os.Chmod` nao substitui ACLs NTFS; mantenha os dados em um
perfil privado e configure ACLs se outros usuarios tiverem acesso ao host.

### Backup e restauracao

Os bancos SQLite dão acesso às contas pareadas. Instale o GnuPG com
`sudo apt install gnupg` no Debian. Gere o backup criptografado em um
terminal e guarde a senha separadamente.
O serviço é reiniciado ao sair do bloco.

```bash
(
  set -e -o pipefail
  umask 077
  export GPG_TTY="$(tty)"
  sudo systemctl stop zylpher
  trap 'sudo systemctl start zylpher' EXIT
  sudo tar -C /var/lib -czf - zylpher |
    gpg --symmetric --cipher-algo AES256 --output "zylpher-$(date +%Y%m%dT%H%M%S).tgz.gpg"
)
```

Para restaurar, descriptografe e extraia o backup em um diretório temporário
privado. Confirme que a descriptografia e a extração terminaram com sucesso.
Pare o serviço, preserve uma cópia dos dados atuais e restaure o diretório em
`/var/lib/zylpher`. Ajuste o proprietário para `zylpher:zylpher` e as
permissões do diretório para `0700` antes de iniciar o serviço.

## Limites conhecidos

- O cliente WhatsApp nao e oficial; mudancas do protocolo podem interromper o
  funcionamento ou causar restricoes na conta.
- Texto e midia (foto, audio, video, documento) desde a `v1.1.0`. Nao
  administra figurinhas, chamadas, stories ou grupos.
- Os anexos ficam em claro no disco até a exclusão pela API ou pela limpeza
  periódica. Proteja a pasta de dados como as credenciais.
- Uma indisponibilidade prolongada do receptor pode aumentar a fila de
  webhooks. O banco guarda conteúdo de mensagens em disco, sob as mesmas
  permissões dos dados da sessão. Anexos referenciados por eventos atrasados
  podem expirar conforme o TTL de mídia.
- Credenciais SQLite equivalem ao acesso da conta e nao devem entrar em Git,
  logs ou backups sem criptografia.

## Validar

```bash
go test ./...
go build ./cmd/...
```

Para repetir a prova real de foto e mensagem de voz nos dois sentidos, siga
o [roteiro com uma conta dedicada](docs/prova-midia.md).

## Publicar uma versão

O workflow **Release** publica versões estáveis quando uma tag nova no
formato `vX.Y.Z` chega ao GitHub. Depois do CI em Debian, Windows e macOS,
publica os pacotes para Linux, Windows e macOS (`amd64` e `arm64`),
o `SHA256SUMS` e o código-fonte correspondente com as dependências.

Para validar o processo antes de criar uma tag, execute **Release**
manualmente no Actions. A prova gera os mesmos pacotes com o nome
`v0.0.0` e disponibiliza o conjunto como artefato `release-assets`.

## Licenca

O codigo do Zylpher usa [Apache-2.0](LICENSE). Dependencias mantem suas
licencas proprias, inclusive o `whatsmeow` sob MPL-2.0.
