package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/rickchristie/gent"
	"github.com/stretchr/testify/assert"
)

func testStore(t *testing.T, path string) *Store {
	t.Helper()
	codecs := gent.NewCodecRegistry()
	assert.NoError(t, newAgent(&DemoModel{}).RegisterCodecs(codecs))
	store, err := openStore(path, codecs)
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	t.Cleanup(func() { assert.NoError(t, store.db.Close()) })
	return store
}

func savedBatch(t *testing.T, number, previous uint64) *gent.SaveBatch {
	t.Helper()
	batch := &gent.SaveBatch{SchemaVersion: gent.RunSchemaVersion, RunId: "test",
		PreviousEventNumber: previous, Events: []*gent.RecordedEvent{{
			RunId: "test", EventNumber: number, Type: "checkpoint",
			Ts: time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC),
		}}}
	var err error
	batch.BatchId, err = gent.BatchDigest(batch)
	assert.NoError(t, err)
	return batch
}

func TestSaveTransaction(t *testing.T) {
	type input struct {
		number, previous uint64
		replaceWriter    bool
		cancel           bool
	}
	type expected struct {
		view gent.RunView
		err  error
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	first, next := savedBatch(t, 1, 0), savedBatch(t, 2, 1)
	firstView := gent.RunView{SchemaVersion: gent.RunSchemaVersion, RunId: "test",
		LastAppliedEventNumber: 1, Activity: first.Events,
		Batches: map[string]string{first.BatchId: first.BatchId}}
	nextView := gent.RunView{SchemaVersion: gent.RunSchemaVersion, RunId: "test",
		LastAppliedEventNumber: 2, Activity: []*gent.RecordedEvent{first.Events[0], next.Events[0]},
		Batches: map[string]string{first.BatchId: first.BatchId, next.BatchId: next.BatchId}}
	cases := []testCase{
		{"next batch advances the full view", input{2, 1, false, false}, expected{nextView, nil}},
		{"duplicate is idempotent", input{1, 0, false, false}, expected{firstView, nil}},
		{"gap is rejected", input{3, 1, false, false}, expected{firstView, gent.ErrEventGap}},
		{"stale cursor is rejected", input{2, 0, false, false}, expected{firstView, gent.ErrEventGap}},
		{"old worker is fenced", input{2, 1, true, false}, expected{firstView, errStaleWriter}},
		{"canceled save is rejected", input{2, 1, false, true}, expected{firstView, context.Canceled}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			store := testStore(t, filepath.Join(t.TempDir(), "runs.db"))
			database, err := store.claim("test")
			assert.NoError(t, err)
			writer := Recorder{DB: database}
			assert.NoError(t, writer.Save(context.Background(), first))
			before, err := database.Get(context.Background(), "test")
			assert.NoError(t, err)
			assert.Equal(t, firstView, before)
			if test.input.replaceWriter {
				_, err := store.claim("test")
				assert.NoError(t, err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if test.input.cancel {
				cancel()
			}
			err = writer.Save(ctx, savedBatch(t, test.input.number, test.input.previous))
			assert.ErrorIs(t, err, test.expected.err)
			after, err := database.Get(context.Background(), "test")
			assert.NoError(t, err)
			assert.Equal(t, test.expected.view, after)
		})
	}
}

func TestDatabaseGet(t *testing.T) {
	type input struct {
		runID  string
		cancel bool
	}
	type expected struct {
		view gent.RunView
		err  error
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	batch := savedBatch(t, 1, 0)
	view := gent.RunView{SchemaVersion: gent.RunSchemaVersion, RunId: "test",
		LastAppliedEventNumber: 1, Activity: batch.Events,
		Batches: map[string]string{batch.BatchId: batch.BatchId}}
	cases := []testCase{
		{"complete saved run", input{"test", false}, expected{view, nil}},
		{"wrong run", input{"other", false}, expected{gent.RunView{}, gent.ErrWrongRun}},
		{"canceled read", input{"test", true}, expected{gent.RunView{}, context.Canceled}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			store := testStore(t, filepath.Join(t.TempDir(), "runs.db"))
			database, err := store.claim("test")
			assert.NoError(t, err)
			assert.NoError(t, Recorder{DB: database}.Save(context.Background(), batch))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if test.input.cancel {
				cancel()
			}
			actual, err := database.Get(ctx, test.input.runID)
			assert.ErrorIs(t, err, test.expected.err)
			assert.Equal(t, test.expected.view, actual)
		})
	}
}
