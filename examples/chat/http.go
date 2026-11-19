package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"time"
)

//go:embed web
var webFiles embed.FS

func (app *application) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, struct {
			Provider  string         `json:"provider"`
			Scenarios []scenarioInfo `json:"scenarios"`
		}{app.provider, scenarios})
	})
	mux.HandleFunc("POST /api/action", app.handleAction)
	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, r *http.Request) {
		runId := r.URL.Query().Get("runId")
		if !app.requireRun(w, runId) {
			return
		}
		state, err := app.snapshot(runId, replayCapture)
		if err != nil {
			log.Printf("restore display: %v", err)
			writeJSON(w, 500, map[string]string{"detail": "Saved conversation cannot be restored."})
			return
		}
		writeJSON(w, 200, state)
	})
	mux.HandleFunc("GET /api/events", app.handleEvents)
	mux.HandleFunc("GET /api/inspect", app.handleInspect)
	files, err := fs.Sub(webFiles, "web")
	if err != nil {
		panic(err)
	}
	mux.Handle("/", http.FileServer(http.FS(files)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; object-src 'none'; "+
			"base-uri 'none'; frame-ancestors 'none'")
		// This local example has no accounts. Reject cross-origin browser writes so another
		// website cannot use the user's configured model credentials through this server.
		if r.Method == http.MethodPost {
			origin := r.Header.Get("Origin")
			if origin != "" && origin != "http://"+r.Host {
				http.Error(w, "Cross-origin action rejected", http.StatusForbidden)
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}

func (app *application) handleAction(w http.ResponseWriter, r *http.Request) {
	var input actionRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeJSON(w, 400, map[string]string{"detail": "Invalid action input."})
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeJSON(w, 400, map[string]string{"detail": "Expected one action."})
		return
	}
	if input.Path == "" {
		input.Path = replayCapture
	}
	if input.Path != replayCapture && input.Path != replayBatches {
		writeJSON(w, 400, map[string]string{"detail": "Unknown replay path."})
		return
	}
	switch input.Action {
	case actionStop:
		if !app.stop(&input) {
			writeJSON(w, 409, map[string]string{"detail": "Stale Stop: the named attempt is not active."})
			return
		}
		writeJSON(w, 202, map[string]string{"runId": input.RunId})
	case actionRestore:
		if !app.requireRun(w, input.RunId) {
			return
		}
		app.mu.Lock()
		active := app.running[input.RunId] != nil
		app.mu.Unlock()
		if active {
			writeJSON(w, 409, map[string]string{"detail": "Stop execution before restoring."})
			return
		}
		state, err := app.snapshot(input.RunId, input.Path)
		if err != nil {
			log.Printf("restore display: %v", err)
			writeJSON(w, 500, map[string]string{"detail": "Saved conversation cannot be restored."})
			return
		}
		writeJSON(w, 200, state)
	case actionTurn, actionContinue:
		if input.Action == actionTurn && strings.TrimSpace(input.Prompt) == "" {
			writeJSON(w, 400, map[string]string{"detail": "Enter a task first."})
			return
		}
		runId, status, err := app.start(&input)
		if err != nil {
			detail := err.Error()
			if status == 500 {
				log.Printf("start: %v", err)
				detail = "Could not prepare execution. Inspect the server log."
			}
			writeJSON(w, status, map[string]string{"detail": detail})
			return
		}
		writeJSON(w, status, map[string]string{"runId": runId})
	default:
		writeJSON(w, 400, map[string]string{"detail": "Unknown action."})
	}
}

// Only a missing run returns 404. A failed restore must keep its URL so the application can
// repair saved data or configuration instead of treating accepted history as absent.
func (app *application) requireRun(w http.ResponseWriter, runId string) bool {
	if _, exists := app.store.read(runId); exists {
		return true
	}
	writeJSON(w, 404, map[string]string{"detail": "Conversation not found."})
	return false
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		log.Printf("HTTP encode: %v", err)
		http.Error(w, "Response encoding failed", 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if _, err := w.Write(data); err != nil {
		log.Printf("HTTP write: %v", err)
	}
}

func (app *application) handleEvents(w http.ResponseWriter, r *http.Request) {
	runId := r.URL.Query().Get("runId")
	if _, exists := app.store.read(runId); !exists {
		http.Error(w, "Conversation not found", 404)
		return
	}
	// Subscribe first, then read a fixed accepted head. Buffered events can overlap that head;
	// the browser deduplicates them by EventSequenceNumber. There is no subscribe-after-read gap.
	queue, unsubscribe := app.hub.subscribe(runId)
	defer unsubscribe()
	state, err := app.snapshot(runId, replayCapture)
	if err != nil {
		log.Printf("stream restore: %v", err)
		http.Error(w, "Could not restore display", 500)
		return
	}
	data, err := json.Marshal(&browserFrame{Kind: frameReset, State: state})
	if err != nil {
		log.Printf("stream encode: %v", err)
		http.Error(w, "Could not encode display", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	control := http.NewResponseController(w)
	write := func(data []byte) error {
		if err := control.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return err
		}
		return control.Flush()
	}
	if err := write(data); err != nil {
		log.Printf("stream initial write: %v", err)
		return
	}
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case data, ok := <-queue:
			if !ok {
				return
			}
			if err := write(data); err != nil {
				log.Printf("stream write: %v", err)
				return
			}
		case <-ticker.C:
			data, err := json.Marshal(&browserFrame{Kind: frameHeartbeat})
			if err != nil {
				log.Printf("heartbeat encode: %v", err)
				return
			}
			if err := write(data); err != nil {
				log.Printf("stream heartbeat: %v", err)
				return
			}
		}
	}
}
