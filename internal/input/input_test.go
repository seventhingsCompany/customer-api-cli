package input

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/SeventhingsCompany/customer-api-cli/internal/exitcode"
)

func TestBody(t *testing.T) {
	file := filepath.Join(t.TempDir(), "b.json")
	_ = os.WriteFile(file, []byte(`{"from":"file","n":1}`), 0o600)

	got, err := Body("@"+file, []string{"serial=123", "count:=5", "tags:=[\"a\"]", "n:=null", "eq=a=b"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"from": "file", "n": nil, "serial": "123", "count": json.Number("5"), "tags": []any{"a"}, "eq": "a=b"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v", got)
	}

	got, err = Body("-", nil, strings.NewReader(`{"stdin":true}`))
	if err != nil || got["stdin"] != true {
		t.Errorf("stdin: %v %v", got, err)
	}

	for _, bad := range [][2]string{{"[1]", ""}, {"", "noequals"}, {"", "x:=notjson"}} {
		var sets []string
		if bad[1] != "" {
			sets = []string{bad[1]}
		}
		if _, err := Body(bad[0], sets, nil); exitcode.For(err) != exitcode.Usage {
			t.Errorf("%v: want usage error, got %v", bad, err)
		}
	}
}

func TestIntoRejectsUnknownFields(t *testing.T) {
	var dest struct {
		Name string `json:"name"`
	}
	if err := Into(`{"name":"x"}`, nil, nil, &dest); err != nil || dest.Name != "x" {
		t.Fatalf("%v %v", dest, err)
	}
	if err := Into(`{"nmae":"x"}`, nil, nil, &dest); exitcode.For(err) != exitcode.Usage {
		t.Fatalf("want usage error, got %v", err)
	}
}
