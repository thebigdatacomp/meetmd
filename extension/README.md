# MeetMD — Extensão (Google Meet)

WebExtension (MV3) que detecta reuniões no Google Meet e dispara `start`/`stop` no [bridge local](../bridge). É um helper de detecção/UX — **não** captura áudio nem grava arquivos (isso é o bridge).

## Componentes

| Arquivo | Papel |
|---------|-------|
| `manifest.json` | MV3; content script no Meet, service worker, popup, host permission pro bridge |
| `lib/protocol.js` | Constantes compartilhadas (URL do bridge, plataforma, tipos de mensagem) |
| `content/meet.js` | Detecta call ativa no Meet + scrape best-effort de título/participantes |
| `background/service-worker.js` | Único que fala com o bridge; gerencia a sessão ativa |
| `popup/` | Status do bridge + gravação, e controle manual (start/stop/cancel) |

## Fluxo

```
content/meet.js  --(MEETING_STARTED/ENDED)-->  service-worker  --HTTP-->  bridge (127.0.0.1:8765)
popup            --(START/STOP/CANCEL/STATUS)-->
```

O content script só observa o DOM e manda mensagens; o service worker (que tem `host_permissions` pro `127.0.0.1`) faz as chamadas HTTP. A sessão fica em `chrome.storage.local` porque o SW do MV3 é efêmero.

**Coexiste com o CLI:** a extensão e o CLI (`meetmd start/stop`) são clientes do mesmo bridge, que tem uma única sessão ativa. O popup lê o `/status` do bridge como verdade e reconcilia o estado local — então iniciar pelo CLI e parar pelo popup (ou vice-versa) funciona; o badge sincroniza ao abrir o popup.

## Carregar (dev)

1. Suba o bridge: `cd ../bridge && make run`
2. Chrome → `chrome://extensions` → ative **Developer mode** → **Load unpacked** → selecione esta pasta `extension/`.
3. Entre num Google Meet: o badge fica 🔴 e a gravação inicia sozinha; ao sair, encerra e os `.md` aparecem no `output_root`.
4. O popup mostra status do bridge e permite start/stop manual (inclusive fora do Meet).

## Limitações conhecidas (MVP)

- **Scrape do Meet é frágil:** título vem do `document.title`; participantes são best-effort e podem vir vazios (o bridge grava mesmo assim). Seletores isolados em `content/meet.js`.
- Detecção por aria-label de "Leave call" (en/pt). UI nova do Meet pode exigir ajuste dos seletores.
- Só Google Meet. Zoom/Teams fora do escopo do MVP.
- Sem ícones próprios ainda (usa o default do Chrome).
