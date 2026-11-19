package gent

import "reflect"

// PrepareOutput registers the result type and validates output controls before model work.
// Configuration does not process output or bind a saved run to a Runner version. Restore checks
// that saved values can be decoded with the current type and registered codecs.
func PrepareOutput[T any](execCtx *ExecutionContext, output Output[T]) error {
	if err := execCtx.Codecs().RegisterValueType(reflect.TypeFor[T]()); err != nil {
		return err
	}
	request := NewModelCallRequest(nil)
	if err := output.Configure(request); err != nil {
		return err
	}
	_, err := captureOutputControls(request.Options)
	return err
}
