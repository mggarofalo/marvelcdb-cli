package api

import (
	"encoding/json"
	"fmt"
)

// MarvelCDB adds fields to its JSON payloads without warning, and the CLI
// promises that `-o json` reproduces what the server sent. Every model in this
// package therefore round-trips whatever its Go fields cannot represent through
// an Extra map instead of dropping it on the floor.
//
// That covers three cases: fields this build has never heard of, fields
// MarvelCDB sends as an explicit null, and fields it sends as an empty string.
// The last two matter because MarvelCDB is inconsistent about them — a nested
// linked_card carries "attack": null where a top-level card omits the key
// entirely — and only replaying the original form reproduces both.

// extraFields returns the members of the JSON object in data that marshalling
// decoded would not put back.
func extraFields(data []byte, decoded any) (map[string]json.RawMessage, error) {
	var original map[string]json.RawMessage
	if err := json.Unmarshal(data, &original); err != nil {
		return nil, fmt.Errorf("decode object: %w", err)
	}

	roundTripped, err := json.Marshal(decoded)
	if err != nil {
		return nil, fmt.Errorf("re-encode for comparison: %w", err)
	}
	var reproduced map[string]json.RawMessage
	if err := json.Unmarshal(roundTripped, &reproduced); err != nil {
		return nil, fmt.Errorf("decode re-encoded object: %w", err)
	}

	for name := range reproduced {
		delete(original, name)
	}
	if len(original) == 0 {
		return nil, nil
	}
	return original, nil
}

// mergeExtras marshals v and folds extras back into the resulting object.
// v must marshal to a JSON object and must not itself carry an Extra field
// that would be serialised (tag it `json:"-"`).
func mergeExtras(v any, extras map[string]json.RawMessage) ([]byte, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if len(extras) == 0 {
		return body, nil
	}
	var merged map[string]json.RawMessage
	if err := json.Unmarshal(body, &merged); err != nil {
		return nil, fmt.Errorf("re-decode for merge: %w", err)
	}
	if merged == nil {
		merged = make(map[string]json.RawMessage, len(extras))
	}
	for k, v := range extras {
		if _, taken := merged[k]; !taken {
			merged[k] = v
		}
	}
	return json.Marshal(merged)
}
