package graph

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
)

// Result is a decoded HydraDB query response.
type Result struct {
	QueryID    string
	Columns    []string
	Rows       [][]any // decoded Go values (see CellValue)
	ReadEpoch  *uint64
	NextCursor *uint64
	Bookmark   string
}

// Count returns the number of rows.
func (r *Result) Count() int { return len(r.Rows) }

// Value returns the decoded value at (row, column index) if in range.
func (r *Result) Value(row, col int) any {
	if row < 0 || row >= len(r.Rows) || col < 0 || col >= len(r.Rows[row]) {
		return nil
	}
	return r.Rows[row][col]
}

// Int64 extracts an integer-valued cell, tolerating int/int64/uint64/float64.
func (r *Result) Int64(row, col int) (int64, error) {
	switch v := r.Value(row, col).(type) {
	case int64:
		return v, nil
	case uint64:
		return int64(v), nil
	case int:
		return int64(v), nil
	case float64:
		return int64(v), nil
	default:
		return 0, fmt.Errorf("column %d row %d: not an integer value (%T)", col, row, r.Value(row, col))
	}
}

// String extracts a string-valued cell.
func (r *Result) String(row, col int) (string, error) {
	v, ok := r.Value(row, col).(string)
	if !ok {
		return "", fmt.Errorf("column %d row %d: not a string value (%T)", col, row, r.Value(row, col))
	}
	return v, nil
}

// httpQueryValue mirrors the verified response cell envelope:
// {"type":"vertex_id"|"integer"|"signed_integer"|"float"|"boolean"|"string"|"list"|"null","value":...}
// (src/client/http.rs HttpQueryValue, verified against build 0.1.0+916637477f3f).
type httpQueryValue struct {
	Type  string          `json:"type"`
	Value json.RawMessage `json:"value"`
}

// decodeCell converts one typed cell envelope to a Go value.
// vertex_id -> uint64-rendered int64 (HydraDB node ids are non-negative
// integers), integer -> int64, signed_integer -> int64, float -> float64,
// boolean -> bool, string -> string, list -> []any, null -> nil.
func decodeCell(b []byte) (any, error) {
	if string(b) == "null" {
		return nil, nil
	}
	var env httpQueryValue
	if err := json.Unmarshal(b, &env); err != nil {
		// Tolerate a bare scalar without the envelope (defensive; not observed).
		var bare any
		if err2 := json.Unmarshal(b, &bare); err2 == nil {
			return bare, nil
		}
		return nil, fmt.Errorf("decode cell: %w", err)
	}
	switch env.Type {
	case "null":
		return nil, nil
	case "vertex_id", "integer", "signed_integer":
		// u64/i64 arrive as JSON numbers; decode through string to keep
		// precision and sign handling uniform.
		s := stringsBare(env.Value)
		if s == "" {
			var f float64
			if err := json.Unmarshal(env.Value, &f); err == nil {
				return int64(f), nil
			}
			return nil, fmt.Errorf("decode integer cell: unexpected %s", string(env.Value))
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			var f float64
			if err2 := json.Unmarshal(env.Value, &f); err2 == nil {
				return int64(f), nil
			}
			return nil, fmt.Errorf("decode integer cell: %w", err)
		}
		return n, nil
	case "float":
		var f float64
		if err := json.Unmarshal(env.Value, &f); err != nil {
			return nil, fmt.Errorf("decode float cell: %w", err)
		}
		return f, nil
	case "boolean":
		var v bool
		if err := json.Unmarshal(env.Value, &v); err != nil {
			return nil, fmt.Errorf("decode boolean cell: %w", err)
		}
		return v, nil
	case "string":
		var s string
		if err := json.Unmarshal(env.Value, &s); err != nil {
			return nil, fmt.Errorf("decode string cell: %w", err)
		}
		return s, nil
	case "list":
		var raw []json.RawMessage
		if err := json.Unmarshal(env.Value, &raw); err != nil {
			return nil, fmt.Errorf("decode list cell: %w", err)
		}
		out := make([]any, 0, len(raw))
		for _, el := range raw {
			v, err := decodeCell(el)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case "path":
		// Native path procedures return a structured path envelope. Keep its
		// node/relationship payload intact for bounded analytical responses;
		// callers must not treat it as a writable graph object.
		path, err := decodePathValue(env.Value)
		if err != nil {
			return nil, fmt.Errorf("decode path cell: %w", err)
		}
		return path, nil
	default:
		return nil, fmt.Errorf("decode cell: unknown type %q", env.Type)
	}
}

// decodePathValue preserves HydraDB's large vertex and relationship IDs. A
// generic interface{} JSON decode would turn those values into float64 and
// lose precision above 2^53. Path property envelopes also carry explicit
// Integer/Float/Bool/String keys, which are decoded to their scalar values.
func decodePathValue(raw []byte) (any, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return normalizePathJSON(value, ""), nil
}

func normalizePathJSON(value any, key string) any {
	switch v := value.(type) {
	case json.Number:
		if key == "id" || key == "src" || key == "dst" || key == "Integer" {
			if n, err := v.Int64(); err == nil {
				return n
			}
		}
		if f, err := v.Float64(); err == nil {
			return f
		}
		return v.String()
	case []any:
		out := make([]any, len(v))
		for i := range v {
			out[i] = normalizePathJSON(v[i], "")
		}
		return out
	case map[string]any:
		// Typed property envelopes in native path responses are represented as
		// {"Integer": 1}, {"String": "x"}, etc. Flatten them to the same
		// scalar convention used by graph.Result for ordinary cells.
		if len(v) == 1 {
			for typeName, scalar := range v {
				if typeName == "Integer" || typeName == "Float" || typeName == "Bool" || typeName == "String" {
					return normalizePathJSON(scalar, typeName)
				}
			}
		}
		out := make(map[string]any, len(v))
		for childKey, child := range v {
			out[childKey] = normalizePathJSON(child, childKey)
		}
		return out
	default:
		return value
	}
}

// stringsBare returns the raw JSON as an unquoted plain string when the value
// is a bare number token (integer cells are emitted unquoted), else "".
func stringsBare(b []byte) string {
	s := string(b)
	if s == "" {
		return ""
	}
	// Quoted strings are not bare numbers.
	if s[0] == '"' {
		return ""
	}
	// Reject tokens that are not integer-ish (e.g. "null", "true").
	for _, r := range s {
		if (r < '0' || r > '9') && r != '-' {
			return ""
		}
	}
	return s
}
