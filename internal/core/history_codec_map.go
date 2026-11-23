package gent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

func compareEncodedKeys(left, right EncodedValue) int {
	if left.Type < right.Type {
		return -1
	}
	if left.Type > right.Type {
		return 1
	}
	return bytes.Compare(left.Data, right.Data)
}

func sortEncodedItems(items []encodedMapItem[encodingValue]) error {
	// Keep the existing key order and receipts for ordinary maps. Distinct pointer keys can
	// have equal saved keys, including pointers inside structs, arrays, and interface keys.
	sort.Slice(items, func(i, j int) bool {
		return compareEncodedKeys(items[i].Key, items[j].Key) < 0
	})
	for start := 0; start < len(items); {
		end := start + 1
		for end < len(items) && compareEncodedKeys(items[start].Key, items[end].Key) == 0 {
			end++
		}
		if end-start > 1 {
			if err := sortEqualKeyValues(items[start:end]); err != nil {
				return err
			}
		}
		start = end
	}
	return nil
}

func sortEqualKeyValues(items []encodedMapItem[encodingValue]) error {
	// Only equal-key groups need value bytes. Encode each already prepared value once, then
	// sort by the complete typed value. Addresses cannot survive decoding, and equal keys must
	// not discard distinct entries. Fully equal entries can keep any order without changing bytes.
	type orderedItem struct {
		item encodedMapItem[encodingValue]
		data []byte
	}
	ordered := make([]orderedItem, len(items))
	for i, item := range items {
		data, err := json.Marshal(item.Value)
		if err != nil {
			return fmt.Errorf("gent: order saved map values: %w", err)
		}
		ordered[i] = orderedItem{item: item, data: data}
	}
	sort.Slice(ordered, func(i, j int) bool {
		return bytes.Compare(ordered[i].data, ordered[j].data) < 0
	})
	for i, item := range ordered {
		items[i] = item.item
	}
	return nil
}
