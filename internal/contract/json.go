package contract

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"unicode/utf8"
)

// hashBytes returns the lowercase SHA-256 digest of exact bytes.
func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// decode rejects ambiguous keys, non-integer numbers, and excessive nesting.
func decode(data []byte) (any, error) {
	if !utf8.Valid(data) {
		return nil, errors.New("JSON must be UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := readValue(decoder, 0)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("expected one JSON value")
	}
	return value, nil
}

func readValue(d *json.Decoder, depth int) (any, error) {
	if depth > 32 {
		return nil, errors.New("JSON nesting exceeds 32 levels")
	}
	token, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch token {
	case json.Delim('{'):
		object := make(map[string]any)
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return nil, err
			}
			name, ok := key.(string)
			if !ok {
				return nil, errors.New("invalid object key")
			}
			if _, exists := object[name]; exists {
				return nil, errors.New("duplicate object key")
			}
			value, err := readValue(d, depth+1)
			if err != nil {
				return nil, err
			}
			object[name] = value
		}
		if _, err := d.Token(); err != nil {
			return nil, err
		}
		return object, nil
	case json.Delim('['):
		array := make([]any, 0)
		for d.More() {
			value, err := readValue(d, depth+1)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		if _, err := d.Token(); err != nil {
			return nil, err
		}
		return array, nil
	}
	if n, ok := token.(json.Number); ok {
		value, err := strconv.ParseInt(string(n), 10, 64)
		if err != nil || value < -9007199254740991 || value > 9007199254740991 {
			return nil, errors.New("numbers must be decimal integers within the exact JSON integer range")
		}
		return json.Number(strconv.FormatInt(value, 10)), nil
	}
	if _, ok := token.(json.Delim); ok {
		return nil, errors.New("unexpected JSON delimiter")
	}
	return token, nil
}

func contentHash(raw json.RawMessage) (string, error) {
	value, err := decode(raw)
	if err != nil {
		return "", err
	}
	// Input schemas permit only ASCII field names and enumerated string values.
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode hash input: %w", err)
	}
	return hashBytes(canonical), nil
}
