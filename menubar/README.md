# MeetMD — App de menu-bar (macOS)

Ícone na barra do topo do macOS que controla o [bridge local](../bridge). Cliente HTTP fino, sem Xcode (compila com `swiftc`).

## O que faz

- **Ícone reflete o estado:** 🔴 gravando · ⏸ pausado · 🎙 pronto · ⚠︎ bridge offline.
- **Popup ao detectar reunião:** quando o bridge detecta um Google Meet no Safari (modo `ask`), pergunta *"Começar a gravar?"* com **Gravar** / **Agora não** (não pergunta de novo a mesma reunião recusada).
- **Menu:** Iniciar · Pausar/Retomar · Parar e salvar · Abrir pasta dos arquivos · Abrir painel · Sair.
- Se o bridge estiver offline, tenta subi-lo (`meetmd serve`) — procura o binário em `MEETMD_BIN`, ao lado do app, ou nos caminhos comuns.

## Build & run

```bash
swiftc -O MeetMDBar.swift -o meetmd-bar -framework Cocoa
./meetmd-bar    # aparece na barra do topo; sem ícone no Dock
```

Para iniciar no login: adicione `meetmd-bar` em **Ajustes ▸ Geral ▸ Itens de Início**.

## Permissões

A captura de áudio e a detecção do Safari são do **bridge**, não deste app — então as permissões (Gravação de Tela, Automation) são pedidas pelo processo que roda o `meetmd serve`. Veja [../spike/macos-audio/README.md](../spike/macos-audio/README.md).

## Limitações (MVP)

- Gravações iniciadas pelo popup não levam projeto (vão pro `output_root` base); use o painel/CLI com `-p` para separar por projeto.
- Cliente puro: não embute o bridge, só o controla.
