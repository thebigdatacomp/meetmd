# MeetMD — Claude Code Context

Captura reuniões e grava a transcrição estruturada em Markdown num diretório local, pronta para o Claude processar (sumarizar, extrair ações, cruzar com outros docs).

## Key Rules

- _ALWAYS_ run `go build ./... && go test ./...` em `bridge/` antes de commitar mudanças no bridge
- _ALWAYS_ rodar o build da extensão antes de commitar mudanças em `extension/`
- _NEVER_ capturar/gravar áudio na extensão — captura é responsabilidade do bridge (loopback de SO)
- _NEVER_ persistir áudio bruto além do necessário para transcrever; apagar o `.wav` temporário ao final por padrão
- _ALWAYS_ manter o transcritor atrás de uma interface (`Transcriber`) — whisper.cpp é o default, mas plugável
- _NEVER_ expor secrets em código — config via arquivo `~/.meetmd/config.yaml` + env vars

## Arquitetura

**Dois componentes:**

- **`bridge/`** — daemon Go local. Captura áudio do SO (loopback), transcreve com whisper.cpp, escreve os `.md`, expõe HTTP local (`127.0.0.1`). É o núcleo.
- **`extension/`** — WebExtension MV3. Detecta Google Meet, lê título/participantes do DOM, dispara `start`/`stop` no bridge. É um helper de detecção/metadados, **não** captura áudio.

**Stack:** Go 1.25 (bridge) + WebExtension MV3 (extensão) + whisper.cpp (transcrição local).

**Decisões-chave** (ver `docs/specs/2026-06-08-architecture.md`):
- Captura no nível do SO → agnóstico de navegador + pega todos os participantes.
- Whisper local → privacidade (áudio nunca sai da máquina) + zero custo por minuto.
- macOS é o caso mais sensível de captura de áudio (ScreenCaptureKit ou BlackHole).

## Output

Formato dos arquivos `.md` consumidos pelo Claude: ver `docs/specs/2026-06-08-output-format.md`.

## Docs

```
docs/
  specs/    # Specs antes da implementação — YYYY-MM-DD-description.md
  design/   # Design do que foi efetivamente construído
  claude/tmp/   # Saídas temporárias (gitignored)
```
