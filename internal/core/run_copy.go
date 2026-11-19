package gent

import "slices"

// Copies here serve explicit mutable display copies and stable saved capture. Ordinary read-only
// Subscriber delivery shares the frozen envelope and does not use this helper.
func cloneRecordedEvent(registry *CodecRegistry, event *RecordedEvent) (*RecordedEvent, error) {
	return cloneOwned(registry, event)
}

func cloneRecordedEvents(
	registry *CodecRegistry, events []*RecordedEvent,
) ([]*RecordedEvent, error) {
	owned := slices.Clone(events)
	for i, event := range events {
		var err error
		owned[i], err = cloneRecordedEvent(registry, event)
		if err != nil {
			return nil, err
		}
	}
	return owned, nil
}

func cloneSaveBatch(registry *CodecRegistry, batch *SaveBatch) (*SaveBatch, error) {
	owned := *batch
	var err error
	owned.Events, err = cloneRecordedEvents(registry, batch.Events)
	if err != nil {
		return nil, err
	}
	owned.Runtime, err = cloneOwned(registry, batch.Runtime)
	if err != nil {
		return nil, err
	}
	// Preserve checkpoint values, including nil and empty maps, for an exact save retry.
	owned.Checkpoint, err = cloneOwned(registry, batch.Checkpoint)
	if err != nil {
		return nil, err
	}
	return &owned, nil
}
