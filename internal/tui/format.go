package tui

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Nested values (attachments, references, reminders, hub data) are shown as
// readable text instead of raw JSON: summary gives one line for table cells
// and history, expand gives indented sub-fields for the detail view.

// labelKeys name a nested record, in order of preference.
var labelKeys = []string{"inventory_name", "name", "title", "display_name", "order_number"}

// hiddenKeys only locate a file's data; they add nothing for the reader.
var hiddenKeys = []string{"data_uri", "file_uri", "thumbnail_uri"}

// decode unpacks JSON stored as a string (hub data nests encoded lists).
func decode(v any) any {
	s, ok := v.(string)
	if !ok {
		return v
	}
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "[") && !strings.HasPrefix(s, "{") || !json.Valid([]byte(s)) {
		return v
	}
	var out any
	if json.Unmarshal([]byte(s), &out) != nil {
		return v
	}
	return out
}

func isEmpty(v any) bool {
	switch t := decode(v).(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(t) == ""
	case []any:
		return len(t) == 0
	case map[string]any:
		return len(t) == 0
	}
	return false
}

// isInterval matches {"unit": "days", "value": 1}.
func isInterval(m map[string]any) bool {
	_, u := m["unit"].(string)
	_, v := m["value"].(float64)
	return len(m) == 2 && u && v
}

func intervalText(m map[string]any) string {
	unit := str(m["unit"])
	if m["value"] == 1.0 {
		unit = strings.TrimSuffix(unit, "s")
	}
	return str(m["value"]) + " " + unit
}

// isFile tells attachments apart from other records, such as references,
// whose "type" is a record kind rather than a MIME type.
func isFile(m map[string]any) bool {
	return str(m["uuid"]) != "" && m["name"] != nil && (m["size"] != nil || strings.Contains(str(m["type"]), "/"))
}

func label(m map[string]any) (key, val string) {
	for _, k := range labelKeys {
		if s := str(m[k]); s != "" {
			return k, oneLine(s)
		}
	}
	return "", ""
}

func isScalar(v any) bool {
	switch v.(type) {
	case map[string]any, []any:
		return false
	}
	return true
}

// inline reports whether a value fits on one line in the detail view.
func inline(v any) bool {
	switch t := v.(type) {
	case map[string]any:
		return isInterval(t)
	case []any:
		return !slices.ContainsFunc(t, func(e any) bool { return !isScalar(e) })
	}
	return true
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// summary renders a value on one line.
func summary(v any) string {
	switch t := decode(v).(type) {
	case map[string]any:
		if isInterval(t) {
			return intervalText(t)
		}
		if _, l := label(t); l != "" {
			return l
		}
		var parts []string
		for _, k := range sortedKeys(t) {
			if !isEmpty(t[k]) && !slices.Contains(hiddenKeys, k) {
				parts = append(parts, k+": "+summary(t[k]))
			}
		}
		return strings.Join(parts, ", ")
	case []any:
		parts := make([]string, 0, len(t))
		for _, e := range t {
			if s := summary(e); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ", ")
	case string:
		return oneLine(t)
	default:
		return str(t)
	}
}

// expand renders a nested value as indented lines (without the field key).
func expand(v any) []string {
	var out []string
	expandInto(&out, decode(v), "")
	return out
}

func expandInto(out *[]string, v any, ind string) {
	switch t := v.(type) {
	case map[string]any:
		if isInterval(t) {
			*out = append(*out, ind+intervalText(t))
			return
		}
		expandMap(out, t, ind)
	case []any:
		if inline(t) {
			*out = append(*out, ind+summary(t))
			return
		}
		for i, e := range t {
			m, ok := decode(e).(map[string]any)
			if !ok || isInterval(m) {
				*out = append(*out, ind+"• "+summary(e))
				continue
			}
			expandRecord(out, m, i, ind)
		}
	default:
		*out = append(*out, ind+summary(t))
	}
}

// expandRecord renders one list element: a bullet with its label and scalar
// fields, then its nested fields indented below.
func expandRecord(out *[]string, m map[string]any, i int, ind string) {
	lk, head := label(m)
	if head == "" {
		head = fmt.Sprintf("#%d", i+1)
	}
	file := isFile(m)
	var extras []string
	nested := map[string]any{}
	if file {
		extras = append(extras, str(m["type"]))
		if n, ok := m["size"].(float64); ok {
			extras = append(extras, humanSize(n))
		}
	}
	for _, k := range sortedKeys(m) {
		val := decode(m[k])
		switch {
		case k == lk || slices.Contains(hiddenKeys, k) || isEmpty(val):
		case file && (k == "uuid" || k == "type" || k == "size"):
		case inline(val):
			extras = append(extras, k+" "+summary(val))
		default:
			nested[k] = val
		}
	}
	extras = slices.DeleteFunc(extras, func(s string) bool { return s == "" })
	line := ind + "• " + head
	if len(extras) > 0 {
		line += "  " + dimStyle.Render(strings.Join(extras, " · "))
	}
	*out = append(*out, line)
	if len(nested) > 0 {
		expandMap(out, nested, ind+"  ")
	}
}

// expandMap renders sub-fields as aligned key/value lines.
func expandMap(out *[]string, m map[string]any, ind string) {
	var keys []string
	pad := 0
	for _, k := range sortedKeys(m) {
		if !isEmpty(m[k]) && !slices.Contains(hiddenKeys, k) {
			keys = append(keys, k)
			pad = max(pad, len(k))
		}
	}
	pad = min(pad, 32)
	for _, k := range keys {
		val := decode(m[k])
		key := dimStyle.Render(fmt.Sprintf("%-*s", pad, truncate(k, pad)))
		if inline(val) {
			*out = append(*out, ind+key+"  "+summary(val))
			continue
		}
		*out = append(*out, ind+dimStyle.Render(k))
		expandInto(out, val, ind+"  ")
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func humanSize(n float64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%.0f B", n)
	}
	i := 0
	for n >= unit && i < 3 {
		n /= unit
		i++
	}
	return fmt.Sprintf("%.1f %s", n, []string{"B", "KB", "MB", "GB"}[i])
}
