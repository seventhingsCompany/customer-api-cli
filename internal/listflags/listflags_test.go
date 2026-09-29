package listflags

import (
	"reflect"
	"testing"

	"github.com/SeventhingsCompany/customer-api-cli/internal/exitcode"
	"github.com/SeventhingsCompany/customer-api-go/models"
)

func TestParseFilter(t *testing.T) {
	tests := []struct {
		in   string
		want models.FilterEntry
	}{
		{"name = Laptop", models.Eq("name", "Laptop")},
		{"name like Dell Latitude 7440", models.Like("name", "Dell Latitude 7440")},
		{"price >= 100", models.Gte("price", "100")},
		{"status in a, b,c", models.In("status", "a", "b", "c")},
		{"status nin x", models.Nin("status", "x")},
		{"note = ", models.Eq("note", "")},
		{"deadline lt_or_null 2026-01-01", models.FilterEntry{Field: "deadline", Operator: models.FilterLtOrNull, Values: []string{"2026-01-01"}}},
		{"serial !~ SN", models.NotLike("serial", "SN")},
	}
	for _, tt := range tests {
		got, err := ParseFilter(tt.in)
		if err != nil {
			t.Errorf("%q: %v", tt.in, err)
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%q: got %+v, want %+v", tt.in, got, tt.want)
		}
	}
	for _, bad := range []string{"name", "name ?? x", "status in  ,"} {
		if _, err := ParseFilter(bad); exitcode.For(err) != exitcode.Usage {
			t.Errorf("%q: want usage error, got %v", bad, err)
		}
	}
}

func TestParseSort(t *testing.T) {
	for in, want := range map[string]models.SortDirection{"name": models.SortASC, "name:DESC": models.SortDESC, "-name": models.SortDESC} {
		f, d, err := ParseSort(in)
		if err != nil || f != "name" || d != want {
			t.Errorf("%q: %s %s %v", in, f, d, err)
		}
	}
	if _, _, err := ParseSort("name:up"); err == nil {
		t.Error("want error")
	}
}

func TestListOptionsEncode(t *testing.T) {
	f := Flags{Filters: []string{"name like Lap top"}, Sorts: []string{"-created_at"}, Page: 2, PerPage: 50}
	o, err := f.ListOptions()
	if err != nil {
		t.Fatal(err)
	}
	want := "page=2&per_page=50&sort[created_at]=DESC&filter[name][like][]=Lap+top"
	if got := o.Encode(); got != want {
		t.Errorf("got %s\nwant %s", got, want)
	}
}
