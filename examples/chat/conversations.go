package main

import (
	"crypto/rand"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/rickchristie/gent"
)

// This small index belongs to the example server. Agent history still comes from saved bytes.
// Titles use the first user input, so listing chats needs no model call or archive decoding.
type conversationInfo struct {
	RunId     string               `json:"runId"`
	Scenario  scenarioId           `json:"scenario"`
	Title     string               `json:"title"`
	UpdatedAt time.Time            `json:"updatedAt"`
	Active    bool                 `json:"active"`
	Status    gent.ExecutionStatus `json:"status"`
}

func conversationTitle(prompt string) string {
	text := []rune(strings.Join(strings.Fields(prompt), " "))
	if len(text) > 80 {
		return string(text[:80]) + "…"
	}
	return string(text)
}

func (app *application) conversations() []conversationInfo {
	app.mu.Lock()
	defer app.mu.Unlock()
	result := make([]conversationInfo, 0, len(app.chats))
	for id, chat := range app.chats {
		item := *chat // The response must remain stable when a worker finishes after this read.
		item.Active = app.running[id] != nil
		if outcome := app.outcomes[id]; outcome != nil {
			item.Status = outcome.Status
		}
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].UpdatedAt.Equal(result[j].UpdatedAt) {
			return result[i].RunId < result[j].RunId
		}
		return result[i].UpdatedAt.After(result[j].UpdatedAt)
	})
	return result
}

const serverHeader = "X-Gent-Server"

// A server identity fences writes sent before a reset, including a delayed first prompt that
// has no RunId yet. Reads expose the current identity; the browser returns it on each write.
func (app *application) acceptServer(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set(serverHeader, app.serverId)
	if r.Method != http.MethodPost || r.Header.Get(serverHeader) == app.serverId {
		return true
	}
	writeJSON(w, http.StatusConflict,
		map[string]string{"detail": "Server state changed. Reload conversations before retrying."})
	return false
}

func (app *application) handleReset(w http.ResponseWriter, r *http.Request) {
	app.lifecycle.Lock()
	defer app.lifecycle.Unlock()
	if !app.acceptServer(w, r) {
		return
	}
	app.mu.Lock()
	for _, cancel := range app.cancels {
		cancel()
	}
	app.mu.Unlock()
	// Cancellation interrupts model work. Release in each worker joins its producers and
	// subscribers. Only then can reset discard data without a late save recreating a run.
	app.wait.Wait()
	app.mu.Lock()
	clear(app.sessions)
	clear(app.outcomes)
	clear(app.chats)
	app.mu.Unlock()
	app.store.clear()
	app.hub.clear()
	app.serverId = rand.Text()
	w.Header().Set(serverHeader, app.serverId)
	writeJSON(w, http.StatusOK, map[string]string{"detail": "Server reset. All chats were cleared."})
}
