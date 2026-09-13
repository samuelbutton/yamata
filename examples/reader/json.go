package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"unicode/utf8"
)

type object = map[string]any

func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func hashObject(value any) (string, error) {
	data, err := json.Marshal(value)
	return digest(data), err
}
func text(o object, key string) string   { s, _ := o[key].(string); return s }
func integer(o object, key string) int64 { n, _ := o[key].(json.Number); v, _ := n.Int64(); return v }
func nested(o object, key string) object { v, _ := o[key].(map[string]any); return v }

func decode(data []byte) (object, error) {
	if len(data) > 1<<20 || !utf8.Valid(data) {
		return nil, errors.New("JSON exceeds 1 MiB or is not UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	value, err := jsonValue(d, 0)
	if err != nil {
		return nil, err
	}
	if _, err := d.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("expected one JSON value")
	}
	o, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("expected JSON object")
	}
	return o, nil
}

func jsonValue(d *json.Decoder, depth int) (any, error) {
	if depth > 32 {
		return nil, errors.New("JSON exceeds 32 nesting levels")
	}
	token, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch token {
	case json.Delim('{'):
		out := object{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return nil, err
			}
			name, ok := key.(string)
			if !ok {
				return nil, errors.New("invalid JSON key")
			}
			if _, ok := out[name]; ok {
				return nil, errors.New("duplicate JSON key")
			}
			value, err := jsonValue(d, depth+1)
			if err != nil {
				return nil, err
			}
			out[name] = value
		}
		_, err = d.Token()
		return out, err
	case json.Delim('['):
		out := []any{}
		for d.More() {
			value, err := jsonValue(d, depth+1)
			if err != nil {
				return nil, err
			}
			out = append(out, value)
		}
		_, err = d.Token()
		return out, err
	}
	if n, ok := token.(json.Number); ok {
		v, err := strconv.ParseInt(string(n), 10, 64)
		if err != nil || v < -9007199254740991 || v > 9007199254740991 {
			return nil, errors.New("expected an exact decimal integer")
		}
		return json.Number(strconv.FormatInt(v, 10)), nil
	}
	if _, ok := token.(json.Delim); ok {
		return nil, errors.New("unexpected JSON delimiter")
	}
	return token, nil
}
