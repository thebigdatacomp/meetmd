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

> Requer **Apple Silicon**, Go 1.21+ (o toolchain 1.25 é baixado automaticamente via `GOTOOLCHAIN`) e Swift (Xcode CLT).

### 1. Pré-requisitos (uma vez)

```bash
brew install cmake          # buildar o whisper com Metal
xcode-select --install      # swiftc
```

### 2. Build e abrir

```bash
./menubar/build-app.sh      # baixa modelos + whisper, builda static Metal, empacota no MeetMD.app
open menubar/MeetMD.app      # ícone (mascote do Claude) na barra — sobe o bridge sozinho, sem terminal
```

A 1ª vez baixa os modelos (~490MB) e compila o whisper (alguns minutos); depois fica cacheado. O `.app` é **autocontido** (resolve whisper/modelos do bundle) — **não precisa de config** (ver [Configuração](#configuração-opcional)).

### 3. Permissões (na 1ª gravação) — conceda ao **MeetMD**
- **Gravação de Tela** → participantes · **Microfone** → sua voz · **Automação ▸ Safari** → detecção do Meet.

Como o app tem identidade própria, as permissões **colam** (não somem entre execuções).

### 4. Usar
- **Auto:** entre num Google Meet no **Safari** → o app pergunta *"Gravar?"* → **Gravar**.
- **Manual:** clique no ícone na barra → **Iniciar gravação**.
- **Nota de voz:** ícone na barra → **Nova nota de voz** → dite uma anotação rápida (só microfone, **sem** permissão de tela) → **Parar e salvar nota**.
- A saída vai pra `~/.meetmd/recordings/`: reuniões em `meetings/[<projeto>/]`, notas em `notes/`. Abra a pasta no Claude e peça resumo/ações. ("Abrir pasta dos arquivos" no menu abre `recordings/`.)

### Configuração (opcional)
O `.app` é **autocontido** e roda **sem nenhum config** — whisper, modelos e helper são resolvidos do próprio bundle. O `~/.meetmd/config.yaml` **não é gerado na instalação**: ele só passa a existir quando você **salva em Configurações** (ou cria à mão), e os caminhos são derivados do **seu** home em runtime (nunca hardcoded). Chaves úteis pra customizar:

| Chave | Default | Pra quê |
|-------|---------|---------|
| `recordings_root` | `~/.meetmd/recordings` | pasta base; reuniões vão em `meetings/`, notas em `notes/` |
| `language` | `auto` | idioma da transcrição (whisper) |
| `ui_language` | `auto` | idioma da UI e dos `.md` (`auto` segue o SO, ou `pt`/`en`) |

### Iniciar no login (opcional)
Ajustes do Sistema ▸ Geral ▸ **Itens de Início** → adicione `MeetMD.app`. (Substitui o antigo `meetmd install`, que rodava o binário cru e tinha as permissões negadas pelo macOS.)

### Modo dev (rebuild rápido, sem empacotar)

```bash
cd bridge && make run                                                       # bridge
cd menubar && swiftc -O MeetMDBar.swift -o meetmd-bar -framework Cocoa && ./meetmd-bar &
```

Nesse modo as permissões ficam no VS Code/terminal e o bridge não usa o bundle — aponte o whisper no config (`whisper.bin_path`) ou tenha `whisper-cli` no `PATH` (ex.: o estático em `~/.meetmd/tools/whisper.cpp/build-static/bin/`). Use o `.app` pro fluxo real.

## Status

Funcional no macOS via `.app` **autocontido** (captura sistema+mic, transcrição local Metal, diarização Você/Participantes, nota de voz mic-only, menu-bar, settings, hot-reload, whisper+modelos bundlados). Distribuível: **Developer ID + notarização + `.dmg`** de arrastar (build via `RELEASE=1 NOTARY_PROFILE=... ./menubar/build-app.sh`). Windows/Linux: capturer pendente (`#1`/`#2`).

> O build **oficial** (o `.dmg` que você baixa) é assinado e notarizado pela Apple; ao buildar do código você gera um build de dev (self-signed) ou assina com seu próprio Developer ID.

## Licença

[Apache License 2.0](LICENSE) — uso, modificação e redistribuição livres (inclusive comercial), com concessão de patente. Veja [LICENSE](LICENSE).
