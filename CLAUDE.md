# MeetMD — Claude Code Context

Captura reuniões e grava a transcrição estruturada em Markdown num diretório local, pronta para o Claude processar (sumarizar, extrair ações, cruzar com outros docs). **Local-first**: o áudio nunca sai da máquina. Foco atual: **macOS**.

## Key Rules

- _ALWAYS_ rodar `make check` em `bridge/` (vet + test + build) antes de commitar mudanças no bridge — a CI cobra isso (gofmt, vet, `go test -race`, cross-compile).
- _ALWAYS_ recompilar o helper de áudio e o menu-bar (`swiftc …`) antes de commitar mudanças em `spike/macos-audio/` ou `menubar/`.
- _NEVER_ capturar áudio na extensão/menu-bar — captura é do **helper** (ScreenCaptureKit), orquestrado pelo bridge.
- _ALWAYS_ manter o transcritor atrás da interface `Transcriber` (whisper.cpp é o default, plugável).
- _ALWAYS_ usar whisper.cpp **nativo arm64 + Metal** — o bottle do Homebrew é x86/Rosetta sem Metal (~1.5x vs ~20x realtime).
- _NEVER_ persistir o `.wav` bruto além do necessário (apagar ao final por padrão).
- _NEVER_ expor secrets em código — config em `~/.meetmd/config.yaml` (hot-reload).

## Arquitetura

**Componentes:**

- **`bridge/`** — daemon Go local (`127.0.0.1:8765`). Núcleo: orquestra captura, transcreve (whisper.cpp), escreve os `.md`, expõe HTTP + CLI (`serve|start|pause|resume|stop|status|cancel|install|uninstall`). Auto-detecção do **Safari** via AppleScript. Config viva via `config.Store` (hot-reload).
- **`spike/macos-audio/`** — helper Swift (ScreenCaptureKit). Captura **áudio do sistema** (todos os participantes) + **mic** (canal separado) → WAV 16kHz mono. Controlado por sinais (SIGTERM/USR1/USR2 = stop/pause/resume).
- **`menubar/`** — app Swift de barra de menu (`NSStatusItem`). **UI principal**: popup de detecção, iniciar/pausar/parar, Configurações (lê/grava `/settings`), Sobre. Ícone = mascote do Claude por estado.
- **`extension/`** — WebExtension MV3 (Chrome). Caminho cross-browser de detecção do Meet; o bridge faz o trabalho. (No Safari, a detecção é via AppleScript no bridge.)
- **whisper.cpp** (arm64+Metal) + modelo `ggml-*.bin` + modelo VAD (`ggml-silero-*`).

**Fluxo:** detecção (extensão/AppleScript) ou comando manual → bridge inicia o helper → captura sistema+mic → no stop, whisper transcreve cada canal em paralelo (Você vs Participantes) → escreve `meeting/transcript/summary/actions.md` + `INDEX.md` por projeto.

**Stack:** Go 1.21 (CI em 1.22) + Swift (helper + menu-bar) + whisper.cpp Metal + WebExtension MV3.

**Decisões-chave** (ver `docs/specs/`):
- Captura no nível do SO → agnóstica de navegador + pega todos os participantes; mic em 2º canal → diarização "Você vs Participantes".
- Anti-alucinação do whisper: `-mc 0 -sns` + VAD (pula silêncio).
- **Permissões macOS (TCC):** Gravação de Tela + Microfone + Automação. Binário cru / LaunchAgent **não** recebe os prompts (erro -3801) — persistência real (#3) depende de empacotar como `.app` (#4). Por ora, rodar via `make run` no terminal (herda as permissões do VS Code).

## Output

Formato dos `.md` consumidos pelo Claude: ver `docs/specs/2026-06-08-output-format.md`. Saída por projeto: `output_root/<projeto>/YYYY-MM-DD-hhmm-slug/`.

## Estado

macOS funcional (captura, transcrição Metal, diarização, menu-bar, settings, hot-reload, serviço). Pendências em issues: Windows/Linux (#1/#2), empacotamento/.app (#4), diarização por pessoa (#5).
