package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/thebigdatacomp/meetmd/internal/config"
	"github.com/thebigdatacomp/meetmd/internal/model"
)

// httpClient has a generous timeout: stop triggers transcription, which can
// take a while on longer recordings.
var httpClient = &http.Client{Timeout: 10 * time.Minute}

const defaultPort = 8765

func runStart(args []string) {
	fs := flag.NewFlagSet("start", flag.ExitOnError)
	project := fs.String("p", "", "projeto (separa as gravações em output_root/<projeto>)")
	fs.StringVar(project, "project", "", "alias de -p")
	_ = fs.Parse(args)

	title := strings.TrimSpace(strings.Join(fs.Args(), " "))
	payload := map[string]any{
		"title":     title,
		"project":   strings.TrimSpace(*project),
		"platform":  string(model.PlatformManual),
		"startedAt": time.Now().Format(time.RFC3339),
	}
	var out struct {
		SessionID string `json:"sessionId"`
	}
	if err := doJSON(http.MethodPost, "/sessions/start", payload, &out); err != nil {
		clientFail(err)
	}
	if p := strings.TrimSpace(*project); p != "" {
		fmt.Printf("● gravando [%s]: %s\n", p, out.SessionID)
	} else {
		fmt.Printf("● gravando: %s\n", out.SessionID)
	}
}

func runStop() {
	id, recording := activeSession()
	if !recording {
		fmt.Println("○ nada gravando")
		return
	}
	var out struct {
		SessionDir string `json:"sessionDir"`
	}
	if err := doJSON(http.MethodPost, "/sessions/"+id+"/stop", nil, &out); err != nil {
		clientFail(err)
	}
	fmt.Printf("✓ salvo em %s\n", out.SessionDir)
}

func runPause() {
	id, recording := activeSession()
	if !recording {
		fmt.Println("○ nada gravando")
		return
	}
	if err := doJSON(http.MethodPost, "/sessions/"+id+"/pause", nil, nil); err != nil {
		clientFail(err)
	}
	fmt.Println("⏸ pausado")
}

func runResume() {
	id, recording := activeSession()
	if !recording {
		fmt.Println("○ nada gravando")
		return
	}
	if err := doJSON(http.MethodPost, "/sessions/"+id+"/resume", nil, nil); err != nil {
		clientFail(err)
	}
	fmt.Println("● retomado")
}

func runCancel() {
	id, recording := activeSession()
	if !recording {
		fmt.Println("○ nada gravando")
		return
	}
	if err := doJSON(http.MethodPost, "/sessions/"+id+"/cancel", nil, nil); err != nil {
		clientFail(err)
	}
	fmt.Println("✓ cancelado")
}

func runStatus() {
	state, meeting := status()
	switch {
	case state == stateRecording && meeting != nil:
		fmt.Printf("● gravando: %s\n", titleOr(meeting.Title))
	case state == statePaused && meeting != nil:
		fmt.Printf("⏸ pausado: %s\n", titleOr(meeting.Title))
	default:
		fmt.Println("○ ocioso")
	}
}

// --- helpers ----------------------------------------------------------------

// State strings mirror session.State* without importing the package.
const (
	stateRecording = "recording"
	statePaused    = "paused"
)

type statusMeeting struct {
	ID    string `json:"ID"`
	Title string `json:"Title"`
}

func status() (string, *statusMeeting) {
	var out struct {
		State   string         `json:"state"`
		Meeting *statusMeeting `json:"meeting"`
	}
	if err := doJSON(http.MethodGet, "/status", nil, &out); err != nil {
		clientFail(err)
	}
	return out.State, out.Meeting
}

func activeSession() (string, bool) {
	state, meeting := status()
	if (state == stateRecording || state == statePaused) && meeting != nil {
		return meeting.ID, true
	}
	return "", false
}

func doJSON(method, path string, payload, out any) error {
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, baseURL()+path, body)
	if err != nil {
		return err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("bridge não respondeu (rodando? `meetmd serve`): %w", err)
	}
	defer res.Body.Close()

	data, _ := io.ReadAll(res.Body)
	if res.StatusCode >= http.StatusMultipleChoices {
		var e struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(data, &e) == nil && e.Message != "" {
			return fmt.Errorf("%s", e.Message)
		}
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

func baseURL() string {
	port := defaultPort
	if cfg, err := config.Load(); err == nil && cfg.Port != 0 {
		port = cfg.Port
	}
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}

func titleOr(title string) string {
	if strings.TrimSpace(title) == "" {
		return "Reunião sem título"
	}
	return title
}

func clientFail(err error) {
	fmt.Fprintln(os.Stderr, "erro:", err)
	os.Exit(1)
}
