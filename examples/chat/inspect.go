package main

import (
	"encoding/json"
	"log"
	"net/http"
)

type inspectionKind string

const (
	// inspectionView reads the complete accepted archive without running an agent.
	inspectionView inspectionKind = "view"
	// inspectionState reads compact execution state as an encoded RunStateSnapshot.
	inspectionState inspectionKind = "state"
)

type inspection struct {
	Kind       inspectionKind `json:"kind"`
	RunId      string         `json:"runId"`
	Head       uint64         `json:"head"`
	BatchCount int            `json:"batchCount"`
	// Encoded preserves exact codec bytes. Reformatting JSON can change an integrity digest or
	// lose precision in a JavaScript number. The browser parses a separate view for inspection.
	Encoded string `json:"encoded"`
	// Decoded is diagnostic JSON of the recovered value, not a replacement recovery format.
	Decoded      string `json:"decoded"`
	EncodedBytes int    `json:"encodedBytes"`
	DecodedBytes int    `json:"decodedBytes"`
}

func (app *application) handleInspect(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	kind, path := inspectionKind(query.Get("kind")), replayPath(query.Get("path"))
	if kind != inspectionView && kind != inspectionState {
		writeJSON(w, 400, map[string]string{"detail": "Select view or state for inspection."})
		return
	}
	if path != replayBatches && path != replayCapture {
		writeJSON(w, 400, map[string]string{"detail": "Select batches or capture for replay."})
		return
	}
	runId := query.Get("runId")
	data, exists := app.store.read(runId)
	if !exists {
		writeJSON(w, 404, map[string]string{"detail": "Conversation not found."})
		return
	}
	if data.head == 0 {
		writeJSON(w, 409, map[string]string{"detail": "No accepted savepoint is available yet."})
		return
	}
	// This fixed read owns immutable byte references. Inspection cannot see provisional worker
	// state or call models, tools, output processing, or custom LoopData restore callbacks.
	result, err := app.inspectData(runId, data, kind, path)
	if err != nil {
		log.Printf("inspect %s for run %s: %v", kind, runId, err)
		writeJSON(w, 500, map[string]string{
			"detail": "Cannot inspect saved data. Check server logs.",
		})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, result)
}

func (app *application) inspectData(runId string, data *storedBytes,
	kind inspectionKind, path replayPath,
) (*inspection, error) {
	var recovered any
	var encoded []byte
	var err error
	if kind == inspectionState {
		state, loadErr := app.store.load(runId, data, path)
		if loadErr != nil {
			return nil, loadErr
		}
		recovered = state
		encoded, err = app.store.codecs.MarshalRunState(state)
	} else {
		view, loadErr := app.store.archive(runId, data)
		if loadErr != nil {
			return nil, loadErr
		}
		recovered = view
		encoded, err = app.store.codecs.Marshal(view)
	}
	if err != nil {
		return nil, err
	}
	// Marshal the actual recovered value. Do not strip codec wrappers in the browser: native
	// types and private state layouts belong to Gent, and the two views must share one read head.
	decoded, err := json.Marshal(recovered)
	if err != nil {
		return nil, err
	}
	return &inspection{Kind: kind, RunId: runId, Head: data.head, BatchCount: len(data.batches),
		Encoded: string(encoded), Decoded: string(decoded),
		EncodedBytes: len(encoded), DecodedBytes: len(decoded)}, nil
}
