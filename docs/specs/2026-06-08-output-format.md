# MeetMD — Spec do Formato de Output

- **Data:** 2026-06-08
- **Status:** Proposta
- **Autor:** Robson Müller

Define a estrutura de arquivos `.md` que o MeetMD grava — o **produto principal** da ferramenta. O formato é otimizado para um LLM (Claude) ler e processar: frontmatter estável, seções previsíveis, links relativos.

## 1. Estrutura de diretórios

```
<output_root>/
├── INDEX.md
├── 2026-06-08-1430-planejamento-sprint-7/
│   ├── meeting.md        # metadados + visão geral (gerado)
│   ├── transcript.md     # transcrição completa (gerado)
│   ├── summary.md        # TEMPLATE vazio — Claude preenche
│   └── actions.md        # TEMPLATE vazio — Claude preenche
└── 2026-06-08-1600-call-cliente-x/
    └── ...
```

- **Nome da pasta:** `YYYY-MM-DD-hhmm-<slug-do-titulo>`. Ordenável cronologicamente, único, legível.
- **`slug`:** título em minúsculas, sem acento, espaços → `-`. Sem título → `reuniao`.

## 2. Convenção de frontmatter

Todos os arquivos compartilham um bloco de identidade no frontmatter YAML:

```yaml
---
id: 2026-06-08-1430-planejamento-sprint-7
title: Planejamento Sprint 7
date: 2026-06-08
start: "14:30"
end: "15:12"
duration_min: 42
platform: google-meet
participants:
  - Robson Müller
  - Alessandro
  - Leonardo
source: meetmd
---
```

Campos derivados (`duration_min`) calculados pelo bridge. `participants` vem do scrape do Meet; vazio se indisponível.

## 3. `meeting.md`

Porta de entrada da reunião. Frontmatter completo + visão geral + links para os outros arquivos.

```markdown
---
id: 2026-06-08-1430-planejamento-sprint-7
title: Planejamento Sprint 7
date: 2026-06-08
start: "14:30"
end: "15:12"
duration_min: 42
platform: google-meet
participants: [Robson Müller, Alessandro, Leonardo]
source: meetmd
status: raw            # raw | summarized
---

# Planejamento Sprint 7

> Reunião capturada por MeetMD em 2026-06-08 14:30 (42 min).

## Arquivos
- [Transcrição completa](transcript.md)
- [Resumo](summary.md) — _a preencher_
- [Ações](actions.md) — _a preencher_

## Participantes
- Robson Müller
- Alessandro
- Leonardo
```

O campo `status` permite ao Claude saber se o resumo já foi gerado (`raw` → ainda não).

## 4. `transcript.md`

Transcrição completa com timestamps e rótulo mínimo de falante (`Você` vs `Participantes`, da separação de 2 canais — ver spec de arquitetura §3.2).

```markdown
---
id: 2026-06-08-1430-planejamento-sprint-7
title: Planejamento Sprint 7
date: 2026-06-08
source: meetmd
kind: transcript
---

# Transcrição — Planejamento Sprint 7

[00:00:04] Participantes: Bom, vamos começar pelo board do sprint.
[00:00:11] Você: Beleza. A issue 70 já fechou, então sobra...
[00:01:23] Participantes: Sobre o deploy, acho melhor segurar até sexta.
...
```

- Timestamps `[hh:mm:ss]` relativos ao início da reunião.
- Rótulo de falante limitado a `Você` / `Participantes` no MVP (sem diarização por pessoa).
- Texto cru do Whisper, sem edição.

## 5. `summary.md` (template a preencher)

Gerado **vazio**, com seções e instrução para o Claude. A ferramenta não chama LLM (decisão: transcript + estrutura pré-pronta).

```markdown
---
id: 2026-06-08-1430-planejamento-sprint-7
title: Planejamento Sprint 7
date: 2026-06-08
source: meetmd
kind: summary
status: empty          # empty | filled
---

# Resumo — Planejamento Sprint 7

<!-- MeetMD: preencha a partir de transcript.md. Remova este comentário ao concluir. -->

## TL;DR
_(2-3 frases)_

## Tópicos discutidos
-

## Decisões
-

## Pontos em aberto
-
```

## 6. `actions.md` (template a preencher)

```markdown
---
id: 2026-06-08-1430-planejamento-sprint-7
title: Planejamento Sprint 7
date: 2026-06-08
source: meetmd
kind: actions
status: empty
---

# Ações — Planejamento Sprint 7

<!-- MeetMD: extraia itens de ação de transcript.md. Um por linha. -->

| # | Ação | Responsável | Prazo | Status |
|---|------|-------------|-------|--------|
|   |      |             |       | aberto |
```

## 7. `INDEX.md` (raiz)

Mantido pelo bridge a cada nova reunião. Tabela mais recente no topo.

```markdown
---
source: meetmd
kind: index
updated: 2026-06-08
---

# Reuniões — MeetMD

| Data | Reunião | Duração | Plataforma | Status |
|------|---------|---------|------------|--------|
| 2026-06-08 14:30 | [Planejamento Sprint 7](2026-06-08-1430-planejamento-sprint-7/meeting.md) | 42 min | Google Meet | raw |
| 2026-06-08 16:00 | [Call cliente X](2026-06-08-1600-call-cliente-x/meeting.md) | 28 min | Google Meet | raw |
```

## 8. Contrato de uso pelo Claude

O fluxo pretendido: o usuário aponta o Claude para `<output_root>` e pede, por exemplo, _"resuma a última reunião"_. O Claude:

1. Lê `INDEX.md` → acha a reunião mais recente.
2. Abre `meeting.md` (contexto) e `transcript.md` (conteúdo).
3. Preenche `summary.md` e `actions.md`, troca `status: empty → filled`.
4. Atualiza `status: raw → summarized` em `meeting.md` e no `INDEX.md`.

O formato é estável e previsível justamente para que esse contrato funcione sem ambiguidade.

## 9. Decisões de formato

- **Markdown + frontmatter YAML:** legível por humano e trivial de parsear por LLM.
- **Arquivos separados** (transcript / summary / actions) em vez de um só: o Claude pode reescrever `summary.md` sem tocar no transcript cru, e o transcript pode ser grande.
- **Templates com comentário-instrução (`<!-- MeetMD: ... -->`):** guiam o LLM e são removíveis, sem poluir o doc final.
- **Campos `status`:** tornam o pipeline idempotente — dá pra saber o que já foi processado.
