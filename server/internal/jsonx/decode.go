// Package jsonx holds the strict JSON codec used at every trust boundary:
// unknown fields, duplicate keys and trailing data are rejected. It lives
// below api/config so pure packages can use it without importing wire types.
package jsonx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// UnknownFieldError reports a JSON object key not present in the target type.
type UnknownFieldError struct{ Field string }

func (e *UnknownFieldError) Error() string {
	return fmt.Sprintf("unknown field %q", e.Field)
}

// DuplicateKeyError reports a duplicated JSON object key, which encoding/json
// would otherwise silently fold.
type DuplicateKeyError struct{ Field string }

func (e *DuplicateKeyError) Error() string {
	return fmt.Sprintf("duplicate JSON key %q", e.Field)
}

var errTrailingData = errors.New("unexpected data after JSON value")

// DecodeStrict decodes exactly one JSON value into v with unknown-field and
// duplicate-key rejection.
func DecodeStrict(data []byte, v any) error {
	if err := rejectDuplicateKeys(data); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var ute *json.UnmarshalTypeError
		if errors.As(err, &ute) {
			return fmt.Errorf("field %s: %w", ute.Field, err)
		}
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return errTrailingData
	}
	return nil
}

// rejectDuplicateKeys walks the raw JSON and rejects any repeated object key.
func rejectDuplicateKeys(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	return walkValue(dec, tok)
}

func walkValue(dec *json.Decoder, tok json.Token) error {
	switch v := tok.(type) {
	case json.Delim:
		switch v {
		case '{':
			seen := map[string]bool{}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return err
				}
				key, _ := kt.(string)
				if seen[key] {
					return &DuplicateKeyError{Field: key}
				}
				seen[key] = true
				if err := walkDup(dec); err != nil {
					return err
				}
			}
			_, err := dec.Token() // consume closing '}'
			return err
		case '[':
			for dec.More() {
				t, err := dec.Token()
				if err != nil {
					return err
				}
				if err := walkValue(dec, t); err != nil {
					return err
				}
			}
			_, err := dec.Token() // consume closing ']'
			return err
		}
	}
	return nil
}

// walkDup reads one value token after an object key and recurses.
func walkDup(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	return walkValue(dec, tok)
}

// EncodeCanonical marshals v with sorted map keys and no HTML escaping drift.
func EncodeCanonical(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
