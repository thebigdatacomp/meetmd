# MeetMD — Spec de Arquitetura

- **Data:** 2026-06-08
- **Status:** Implementado (com evoluções)
- **Autor:** Robson Müller

> Esta spec registra o desenho inicial. O que foi construído evoluiu em alguns
> pontos — fonte de verdade atual: `CLAUDE.md`. Principais diferenças: a **UI
> principal é o app de menu-bar** (não a extensão); a detecção no **Safari é via
> AppleScript** no bridge; whisper.cpp roda **arm64+Metal**; há **diarização
> Você vs Participantes** (mic em 2º canal), **VAD/anti-alucinação**, **hot-reload**
> de config e **serviço LaunchAgent** (com ressalva de permissões TCC — ver #3/#4).

## 1. Objetivo

Capturar reuniões e entregar a transcrição **estruturada em Markdown** num diretório local de um projeto, pronta para o Claude processar. O usuário não deve precisar copiar/colar nada: termina a reunião, e os arquivos já estão lá no formato certo.

## 2. Requisitos

### Funcionais
- **F1.** Capturar o áudio de **todos os participantes** da reunião (não só o microfone do usuário).
- **F2.** Funcionar de forma **agnóstica de navegador** (Chrome, Firefox, Safari) — e idealmente até com apps desktop.
- **F3.** Detectar automaticamente o início/fim de uma reunião no Google Meet (MVP).
- **F4.** Transcrever localmente, sem que o áudio saia da máquina.
- **F5.** Gravar a saída como `.md` estruturado num diretório configurável, com transcript cru + `summary.md`/`actions.md` pré-prontos (templates) para o Claude preencher.
- **F6.** Manter um `INDEX.md` navegável de todas as reuniões.

### Não-funcionais
- **NF1.** Privacidade: áudio nunca sai da máquina; `.wav` temporário apagado ao final por padrão.
- **NF2.** Zero custo recorrente no MVP (sem API paga).
- **NF3.** Transcritor plugável (interface `Transcriber`) — trocar whisper.cpp por API depois sem refatorar o resto.
- **NF4.** Setup do usuário o mais simples possível, dado o custo inerente de captura de áudio no SO.

## 3. Decisões de arquitetura e tradeoffs

### 3.1. Onde capturar o áudio — **loopback no SO** (decidido)

Duas alternativas foram avaliadas:

| | Tab audio (`getDisplayMedia`) | Loopback no SO (escolhido) |
|---|---|---|
| Pega todos os participantes | ✅ (áudio da aba) | ✅ (saída do sistema) |
| Agnóstico de navegador | ❌ Chromium-only (Firefox/Safari não capturam áudio de aba) | ✅ qualquer browser + apps desktop |
| Setup do usuário | Trivial (prompt de compartilhar aba) | Médio — depende do SO |
| Núcleo da solução | No navegador | No bridge nativo |

**Escolha: loopback no SO**, pois F2 (agnóstico de verdade) e F1 são requisitos rígidos. Consequência: o núcleo vive no bridge Go, e a extensão vira um helper de detecção/metadados.

**Custo por plataforma:**
- **Windows:** WASAPI loopback nativo — simples.
- **Linux:** monitor source do PulseAudio/PipeWire — simples.
- **macOS:** mais sensível. Duas opções:
  - **ScreenCaptureKit** (macOS 13+) captura áudio do sistema com permissão, sem driver. Preferido.
  - **Driver virtual (BlackHole)** como fallback para macOS < 13.

> ⚠️ macOS é o maior risco de implementação. Validar a captura via ScreenCaptureKit cedo, antes de investir no resto.

### 3.2. Captura de mic separada (diarização mínima)

O loopback do sistema é o áudio **misturado** (todos os participantes, incluindo você pelo retorno). Para permitir pelo menos a separação **"eu vs. outros"**, o bridge captura **dois canais**:
- **Canal A:** loopback do sistema (outros participantes).
- **Canal B:** microfone do usuário (você).

Transcreve-se cada canal e mescla-se por timestamp. Diarização real por pessoa (pyannote etc.) fica como upgrade futuro — fora do MVP.

### 3.3. Transcrição — **whisper.cpp local** (decidido)

- **whisper.cpp** rodando no bridge: privado (NF1), sem custo (NF2), boa qualidade. Modelo default sugerido: `ggml-base` ou `ggml-small` (pt). Configurável.
- SpeechRecognition do browser foi **descartado**: Chrome-only (fura F2) e só faz mic bem (fura F1).
- Whisper API fica atrás da interface `Transcriber` (NF3), habilitável por flag, para quem quiser mais qualidade aceitando custo + áudio saindo da máquina.

### 3.4. Bridge local — **servidor Go** (decidido)

- Daemon Go escutando em `127.0.0.1:<porta>` (default sugerido: `8765`), só loopback local.
- Alinha com a stack TBDC (Go no Bora), fácil de empacotar como binário único multiplataforma.
- Alternativa Native Messaging Host foi considerada (sem porta aberta), mas o registro por-OS é mais chato e dificulta o uso agnóstico/sem navegador. HTTP local é mais simples e flexível.

### 3.5. Papel da extensão

A extensão (WebExtension MV3, portável Chrome/Firefox) faz **só**:
1. Detecta que o usuário entrou/saiu de um Google Meet (match de URL `meet.google.com/*`).
2. Lê do DOM o **título** da reunião e os **nomes dos participantes** (que o áudio do SO não fornece).
3. Dispara `POST /sessions/start` (ao entrar) e `POST /sessions/stop` (ao sair ou clicar em parar).

Como o núcleo é agnóstico, a extensão é **substituível**: um app de bandeja + integração de calendário daria o mesmo, 100% sem navegador. No MVP fica a extensão por ser o caminho mais rápido para os metadados do Meet.

## 4. Fluxo de ponta a ponta

```
1. Usuário entra num Meet
2. Extensão detecta → lê título + participantes do DOM
3. POST /sessions/start { title, platform, participants, startedAt }
4. Bridge cria a pasta da sessão, começa a gravar 2 canais (loopback + mic) → temp .wav
5. Usuário sai do Meet (ou clica "parar")
6. POST /sessions/{id}/stop
7. Bridge para a captura → roda whisper.cpp em cada canal → mescla por timestamp
8. Bridge escreve transcript.md + summary.md + actions.md + meeting.md
9. Bridge atualiza INDEX.md
10. Bridge apaga o .wav temporário (config)
11. Claude lê o diretório e processa
```

## 5. API HTTP do bridge (local)

| Método | Rota | Descrição |
|--------|------|-----------|
| `GET` | `/health` | Extensão checa se o bridge está rodando |
| `GET` | `/status` | Estado atual (idle / recording + sessão) |
| `POST` | `/sessions/start` | Inicia captura. Body: `{title, platform, participants[], startedAt}` → `{sessionId}` |
| `POST` | `/sessions/{id}/stop` | Finaliza, transcreve, escreve os `.md` → `{sessionDir, files[]}` |
| `POST` | `/sessions/{id}/cancel` | Aborta e descarta a captura |

Erros em JSON `{error, message}`. Sem auth no MVP (loopback local); avaliar token compartilhado depois.

## 6. Configuração

Arquivo `~/.meetmd/config.yaml`:

```yaml
output_root: /Users/robsonmuller/dev/projects/tbdc/<projeto>/meetings
port: 8765
language: pt
whisper:
  engine: local            # local | api
  model_path: ~/.meetmd/models/ggml-small.bin
audio:
  capture_mic: true        # canal B (você)
  delete_wav_on_finish: true
```

## 7. Estrutura do repo

```
meetmd/
├── bridge/                 # daemon Go (núcleo)
│   ├── cmd/meetmd/         # main.go
│   ├── internal/
│   │   ├── server/         # HTTP local + handlers
│   │   ├── audio/          # captura loopback por SO (build tags darwin/windows/linux)
│   │   ├── transcribe/     # interface Transcriber + whisper.cpp + (api)
│   │   ├── writer/         # geração dos .md + INDEX
│   │   └── config/
│   └── go.mod
├── extension/              # WebExtension MV3
│   ├── manifest.json
│   ├── content/            # detecção Meet + scrape do DOM
│   ├── background/         # chamadas ao bridge
│   └── popup/              # UI start/stop + status
└── docs/specs/
```

## 8. Fora de escopo (MVP)

- Zoom/Teams (web ou desktop) — só Google Meet no MVP.
- Diarização real por pessoa.
- Sumarização automática via LLM dentro da ferramenta (o Claude faz depois, lendo os arquivos).
- Sincronização em nuvem / multi-máquina.

## 9. Riscos e mitigações

| Risco | Mitigação |
|-------|-----------|
| Captura de áudio no macOS (maior incerteza) | Spike de ScreenCaptureKit antes do resto; BlackHole como fallback |
| Scrape do DOM do Meet quebra com mudança de UI | Isolar seletores num módulo; degradar para título/participantes vazios sem travar |
| Qualidade da transcrição em pt | Permitir trocar modelo (small/medium) via config |
| Permissões de áudio/tela no SO | Documentar o setup; checar permissão no `/health` |

## 10. Marcos sugeridos

1. **M1 — Spike de áudio:** captura loopback funcionando nos 3 SOs (foco macOS), grava `.wav`.
2. **M2 — Transcrição:** whisper.cpp integrado, `.wav` → `transcript.md`.
3. **M3 — Bridge completo:** API HTTP + writer dos `.md` + INDEX + config.
4. **M4 — Extensão:** detecção de Meet + scrape + start/stop.
5. **M5 — Polish:** apagar `.wav`, popup de status, doc de setup por SO.
