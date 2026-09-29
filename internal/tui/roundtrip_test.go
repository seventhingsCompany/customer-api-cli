package tui_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/SeventhingsCompany/customer-api-cli/internal/cmd"
	"github.com/SeventhingsCompany/customer-api-cli/internal/tui"
)

// splitShell undoes shellJoin: words split on spaces, '…' quoted, \
// escapes outside quotes.
func splitShell(s string) []string {
	var args []string
	var cur strings.Builder
	inQuote, started, escaped := false, false, false
	for _, c := range s {
		switch {
		case escaped:
			cur.WriteRune(c)
			escaped = false
		case c == '\\' && !inQuote:
			escaped, started = true, true
		case c == '\'':
			inQuote, started = !inQuote, true
		case c == ' ' && !inQuote:
			if started {
				args = append(args, cur.String())
				cur.Reset()
				started = false
			}
		default:
			cur.WriteRune(c)
			started = true
		}
	}
	if started {
		args = append(args, cur.String())
	}
	return args
}

// TestCLICommandRoundTrip runs the commands the UI copies through the real
// CLI, so a renamed flag or command group breaks here.
func TestCLICommandRoundTrip(t *testing.T) {
	var mu sync.Mutex
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = append(got, r.URL.Path+"?"+r.URL.RawQuery)
		mu.Unlock()
		_, _ = w.Write([]byte(`{"items":[],"total":0}`))
	}))
	t.Cleanup(srv.Close)
	env := map[string]string{
		"SEVENTHINGS_CONFIG_DIR": t.TempDir(), "SEVENTHINGS_BASE_URL": srv.URL,
		"SEVENTHINGS_TOKEN": "t", "SEVENTHINGS_RATE_LIMIT": "0",
	}
	for _, tc := range []struct {
		tab                  int
		search, filter, sort string
		want                 []string
	}{
		{0, "Lap top", "barcode = it's\ninventory_group in IT, Möbel", "-updated_at",
			[]string{"/objects?", "page=2", "filter[inventory_name][like][]=Lap+top", "filter[barcode][eq]=it%27s", "filter[inventory_group][in][]=M%C3%B6bel", "sort[updated_at]=DESC"}},
		{3, "", "", "last_name:desc", []string{"/persons?", "sort_by=last_name", "order=desc"}},
		{4, "", "", "email", []string{"/users?", "sort_by=email", "order=asc"}},
		{6, "", "status = open", "title", []string{"/rental-management/rental-cases?", "filter[status][eq]=open", "sort[title]=ASC"}},
		{8, "chair", "", "", []string{"/circularity-hub/items?", "filter[name][like][]=chair"}},
		{9, "", "", "-id", []string{"/circularity-hub/orders?", "sort[id]=DESC"}},
	} {
		line, err := tui.ListCommand(tc.tab, tc.search, tc.filter, tc.sort)
		if err != nil {
			t.Fatal(err)
		}
		args := splitShell(line)[1:] // drop "seventhings"
		mu.Lock()
		got = nil
		mu.Unlock()
		var out, errBuf bytes.Buffer
		code := cmd.Execute(context.Background(), args, cmd.IO{In: strings.NewReader(""), Out: &out, Err: &errBuf},
			func(k string) string { return env[k] }, cmd.BuildInfo{})
		if code != 0 {
			t.Errorf("%s: exit %d: %s", line, code, errBuf.String())
			continue
		}
		mu.Lock()
		req := strings.Join(got, " ")
		mu.Unlock()
		for _, w := range tc.want {
			if !strings.Contains(req, w) {
				t.Errorf("%s: request %q lacks %q", line, req, w)
			}
		}
	}
}
