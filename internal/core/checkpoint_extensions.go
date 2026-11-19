package gent

import (
	"encoding/json"
	"fmt"
	"reflect"
)

// BindCheckpointState binds an explicit extension before execution. Save/Restore methods must
// only copy local state; they must not publish events or call external services.
func (execCtx *ExecutionContext) BindCheckpointState(name string, component CheckpointState) error {
	if name == "" || component == nil || component.CheckpointType() == "" ||
		component.CheckpointVersion() < 1 {
		return fmt.Errorf("%w: invalid checkpoint component %q", ErrUnsupportedRecovery, name)
	}
	execCtx.run.mu.Lock()
	defer execCtx.run.mu.Unlock()
	if execCtx.run.components == nil {
		execCtx.run.components = map[string]CheckpointState{}
	}
	if existing := execCtx.run.components[name]; existing != nil {
		if !reflect.ValueOf(existing).Comparable() || existing != component {
			return fmt.Errorf("gent: component %q already bound", name)
		}
	}
	execCtx.run.components[name] = component
	return nil
}

func (execCtx *ExecutionContext) captureExtensions() error {
	execCtx.run.mu.RLock()
	components := make(map[string]CheckpointState, len(execCtx.run.components))
	for name, component := range execCtx.run.components {
		components[name] = component
	}
	execCtx.run.mu.RUnlock()
	values := map[string]SavedCheckpointState{}
	for name, component := range components {
		value, err := component.SaveCheckpointState()
		if err != nil {
			return fmt.Errorf("gent: save component %q: %w", name, err)
		}
		if value.Type == "" || value.Version < 1 || !json.Valid(value.Data) {
			return fmt.Errorf("gent: invalid state from component %q", name)
		}
		values[name] = SavedCheckpointState{component.CheckpointType(),
			component.CheckpointVersion(), *value}
	}
	execCtx.run.mu.Lock()
	execCtx.run.extensions = values
	execCtx.run.mu.Unlock()
	return nil
}
