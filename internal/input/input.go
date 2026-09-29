// Package input builds request bodies from --data and --set flags.
package input

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"

	"github.com/SeventhingsCompany/customer-api-cli/internal/exitcode"
)

// Body builds a JSON object body.
//
// --data accepts inline JSON, @path to read a file, or - to read stdin.
// --set is repeatable: key=value sets a string, key:=json sets a raw JSON
// value (numbers, booleans, null, arrays, objects). --set wins over --data.
func Body(data string, sets []string, stdin io.Reader) (map[string]any, error) {
	body := map[string]any{}
	if data != "" {
		raw, err := Raw(data, stdin)
		if err != nil {
			return nil, err
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&body); err != nil {
			return nil, exitcode.Usagef("--data must be a JSON object: %v", err)
		}
	}
	for _, s := range sets {
		key, val, err := parseSet(s)
		if err != nil {
			return nil, err
		}
		body[key] = val
	}
	return body, nil
}

// Raw returns the bytes named by a --data value.
func Raw(data string, stdin io.Reader) ([]byte, error) {
	switch {
	case data == "-":
		b, err := io.ReadAll(stdin)
		if err != nil {
			return nil, fmt.Errorf("read stdin: %w", err)
		}
		return b, nil
	case strings.HasPrefix(data, "@"):
		b, err := os.ReadFile(data[1:])
		if err != nil {
			return nil, exitcode.Usagef("read --data file: %v", err)
		}
		return b, nil
	}
	return []byte(data), nil
}

// Into decodes a --data value into a typed request struct, rejecting
// unknown fields so typos fail loudly instead of being dropped.
func Into(data string, sets []string, stdin io.Reader, dest any) error {
	body, err := Body(data, sets, stdin)
	if err != nil {
		return err
	}
	return Decode(body, dest)
}

// Decode converts a body map into a typed request struct, rejecting unknown
// fields.
func Decode(body map[string]any, dest any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dest); err != nil {
		return exitcode.Usagef("invalid input: %v", err)
	}
	return nil
}

func parseSet(s string) (string, any, error) {
	if k, v, ok := strings.Cut(s, ":="); ok && k != "" && !strings.Contains(k, "=") {
		dec := json.NewDecoder(strings.NewReader(v))
		dec.UseNumber()
		var val any
		if err := dec.Decode(&val); err != nil {
			return "", nil, exitcode.Usagef("--set %s: value after := must be JSON: %v", k, err)
		}
		return k, val, nil
	}
	k, v, ok := strings.Cut(s, "=")
	if !ok || k == "" {
		return "", nil, exitcode.Usagef("--set %q: want key=value or key:=json", s)
	}
	return k, v, nil
}

// Merge prepares a full-replacement (PUT) body: the current record,
// overlaid with the user's fields, decoded into dest. Fields the user did not
// give keep their current values. User keys are checked strictly against
// dest so typos fail instead of being dropped. fixup adapts read-shaped
// values to write shapes (e.g. attachment objects → UUIDs).
func Merge(current any, body map[string]any, dest any, fixup func(map[string]any)) error {
	if err := strictCheck(body, dest); err != nil {
		return err
	}
	b, err := json.Marshal(current)
	if err != nil {
		return err
	}
	base := map[string]any{}
	if err := json.Unmarshal(b, &base); err != nil {
		return err
	}
	if fixup != nil {
		fixup(base)
	}
	for k, v := range body {
		base[k] = v
	}
	merged, err := json.Marshal(base)
	if err != nil {
		return err
	}
	return json.Unmarshal(merged, dest)
}

func strictCheck(body map[string]any, dest any) error {
	return Decode(body, reflect.New(reflect.TypeOf(dest).Elem()).Interface())
}

// AttachmentUUIDs rewrites attachments from file objects to UUID strings.
func AttachmentUUIDs(m map[string]any) {
	list, ok := m["attachments"].([]any)
	if !ok {
		return
	}
	out := make([]any, 0, len(list))
	for _, item := range list {
		if f, ok := item.(map[string]any); ok {
			out = append(out, f["uuid"])
		} else {
			out = append(out, item)
		}
	}
	m["attachments"] = out
}
