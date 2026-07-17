package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// decodeFields parses a config payload as a flat JSON object of field name
// to raw value, matching the dotted-key shape MEG-015 §08 shows
// (`storage.postgres.password = secret://...`). An empty payload decodes to
// no fields, so a fresh install activating its first version has nothing to
// diff against.
func decodeFields(payload []byte) (map[string]json.RawMessage, error) {
	if len(payload) == 0 {
		return map[string]json.RawMessage{}, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return nil, fmt.Errorf("config: decode payload: %w", err)
	}
	return fields, nil
}

// ChangedFields returns the sorted set of field names whose value differs
// between previous and candidate, including fields present in only one of
// the two payloads. previous may be empty (no prior active version).
func ChangedFields(previous, candidate []byte) ([]string, error) {
	prevFields, err := decodeFields(previous)
	if err != nil {
		return nil, err
	}
	candFields, err := decodeFields(candidate)
	if err != nil {
		return nil, err
	}

	changed := make(map[string]struct{})
	for name, value := range candFields {
		if prevValue, ok := prevFields[name]; !ok || !bytes.Equal(prevValue, value) {
			changed[name] = struct{}{}
		}
	}
	for name := range prevFields {
		if _, ok := candFields[name]; !ok {
			changed[name] = struct{}{}
		}
	}

	names := make([]string, 0, len(changed))
	for name := range changed {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// FieldNames returns the sorted set of field names present in payload.
// Validation uses this to check every declared field is registered in the
// Schema before a Draft may move to Validated.
func FieldNames(payload []byte) ([]string, error) {
	fields, err := decodeFields(payload)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// FieldValue decodes field's raw JSON value from payload as a string. A
// Secret field's value must decode this way (MEG-015 §08 — Secret
// References store a secret:// reference string, never a raw value or a
// nested structure), so Validate uses this to check it.
func FieldValue(payload []byte, field string) (string, error) {
	fields, err := decodeFields(payload)
	if err != nil {
		return "", err
	}
	raw, ok := fields[field]
	if !ok {
		return "", fmt.Errorf("config: field %q not present", field)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("config: field %q is not a string: %w", field, err)
	}
	return value, nil
}
