// Package output renders command results as json, ndjson, yaml or a table,
// with optional --fields projection and --jq filtering.
package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/itchyny/gojq"
	"gopkg.in/yaml.v3"
)

// Format is an output format.
type Format string

const (
	JSON   Format = "json"
	NDJSON Format = "ndjson"
	YAML   Format = "yaml"
	Table  Format = "table"
)

// Formats lists the valid --output values.
var Formats = []Format{JSON, NDJSON, YAML, Table}

// ParseFormat validates s.
func ParseFormat(s string) (Format, error) {
	if slices.Contains(Formats, Format(s)) {
		return Format(s), nil
	}
	return "", fmt.Errorf("invalid output format %q (want json, ndjson, yaml or table)", s)
}

// maxAutoColumns caps table width when --fields is not given.
const maxAutoColumns = 8

// preferredColumns are shown first in tables when present.
var preferredColumns = []string{"uuid", "asset_uuid", "id", "name", "inventory_name", "title", "barcode", "status", "email", "type"}

// Printer writes results.
type Printer struct {
	Out    io.Writer
	Format Format
	Fields []string
	// Raw prints string results without JSON quotes (like jq -r).
	Raw bool
	jq  *gojq.Code
}

// New builds a Printer. jqExpr may be empty.
func New(out io.Writer, format Format, fields []string, jqExpr string) (*Printer, error) {
	p := &Printer{Out: out, Format: format, Fields: fields}
	if jqExpr != "" {
		q, err := gojq.Parse(jqExpr)
		if err != nil {
			return nil, fmt.Errorf("invalid --jq expression: %w", err)
		}
		if p.jq, err = gojq.Compile(q); err != nil {
			return nil, fmt.Errorf("invalid --jq expression: %w", err)
		}
	}
	return p, nil
}

// Print renders v.
func (p *Printer) Print(v any) error {
	values, err := p.transform(v)
	if err != nil {
		return err
	}
	if p.jq != nil && p.Format != Table {
		// jq may emit zero or more values; print each as a JSON document.
		for _, val := range values {
			if err := p.render(val, p.Format); err != nil {
				return err
			}
		}
		return nil
	}
	if len(values) == 0 {
		return nil
	}
	return p.render(values[0], p.Format)
}

// Stream renders items one at a time as they arrive (for --all). Table and
// yaml output are buffered, since they need the whole set.
type Stream struct {
	p     *Printer
	items []any
}

// Stream starts a stream.
func (p *Printer) Stream() *Stream { return &Stream{p: p} }

// Add emits or buffers one item.
func (s *Stream) Add(item any) error {
	if s.p.Format == NDJSON || s.p.Format == JSON {
		values, err := s.p.transform(item)
		if err != nil {
			return err
		}
		for _, v := range values {
			if err := s.p.render(v, NDJSON); err != nil {
				return err
			}
		}
		return nil
	}
	s.items = append(s.items, item)
	return nil
}

// Close flushes buffered items.
func (s *Stream) Close() error {
	if s.p.Format == NDJSON || s.p.Format == JSON {
		return nil
	}
	if s.items == nil {
		s.items = []any{}
	}
	return s.p.Print(s.items)
}

// transform normalizes v to plain JSON values, then applies --fields and --jq.
func (p *Printer) transform(v any) ([]any, error) {
	norm, err := normalize(v)
	if err != nil {
		return nil, err
	}
	if len(p.Fields) > 0 {
		norm = project(norm, p.Fields)
	}
	if p.jq == nil {
		return []any{norm}, nil
	}
	var out []any
	iter := p.jq.Run(norm)
	for {
		val, ok := iter.Next()
		if !ok {
			break
		}
		if err, ok := val.(error); ok {
			return nil, fmt.Errorf("jq: %w", err)
		}
		out = append(out, val)
	}
	return out, nil // may be empty: jq selected nothing
}

// normalize round-trips v through JSON so structs, typed maps and slices all
// become map[string]any / []any / scalars.
func normalize(v any) (any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func project(v any, fields []string) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(fields))
		for _, f := range fields {
			if val, ok := t[f]; ok {
				out[f] = val
			}
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			out[i] = project(item, fields)
		}
		return out
	}
	return v
}

func (p *Printer) render(v any, format Format) error {
	if s, ok := v.(string); ok && p.Raw && format != Table {
		_, err := fmt.Fprintln(p.Out, s)
		return err
	}
	switch format {
	case NDJSON:
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(p.Out, "%s\n", b)
		return err
	case YAML:
		enc := yaml.NewEncoder(p.Out)
		enc.SetIndent(2)
		if err := enc.Encode(yamlSafe(v)); err != nil {
			return err
		}
		return enc.Close()
	case Table:
		return p.table(v)
	default:
		enc := json.NewEncoder(p.Out)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(v)
	}
}

// yamlSafe converts json.Number, which yaml.v3 would quote, to int/float.
func yamlSafe(v any) any {
	switch t := v.(type) {
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return i
		}
		f, _ := t.Float64()
		return f
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = yamlSafe(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = yamlSafe(val)
		}
		return out
	}
	return v
}

func (p *Printer) table(v any) error {
	tw := tabwriter.NewWriter(p.Out, 0, 0, 2, ' ', 0)
	switch t := v.(type) {
	case []any:
		if len(t) == 0 {
			_, err := fmt.Fprintln(p.Out, "(no results)")
			return err
		}
		if _, ok := t[0].(map[string]any); !ok {
			for _, item := range t {
				_, _ = fmt.Fprintln(tw, cell(item))
			}
			return tw.Flush()
		}
		cols := p.columns(t)
		_, _ = fmt.Fprintln(tw, strings.ToUpper(strings.Join(cols, "\t")))
		for _, item := range t {
			m, _ := item.(map[string]any)
			row := make([]string, len(cols))
			for i, c := range cols {
				row[i] = cell(m[c])
			}
			_, _ = fmt.Fprintln(tw, strings.Join(row, "\t"))
		}
	case map[string]any:
		keys := orderedKeys(t)
		for _, k := range keys {
			_, _ = fmt.Fprintf(tw, "%s\t%s\n", k, cell(t[k]))
		}
	default:
		_, _ = fmt.Fprintln(tw, cell(t))
	}
	return tw.Flush()
}

func (p *Printer) columns(rows []any) []string {
	if len(p.Fields) > 0 {
		return p.Fields
	}
	seen := map[string]bool{}
	for _, r := range rows {
		if m, ok := r.(map[string]any); ok {
			for k := range m {
				seen[k] = true
			}
		}
	}
	all := make(map[string]any, len(seen))
	for k := range seen {
		all[k] = nil
	}
	cols := orderedKeys(all)
	if len(cols) > maxAutoColumns {
		cols = cols[:maxAutoColumns]
	}
	return cols
}

func orderedKeys(m map[string]any) []string {
	var keys, rest []string
	for _, k := range preferredColumns {
		if _, ok := m[k]; ok {
			keys = append(keys, k)
		}
	}
	for k := range m {
		if !slices.Contains(preferredColumns, k) {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	return append(keys, rest...)
}

func cell(v any) string {
	switch t := v.(type) {
	case nil:
		return "-"
	case string:
		s := strings.ReplaceAll(t, "\n", " ")
		if len(s) > 60 {
			s = s[:57] + "..."
		}
		return s
	case map[string]any, []any:
		b, _ := json.Marshal(t)
		s := string(b)
		if len(s) > 60 {
			s = s[:57] + "..."
		}
		return s
	}
	return fmt.Sprint(v)
}
