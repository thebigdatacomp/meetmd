# M1 Spike — Captura de áudio do sistema no macOS (ScreenCaptureKit)

Prova a premissa mais arriscada do MeetMD: capturar o **áudio misturado do sistema** (todos os participantes que você ouve), **sem driver virtual** e de forma **agnóstica de navegador/app**.

## Resultado do spike (2026-06-08)

- ✅ **Validado de ponta a ponta.** Após conceder **Screen Recording** ao VSCode, o helper capturou **3.2s de áudio do sistema** num WAV de ~614 KB (48kHz × 2ch × 16-bit, duração confirmada via `afinfo`). Captura o áudio misturado (todos os participantes), **sem driver virtual** e agnóstico de navegador/app.
- ℹ️ **Permissão é o único atrito.** Sem a permissão **Screen Recording (TCC)**, a API retorna `SCStreamErrorDomain Code=-3801 "user declined TCCs"`. A permissão só vale **após reiniciar o app** que roda o binário. No produto, é pedida uma vez no onboarding.
- 🎯 **Conclusão:** o maior risco da arquitetura está **resolvido e verificado**.

## Build

```bash
swiftc -O SystemAudioRecorder.swift -o system-audio-recorder
```

## Conceder permissão e validar

1. Rode uma vez: `./system-audio-recorder out.wav 8`
2. No primeiro uso, macOS pede **Screen Recording**. Se não aparecer o prompt, conceda manualmente em:
   **Ajustes do Sistema ▸ Privacidade e Segurança ▸ Gravação de Tela** → habilite o **terminal** (Terminal/iTerm/VS Code) que está rodando o binário.
3. **Reinicie o terminal** (a permissão só vale após reabrir) e rode de novo:
   ```bash
   # toque um áudio/vídeo qualquer durante a captura
   ./system-audio-recorder out.wav 8
   afinfo out.wav   # deve mostrar ~8s, 48kHz, 2 canais
   ```
4. Sucesso = `out.wav` com a duração esperada e o áudio audível.

## Como vira produto

Este helper é o protótipo do binário que o **bridge Go** vai invocar no macOS (`internal/audio` com build tag `darwin`): o bridge gerencia start/stop e o helper Swift faz a captura, gravando o WAV que o whisper.cpp (M2) transcreve. No produto final, a permissão é pedida no onboarding, uma vez.

## Notas

- `excludesCurrentProcessAudio = true` evita capturar o próprio áudio do helper.
- Saída: 16kHz, mono, PCM 16-bit — formato nativo do whisper.cpp (sem resample).
- Windows (WASAPI loopback) e Linux (monitor PipeWire/PulseAudio) são caminhos análogos, sem o atrito de permissão do macOS.
