package tui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func item(t *testing.T, s string) Item {
	t.Helper()
	var it Item
	if err := json.Unmarshal([]byte(s), &it); err != nil {
		t.Fatal(err)
	}
	return it
}

func TestSummary(t *testing.T) {
	it := item(t, `{
		"picture": [{"uuid":"p1","name":"front.png","type":"image/png","size":2048},{"uuid":"p2","name":"back.png","type":"image/png"}],
		"reminder": {"unit":"days","value":1},
		"reminders": [{"unit":"weeks","value":2}],
		"assignees": ["u1","u2"],
		"renter": {"type":"user","value":"u1"},
		"encoded": "[{\"uuid\":\"d1\",\"name\":\"manual.pdf\",\"size\":10}]",
		"price": 287.5
	}`)
	for key, want := range map[string]string{
		"picture":   "front.png, back.png",
		"reminder":  "1 day",
		"reminders": "2 weeks",
		"assignees": "u1, u2",
		"renter":    "type: user, value: u1",
		"encoded":   "manual.pdf",
		"price":     "287.5",
	} {
		if got := summary(it[key]); got != want {
			t.Errorf("summary(%s) = %q, want %q", key, got, want)
		}
	}
}

func TestExpand(t *testing.T) {
	plain := func(v any) string { return ansi.Strip(strings.Join(expand(v), "\n")) }

	files := item(t, `{"v":[{"uuid":"p1","name":"front.png","type":"image/jpeg","size":1434008,"data_uri":"/x"}]}`)
	if got := plain(files["v"]); got != "• front.png  image/jpeg · 1.4 MB" {
		t.Errorf("files: %q", got)
	}

	refs := item(t, `{"v":[{"id":1,"name":"Chair (102020)","type":"asset","uuid":"e7","status":"open"}]}`)
	if got := plain(refs["v"]); got != "• Chair (102020)  id 1 · status open · type asset · uuid e7" {
		t.Errorf("references: %q", got)
	}

	hub := item(t, `{"v":{"internal_identifier":"BC-1","documents":"[{\"uuid\":\"d1\",\"name\":\"a.pdf\",\"size\":10}]","empty":null}}`)
	want := "documents\n  • a.pdf  10 B\ninternal_identifier  BC-1"
	if got := plain(hub["v"]); got != want {
		t.Errorf("nested map:\n%s\nwant:\n%s", got, want)
	}

	order := item(t, `{"v":[{"id":7,"mappedAssetData":{"inventory_name":"Desk"}}]}`)
	want = "• #1  id 7\n  mappedAssetData\n    inventory_name  Desk"
	if got := plain(order["v"]); got != want {
		t.Errorf("list of records:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderItemNoJSON(t *testing.T) {
	it := item(t, `{
		"inventory_name": "Laptop",
		"picture": [{"uuid":"p1","name":"front.png","type":"image/png","size":2048}],
		"documents": [{"uuid":"d1","name":"manual.pdf","type":"application/pdf","size":10}],
		"due_date_reminder": {"unit":"days","value":3},
		"notes": [],
		"renter": {"type":"user","value":"u1"}
	}`)
	out := ansi.Strip(renderItem(it, 100))
	for _, want := range []string{"• front.png  image/png · 2.0 KB", "• manual.pdf  application/pdf · 10 B", "3 days", "type   user"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "notes") {
		t.Errorf("empty field shown:\n%s", out)
	}
	if strings.Contains(out, `{"`) || strings.Contains(out, "[{") {
		t.Errorf("raw JSON in:\n%s", out)
	}
	// Scalars come before expanded blocks.
	if strings.Index(out, "due_date_reminder") > strings.Index(out, "documents") {
		t.Errorf("inline fields should precede nested ones:\n%s", out)
	}
}

func TestClientPageSearchWords(t *testing.T) {
	all := []Item{
		{"firstname": "Henry", "lastname": "Rausch", "email": "h@example.com"},
		{"firstname": "Henry", "lastname": "Ford", "email": "ford@example.com"},
	}
	for search, want := range map[string]int{"Henry Rausch": 1, "rausch  HENRY": 1, "henry": 2, "Henry Miller": 0, "": 2} {
		got, total := clientPage(all, query{page: 1, perPage: 10, search: search})
		if len(got) != want || total != want {
			t.Errorf("search %q: %d hits (total %d), want %d", search, len(got), total, want)
		}
	}
}
