package gent

import "reflect"

// Independent decoded maps have different pointer keys. Compare their saved logical values,
// including concrete types and nil/empty distinctions, when ordinary Go equality is insufficient.
// Typed trees avoid constructing two complete expanded JSON documents or relying on hash equality.
func (encoder *valueEncoder) equalSavedValues(left, right any) (bool, error) {
	if reflect.DeepEqual(left, right) {
		return true, nil
	}
	first, err := encoder.encode(reflect.ValueOf(left), 0)
	if err != nil {
		return false, err
	}
	second, err := encoder.encode(reflect.ValueOf(right), 0)
	if err != nil {
		return false, err
	}
	return reflect.DeepEqual(first, second), nil
}

func (decoder *valueDecoder) equalSavedValues(left, right any) (bool, error) {
	encoder := newValueEncoder(decoder.CodecRegistry)
	equal, err := encoder.equalSavedValues(left, right)
	if err != nil {
		switch encoder.status {
		case valueEncodeIncompatible:
			decoder.status = valueDecodeIncompatible
		case valueEncodeOperationFailed:
			decoder.status = valueDecodeOperationFailed
		}
	}
	return equal, err
}
