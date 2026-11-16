package main

import (
	"context"

	"github.com/rickchristie/gent"
)

// Database is the application's storage adapter, not a Gent interface.
// Each handle belongs to one run and one worker. Insert commits the complete batch atomically,
// rejects an old worker, and accepts an identical retry. Get returns the complete saved RunView.
// The adapter owns transactions and codecs so the agent code does not depend on a database driver.
type Database interface {
	Insert(context.Context, *gent.SaveBatch) error
	Get(context.Context, string) (gent.RunView, error)
}

type Recorder struct{ DB Database }

// Save is the Gent persistence boundary. Returning nil means the batch has committed.
func (r Recorder) Save(ctx context.Context, batch *gent.SaveBatch) error {
	return r.DB.Insert(ctx, batch)
}
