<div align="center">

<a href="https://github.com/thebigdatacomp/meetmd/actions/workflows/ci.yml"><img src="https://github.com/thebigdatacomp/meetmd/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
<img src="https://img.shields.io/badge/license-Apache%202.0-blue" alt="Apache 2.0">
<img src="https://img.shields.io/badge/macOS-Apple%20Silicon-black?logo=apple&logoColor=white" alt="macOS · Apple Silicon">
<img src="https://img.shields.io/badge/transcri%C3%A7%C3%A3o-100%25%20local-success" alt="100% local">

<br><br>

<img src="assets/meetmd-icon.png" alt="MeetMD" width="120">

<h1>MeetMD</h1>

<p><strong>Suas reuniões viram Markdown — local, privado, pronto pro Claude.</strong><br>
Captura o áudio da reunião, transcreve <strong>na sua máquina</strong> (o áudio nunca sai) e grava a transcrição estruturada num diretório que o Claude lê.</p>

<a href="https://github.com/thebigdatacomp/meetmd/releases/latest"><img src="https://img.shields.io/badge/⬇%20Baixar%20para%20Mac-MeetMD.dmg-orange?style=for-the-badge&logo=apple" alt="Baixar para Mac"></a>

</div>

---

## Por que

A transcrição roda **localmente** com whisper.cpp (Metal) — nada de mandar áudio de reunião pra nuvem de terceiros. A saída é **Markdown** num diretório do seu projeto, então o Claude (ou qualquer LLM) resume, extrai ações e cruza com seus outros docs.

- 🔒 **100% local** — o áudio nunca sai da sua máquina
- 🎙️ **Todos os participantes** — captura no nível do SO (loopback), agnóstico de navegador (e até de apps desktop)
- 🗣️ **Diarização** Você vs. Participantes (seu mic num canal separado)
- 📝 **Nota de voz** rápida — só microfone, sem permissão de tela
- 🧠 **Pronto pro Claude** — abre a pasta e pede resumo/ações

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
                                          ~/.meetmd/recordings/meetings/<reunião>/
                                            ├── transcript.md
                                            ├── summary.md   (template)
                                            ├── actions.md   (template)
                                            └── meeting.md   (metadados)
```

A extensão **não** captura áudio nem escreve arquivos (o sandbox do navegador não permite e prenderia a um browser). Quem faz o trabalho pesado é o **bridge local em Go**: captura o áudio do sistema (todos os participantes, agnóstico de navegador), transcreve com Whisper local e grava a estrutura de Markdown. Detalhes e tradeoffs em [docs/specs/2026-06-08-architecture.md](docs/specs/2026-06-08-architecture.md).

## Instalação (macOS · Apple Silicon)

1. **[Baixe o `MeetMD.dmg`](https://github.com/thebigdatacomp/meetmd/releases/latest)** → arraste pro **Aplicativos** → abra.
2. O **onboarding** guia as 3 permissões: **Gravação de Tela** (participantes), **Microfone** (sua voz), **Automação ▸ Safari** (detecção do Meet).
3. Entre num Google Meet no **Safari** (detecção automática) ou grave manual pelo ícone na barra.

O app é **assinado (Developer ID) e notarizado** pela Apple — abre sem o aviso de "desenvolvedor não identificado". É **autocontido** (whisper + modelos + helper no bundle), então roda **sem nenhuma config**.

## Usar

- **Auto:** entre num Google Meet no Safari → o app pergunta *"Gravar?"* → **Gravar**.
- **Manual:** ícone na barra → **Iniciar gravação**.
- **Nota de voz:** ícone na barra → **Nova nota de voz** → dite algo (só mic, sem permissão de tela) → **Parar e salvar nota**.
- A saída vai pra `~/.meetmd/recordings/` (reuniões em `meetings/[<projeto>/]`, notas em `notes/`). Abra a pasta no Claude e peça resumo/ações.

## Build do código

> Requer **Apple Silicon**, Go 1.21+ (o toolchain 1.25 é baixado via `GOTOOLCHAIN`) e Swift (Xcode CLT).

```bash
brew install cmake             # buildar o whisper com Metal
xcode-select --install         # swiftc

./menubar/build-app.sh         # baixa modelos + whisper, builda static Metal, empacota no MeetMD.app
open menubar/MeetMD.app
```

A 1ª vez baixa os modelos (~490MB) e compila o whisper (alguns minutos); depois fica cacheado. Para um build **distribuível** (Developer ID + notarização + `.dmg`):

```bash
RELEASE=1 NOTARY_PROFILE=<seu-perfil-notarytool> ./menubar/build-app.sh
```

Sem `RELEASE`, o build é de **dev** (self-signed/ad-hoc) — perfeito pra iterar localmente.

### Modo dev (rebuild rápido, sem empacotar)

```bash
cd bridge && make run
cd menubar && swiftc -O MeetMDBar.swift -o meetmd-bar -framework Cocoa && ./meetmd-bar &
```

## Componentes

| Componente | Stack | Papel |
|------------|-------|-------|
| `bridge/` | Go 1.25 | Captura de áudio (loopback SO), transcrição (whisper.cpp Metal), escrita dos `.md`, HTTP local |
| `menubar/` | Swift (AppKit) | App de menu-bar + helper de captura (ScreenCaptureKit) |
| `extension/` | WebExtension (MV3) | Detecta Meet, lê título/participantes do DOM, dispara start/stop |

## Configuração (opcional)

O `.app` roda **sem config**. O `~/.meetmd/config.yaml` **não é gerado na instalação** — só existe se você salvar em Configurações (ou criar à mão), e os caminhos derivam do **seu** home em runtime. Chaves úteis:

| Chave | Default | Pra quê |
|-------|---------|---------|
| `recordings_root` | `~/.meetmd/recordings` | pasta base; reuniões em `meetings/`, notas em `notes/` |
| `language` | `auto` | idioma da transcrição (whisper) |
| `ui_language` | `auto` | idioma da UI e dos `.md` (`auto` segue o SO, ou `pt`/`en`) |

## Roadmap

- Captura de áudio no **Windows** (WASAPI) e **Linux** (PipeWire) — hoje só macOS
- **Instruções por voz pro Claude** (loop voz → input)

## Licença

[Apache License 2.0](LICENSE) — uso, modificação e redistribuição livres (inclusive comercial), com concessão de patente.
