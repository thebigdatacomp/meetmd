# MeetMD

[![CI](https://github.com/thebigdatacomp/meetmd/actions/workflows/ci.yml/badge.svg)](https://github.com/thebigdatacomp/meetmd/actions/workflows/ci.yml)

Captura reuniões e entrega a transcrição **estruturada em Markdown**, pronta para o Claude (ou qualquer LLM) processar — sumarizar, extrair ações, cruzar com outros documentos.

## Como funciona

```
┌──────────────┐     start/stop + metadados      ┌──────────────────────┐
│  Extensão    │ ──────────────────────────────> │   Bridge (Go, local)  │
│  (Meet)      │   POST /sessions  (HTTP local)  │                       │
│  detecta a   │                                 │  • loopback de áudio   │
│  reunião,    │                                 │    do SO (todos os     │
│  lê título e │                                 │    participantes)      │
│  participan- │                                 │  • whisper.cpp (local) │
│  tes do DOM  │                                 │  • escreve .md         │
└──────────────┘                                 └──────────┬───────────┘
                                                            │
                                                            v
                                          <output-root>/YYYY-MM-DD-hhmm-titulo/
                                            ├── transcript.md
                                            ├── summary.md   (template)
                                            ├── actions.md   (template)
                                            └── meeting.md   (metadados)
```

A extensão **não** captura áudio nem escreve arquivos (o sandbox do navegador não permite e seria preso a um browser). Quem faz o trabalho pesado é o **bridge local em Go**: captura o áudio do sistema (todos os participantes, agnóstico de navegador e até de apps desktop), transcreve com Whisper local e grava a estrutura de Markdown num diretório do seu projeto.

## Por que essa arquitetura

Dois requisitos definiram o desenho:

1. **Áudio de todos os participantes** → captura no nível do SO (loopback), não só o mic.
2. **Agnóstico de navegador** → o núcleo vive fora do navegador (bridge nativo), funcionando em qualquer browser e até no app desktop do Zoom/Teams.

Detalhes e tradeoffs em [docs/specs/2026-06-08-architecture.md](docs/specs/2026-06-08-architecture.md).

## Componentes

| Componente | Stack | Papel |
|------------|-------|-------|
| `bridge/` | Go 1.25 | Captura de áudio (loopback SO), transcrição (whisper.cpp), escrita dos `.md`, HTTP local |
| `extension/` | WebExtension (MV3) | Detecta Meet, lê título/participantes do DOM, dispara start/stop no bridge |

## Para testers (macOS)

> Setup manual por enquanto — o [#4](https://github.com/thebigdatacomp/meetmd/issues/4) vai empacotar tudo num `.app`. Requer **Apple Silicon**, Go 1.21+ e Swift (Xcode CLT).

### 1. Pré-requisitos (uma vez)

```bash
# dependências
brew install cmake                 # p/ buildar o whisper com Metal
xcode-select --install             # swiftc (se ainda não tiver)

# modelos (transcrição + VAD)
mkdir -p ~/.meetmd/models
curl -L -o ~/.meetmd/models/ggml-small.bin        https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-small.bin
curl -L -o ~/.meetmd/models/ggml-silero-v5.1.2.bin https://huggingface.co/ggml-org/whisper-vad/resolve/main/ggml-silero-v5.1.2.bin

# whisper.cpp NATIVO arm64 + Metal (o bottle do brew é x86/Rosetta, ~10x mais lento)
git clone --depth 1 https://github.com/ggerganov/whisper.cpp ~/.meetmd/tools/whisper.cpp
cmake -S ~/.meetmd/tools/whisper.cpp -B ~/.meetmd/tools/whisper.cpp/build \
  -DCMAKE_BUILD_TYPE=Release -DCMAKE_OSX_ARCHITECTURES=arm64 \
  -DCMAKE_SYSTEM_PROCESSOR=arm64 -DGGML_NATIVE=OFF -DGGML_METAL=ON -DWHISPER_BUILD_TESTS=OFF
cmake --build ~/.meetmd/tools/whisper.cpp/build -j --target whisper-cli
```

Helper de áudio + config (**rode da raiz do repo** — usa `$PWD`):

```bash
( cd spike/macos-audio && swiftc -O SystemAudioRecorder.swift -o system-audio-recorder )

cat > ~/.meetmd/config.yaml <<EOF
output_root: $HOME/.meetmd/meetings
whisper:
  bin_path: $HOME/.meetmd/tools/whisper.cpp/build/bin/whisper-cli
  model_path: $HOME/.meetmd/models/ggml-small.bin
  vad_model: $HOME/.meetmd/models/ggml-silero-v5.1.2.bin
audio:
  mac_helper_path: $PWD/spike/macos-audio/system-audio-recorder
EOF
```

### 2. Rodar (toda vez)

```bash
# Terminal 1 — bridge
cd bridge && make run

# Terminal 2 — app de menu-bar
cd menubar && swiftc -O MeetMDBar.swift -o meetmd-bar -framework Cocoa && ./meetmd-bar &
```

### 3. Permissões (na 1ª gravação)
O macOS vai pedir — **conceda os três**:
- **Gravação de Tela** → captura os participantes
- **Microfone** → captura a sua voz ("Você")
- **Automação ▸ Safari** → detecção automática do Meet

### 4. Usar
- **Auto:** entre num Google Meet no **Safari** → o app pergunta *"Gravar?"* → **Gravar**.
- **Manual:** clique no ícone (mascote do Claude) na barra → **Iniciar gravação**.
- Os `.md` aparecem em `~/.meetmd/meetings/[<projeto>/]`. Abra a pasta no Claude e peça resumo/ações.

> ⚠️ `meetmd install` (serviço de login) **ainda não funciona pra captura** — o macOS nega Gravação de Tela/Microfone a agentes de background ([#3](https://github.com/thebigdatacomp/meetmd/issues/3), depende do `.app` em #4). Por ora, rode pelo terminal.

## Status

Funcional no macOS (captura sistema+mic, transcrição local Metal, diarização Você/Participantes, menu-bar, settings, hot-reload). Windows e Linux: capturer de áudio pendente (`#1`/`#2`).
