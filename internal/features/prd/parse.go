package prd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// ParseEnvelope decodes a BatchPRDEnvelope from r using strict JSON decoding.
// Unknown fields are rejected. Syntax and type errors are wrapped with
// line/column/offset information when available so callers can surface precise
// diagnostics.
func ParseEnvelope(r io.Reader) (BatchPRDEnvelope, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()

	var env BatchPRDEnvelope
	if err := dec.Decode(&env); err != nil {
		return BatchPRDEnvelope{}, wrapDecodeError(err)
	}
	return env, nil
}

// ParseFile decodes a per-plan prd.json from the file at path using strict
// JSON decoding. Unknown fields are rejected. Used by the runner to load a
// plan's PRD after it has been written to .springfield/plans/<id>/prd.json.
func ParseFile(path string) (PRD, error) {
	f, err := os.Open(path)
	if err != nil {
		return PRD{}, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()

	var plan PRD
	if err := dec.Decode(&plan); err != nil {
		return PRD{}, fmt.Errorf("%s: %w", path, wrapDecodeError(err))
	}
	return plan, nil
}

// wrapDecodeError enriches JSON decode errors with positional context.
// *json.SyntaxError carries an Offset; *json.UnmarshalTypeError carries
// Offset plus field information. We wrap rather than replace so callers can
// still use errors.As to inspect the underlying type.
func wrapDecodeError(err error) error {
	switch e := err.(type) {
	case *json.SyntaxError:
		return fmt.Errorf("json syntax error at offset %d: %w", e.Offset, err)
	case *json.UnmarshalTypeError:
		return fmt.Errorf("json type error at offset %d (field %q): %w", e.Offset, e.Field, err)
	default:
		return err
	}
}
