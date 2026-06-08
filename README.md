# MeetMD

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

## Status

🚧 Especificação. Ver `docs/specs/`.
