package output

import (
	"bytes"
	"strings"
	"testing"
)

type person struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
	Age  int    `json:"age"`
}

func render(t *testing.T, format Format, fields []string, jq string, v any) string {
	t.Helper()
	var buf bytes.Buffer
	p, err := New(&buf, format, fields, jq)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Print(v); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestFormats(t *testing.T) {
	items := []person{{"u1", "Ada", 36}, {"u2", "Linus", 54}}

	if got := render(t, JSON, nil, "", items[0]); got != "{\n  \"age\": 36,\n  \"name\": \"Ada\",\n  \"uuid\": \"u1\"\n}\n" {
		t.Errorf("json:\n%s", got)
	}
	if got := render(t, NDJSON, nil, "", items[0]); got != `{"age":36,"name":"Ada","uuid":"u1"}`+"\n" {
		t.Errorf("ndjson:\n%s", got)
	}
	if got := render(t, YAML, nil, "", items[0]); got != "age: 36\nname: Ada\nuuid: u1\n" {
		t.Errorf("yaml:\n%s", got)
	}
	want := "UUID  NAME   AGE\nu1    Ada    36\nu2    Linus  54\n"
	if got := render(t, Table, nil, "", items); got != want {
		t.Errorf("table:\n%q\nwant\n%q", got, want)
	}
}

func TestFieldsAndJQ(t *testing.T) {
	items := []person{{"u1", "Ada", 36}, {"u2", "Linus", 54}}
	if got := render(t, NDJSON, []string{"name"}, "", items); got != `[{"name":"Ada"},{"name":"Linus"}]`+"\n" {
		t.Errorf("fields: %s", got)
	}
	if got := render(t, NDJSON, nil, ".[].name", items); got != "\"Ada\"\n\"Linus\"\n" {
		t.Errorf("jq: %s", got)
	}
	if got := render(t, Table, []string{"name", "age"}, "", items); !strings.HasPrefix(got, "NAME   AGE\n") {
		t.Errorf("table fields: %q", got)
	}
}

func TestStream(t *testing.T) {
	var buf bytes.Buffer
	p, _ := New(&buf, JSON, nil, "")
	s := p.Stream()
	_ = s.Add(person{"u1", "Ada", 36})
	_ = s.Add(person{"u2", "Linus", 54})
	_ = s.Close()
	if got := buf.String(); strings.Count(got, "\n") != 2 || !strings.HasPrefix(got, `{"age":36`) {
		t.Errorf("stream json should be ndjson: %q", got)
	}

	buf.Reset()
	p, _ = New(&buf, Table, nil, "")
	s = p.Stream()
	_ = s.Close()
	if buf.String() != "(no results)\n" {
		t.Errorf("empty table: %q", buf.String())
	}
}

func TestLargeNumbersPreserved(t *testing.T) {
	if got := render(t, NDJSON, nil, "", map[string]any{"id": 12345678901234567}); got != `{"id":12345678901234567}`+"\n" {
		t.Errorf("got %s", got)
	}
}

func TestJQNumbers(t *testing.T) {
	items := []person{{"u1", "Ada", 36}, {"u2", "Linus", 54}}
	if got := render(t, NDJSON, nil, "map(.age + 1) | add", items); got != "92\n" {
		t.Errorf("jq numbers: %q", got)
	}
	if got := render(t, NDJSON, nil, "map(select(.age > 40)) | length", items); got != "1\n" {
		t.Errorf("jq compare: %q", got)
	}
}

func TestJQSelectNothing(t *testing.T) {
	var buf bytes.Buffer
	p, _ := New(&buf, NDJSON, nil, "select(.age > 100)")
	s := p.Stream()
	_ = s.Add(person{"u1", "Ada", 36})
	_ = s.Close()
	if got := render(t, NDJSON, nil, "empty", person{"u1", "Ada", 36}); got != "" || buf.String() != "" {
		t.Errorf("want no output, got %q / %q", got, buf.String())
	}
}
