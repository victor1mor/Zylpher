# Prova de foto e voz com uma conta dedicada

Use a conta dedicada e um segundo numero autorizado. Envie uma foto JPEG de
teste e um audio curto gravado pelo botao de microfone do WhatsApp. O roteiro
usa Bash no Debian 13, Go conforme o `go.mod`, Python 3, OpenSSL e `curl`.

## Iniciar um ambiente isolado

Na raiz do repositorio, use um terminal para todo o roteiro. O diretorio
temporario tem permissoes privadas e guarda as credenciais desse pareamento.

```bash
umask 077
export PROVA_DIR="$(mktemp -d)"
export ZYLPHER_DATA_DIR="$PROVA_DIR/data"
export ZYLPHER_ADDR=127.0.0.1:18080
export ZYLPHER_WEBHOOK_URL=http://127.0.0.1:19000/webhook
export ZYLPHER_API_KEY="$(openssl rand -hex 32)"
export ZYLPHER_WEBHOOK_SECRET="$(openssl rand -hex 32)"
export ZYLPHER_MEDIA_MAX_BYTES=16777216 ZYLPHER_MEDIA_TTL=15m
go build -o "$PROVA_DIR/zylpher" ./cmd/zylpher &&
  go build -o "$PROVA_DIR/zylpher-probe" ./cmd/zylpher-probe
```

O receptor local verifica o HMAC sobre os bytes originais e registra apenas
os campos de midia usados na prova, sem telefones, texto ou QR.

```bash
python3 -u - >"$PROVA_DIR/webhook.log" 2>&1 <<'PY' &
from http.server import BaseHTTPRequestHandler, HTTPServer
import hmac
import json
import os

key = os.environ['ZYLPHER_WEBHOOK_SECRET'].encode()

class Receiver(BaseHTTPRequestHandler):
    def do_POST(self):
        size = int(self.headers.get('Content-Length', '0'))
        if self.path != '/webhook' or not 0 < size <= 65536:
            self.send_error(400)
            return
        body = self.rfile.read(size)
        expected = 'sha256=' + hmac.digest(key, body, 'sha256').hex()
        if not hmac.compare_digest(expected, self.headers.get('X-Zylpher-Signature', '')):
            self.send_error(401)
            return
        event = json.loads(body)
        if event['event'] == 'message.received':
            payload = event['payload']
            media = payload.get('media')
            if media:
                media = {field: media.get(field) for field in ('id', 'kind', 'mime_type', 'size', 'sha256', 'voice')}
            print(json.dumps({'media': media, 'media_error': payload.get('media_error')}))
        self.send_response(204)
        self.end_headers()

HTTPServer(('127.0.0.1', 19000), Receiver).serve_forever()
PY
webhook_pid=$!
"$PROVA_DIR/zylpher" >"$PROVA_DIR/zylpher.log" 2>&1 &
zylpher_pid=$!
trap 'kill -INT "$zylpher_pid" 2>/dev/null; kill "$webhook_pid" 2>/dev/null; wait "$zylpher_pid" "$webhook_pid"' EXIT

BASE=http://127.0.0.1:18080
AUTH="Authorization: Bearer $ZYLPHER_API_KEY"
SESSION="$BASE/sessions/media-proof"
curl -fsS --retry 5 --retry-connrefused -H "$AUTH" "$BASE/health"
curl -fsS -H "$AUTH" -H 'Content-Type: application/json' \
  -d '{"name":"media-proof"}' "$BASE/sessions"
curl -fsS -H "$AUTH" -X POST "$SESSION/start"
"$PROVA_DIR/zylpher-probe" -qr-url "$SESSION/qr"
```

Se o QR ainda nao estiver disponivel, aguarde e repita o ultimo comando.
Escaneie em **Dispositivos conectados** na conta dedicada e confira
`WORKING` com `curl -fsS -H "$AUTH" "$SESSION"`.

## Receber e devolver os anexos

1. No segundo aparelho, envie a foto e depois a nota de voz para a conta
   dedicada. Confira `tail -n 10 "$PROVA_DIR/webhook.log"`. Os dois eventos
   devem ter `media_error` nulo, `size` positivo e `sha256`. A foto deve ter
   `kind=image` e `mime_type=image/jpeg`. A nota deve ter `kind=audio`,
   `mime_type` com `audio/ogg` e `voice=true`.
2. Para cada evento, copie o `media.id` e o SHA-256 do webhook. Rode o bloco
   abaixo uma vez com `arquivo=foto.jpg` e outra com `arquivo=voz.ogg`.

   ```bash
   read -r -p 'ID do anexo: ' id
   read -r -p 'SHA-256 do webhook: ' esperado
   read -r -p 'Arquivo (foto.jpg ou voz.ogg): ' arquivo
   rota="$SESSION/media/$id"
   curl -fsS -H "$AUTH" -D "$PROVA_DIR/headers" -o "$PROVA_DIR/$arquivo" "$rota" &&
     (cd "$PROVA_DIR" && printf '%s  %s\n' "$esperado" "$arquivo" | sha256sum -c -) &&
     awk -v hash="$esperado" '{sub(/\r$/, "")} tolower($1)=="x-zylpher-sha256:" && $2==hash {ok=1} END {exit !ok}' "$PROVA_DIR/headers" &&
     curl -fsS -H "$AUTH" -X DELETE "$rota"
   curl -sS -o /dev/null -w '%{http_code}\n' -H "$AUTH" "$rota"
   ```

   O SHA-256 do arquivo e do header deve bater com o webhook. O ultimo GET
   deve retornar `404`. Abra a foto e reproduza a voz antes de seguir.
3. Devolva os mesmos arquivos ao segundo aparelho. Digite o numero autorizado
   com codigo do pais e DDD, apenas digitos.

   ```bash
   read -r -p 'Numero autorizado: ' DESTINO
   curl -fsS -H "$AUTH" -H 'Content-Type: image/jpeg' \
     --data-binary "@$PROVA_DIR/foto.jpg" \
     "$SESSION/messages/media?to=$DESTINO&kind=image" >"$PROVA_DIR/envio-foto.json"
   curl -fsS -H "$AUTH" -H 'Content-Type: audio/ogg; codecs=opus' \
     --data-binary "@$PROVA_DIR/voz.ogg" \
     "$SESSION/messages/media?to=$DESTINO&kind=audio&voice=true" >"$PROVA_DIR/envio-voz.json"
   ```

   As duas chamadas devem retornar sucesso. No segundo aparelho, confirme
   que a imagem abre e que o audio toca como mensagem de voz.
4. Registre o commit testado e o resultado de receber foto, receber voz,
   enviar foto e enviar voz. Registre tambem a verificacao de HMAC, hash e
   exclusao. Mantenha numeros, URLs privadas, QR, chaves, anexos e bancos fora
   da evidencia. Pare a sessao com `curl -fsS -H "$AUTH" -X POST "$SESSION/stop"`,
   encerre o terminal e desvincule o dispositivo de teste no WhatsApp.
   Apague o diretorio temporario quando terminar ou mantenha-o privado, pois
   ele contem as credenciais do pareamento e as copias dos anexos.
