package writer

import "github.com/thebigdatacomp/meetmd/internal/config"

// texts holds every user-facing string of the generated Markdown for one
// language. Adding a language = adding one texts value to the table below.
// Format verbs (%s/%d) must line up across languages.
type texts struct {
	capturedBy        string // "> … on %s (%d min)."
	micMissing        string // warning shown when the mic channel captured nothing
	transcriptSuspect string // warning shown when the transcript covers little of the recording
	filesHeading      string
	linkFull          string // "- [Full transcript](%s)"
	linkSummary       string // "- [Summary](%s) — _to fill in_"
	linkActions       string // "- [Actions](%s) — _to fill in_"
	participants      string // heading
	notCaptured       string
	noSpeech          string

	transcriptTitle string // "# Transcript — %s"
	summaryTitle    string // "# Summary — %s"
	actionsTitle    string // "# Actions — %s"
	indexTitle      string

	summaryComment string
	tldr           string
	topics         string
	decisions      string
	openPoints     string

	actionsComment   string
	actionsTableHead string // header row + separator row
	actionsTableRow  string

	indexTableHead string // header row + separator row

	titleFallback string
	speakerYou    string
	speakerOthers string

	noteTitle string // quick voice note heading/title
}

var ptTexts = texts{
	capturedBy:        "> Reunião capturada por MeetMD em %s (%d min).\n\n",
	micMissing:        "> ⚠️ **Seu microfone não foi capturado** — este transcript tem apenas a fala dos participantes.\n\n",
	transcriptSuspect: "> ⚠️ **Transcrição possivelmente incompleta** — o transcript cobre só uma fração do que foi gravado. O áudio bruto foi preservado em `recovery/` para reprocessamento.\n\n",
	filesHeading:      "## Arquivos\n",
	linkFull:          "- [Transcrição completa](%s)\n",
	linkSummary:       "- [Resumo](%s) — _a preencher_\n",
	linkActions:       "- [Ações](%s) — _a preencher_\n\n",
	participants:      "## Participantes\n",
	notCaptured:       "- _(não capturados)_\n",
	noSpeech:          "_(sem fala detectada — nenhum áudio audível durante a gravação)_\n",

	transcriptTitle: "# Transcrição — %s\n\n",
	summaryTitle:    "# Resumo — %s\n\n",
	actionsTitle:    "# Ações — %s\n\n",
	indexTitle:      "# Reuniões — MeetMD\n\n",

	summaryComment: "<!-- MeetMD: preencha a partir de transcript.md. Remova este comentário ao concluir. -->\n\n",
	tldr:           "## TL;DR\n_(2-3 frases)_\n\n",
	topics:         "## Tópicos discutidos\n- \n\n",
	decisions:      "## Decisões\n- \n\n",
	openPoints:     "## Pontos em aberto\n- \n",

	actionsComment:   "<!-- MeetMD: extraia itens de ação de transcript.md. Um por linha. -->\n\n",
	actionsTableHead: "| # | Ação | Responsável | Prazo | Status |\n|---|------|-------------|-------|--------|\n",
	actionsTableRow:  "|   |      |             |       | aberto |\n",

	indexTableHead: "| Data | Reunião | Duração | Plataforma | Status |\n|------|---------|---------|------------|--------|\n",

	titleFallback: "Reunião sem título",
	speakerYou:    "Você",
	speakerOthers: "Participantes",

	noteTitle: "Nota de voz",
}

var enTexts = texts{
	capturedBy:        "> Meeting captured by MeetMD on %s (%d min).\n\n",
	micMissing:        "> ⚠️ **Your microphone was not captured** — this transcript only has the participants' speech.\n\n",
	transcriptSuspect: "> ⚠️ **Transcript may be incomplete** — it covers only a fraction of what was recorded. The raw audio was kept in `recovery/` so it can be re-processed.\n\n",
	filesHeading:      "## Files\n",
	linkFull:          "- [Full transcript](%s)\n",
	linkSummary:       "- [Summary](%s) — _to fill in_\n",
	linkActions:       "- [Actions](%s) — _to fill in_\n\n",
	participants:      "## Participants\n",
	notCaptured:       "- _(not captured)_\n",
	noSpeech:          "_(no speech detected — no audible audio during the recording)_\n",

	transcriptTitle: "# Transcript — %s\n\n",
	summaryTitle:    "# Summary — %s\n\n",
	actionsTitle:    "# Actions — %s\n\n",
	indexTitle:      "# Meetings — MeetMD\n\n",

	summaryComment: "<!-- MeetMD: fill in from transcript.md. Remove this comment when done. -->\n\n",
	tldr:           "## TL;DR\n_(2-3 sentences)_\n\n",
	topics:         "## Topics discussed\n- \n\n",
	decisions:      "## Decisions\n- \n\n",
	openPoints:     "## Open points\n- \n",

	actionsComment:   "<!-- MeetMD: extract action items from transcript.md. One per line. -->\n\n",
	actionsTableHead: "| # | Action | Owner | Due | Status |\n|---|--------|-------|-----|--------|\n",
	actionsTableRow:  "|   |        |       |     | open |\n",

	indexTableHead: "| Date | Meeting | Duration | Platform | Status |\n|------|---------|----------|----------|--------|\n",

	titleFallback: "Untitled meeting",
	speakerYou:    "You",
	speakerOthers: "Participants",

	noteTitle: "Voice note",
}

// textsFor returns the string set for a resolved language ("pt"/"en"),
// defaulting to English.
func textsFor(lang string) texts {
	if lang == config.LangPT {
		return ptTexts
	}
	return enTexts
}
