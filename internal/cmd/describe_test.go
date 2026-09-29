package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func describeInTerminal(t *testing.T, flags ...string) result {
	t.Helper()
	dir := t.TempDir()
	var out, stderr bytes.Buffer
	code := Execute(context.Background(), append([]string{"describe"}, flags...),
		IO{In: strings.NewReader(""), Out: &out, Err: &stderr, StdinTTY: true, StdoutTTY: true},
		func(k string) string {
			if k == "SEVENTHINGS_CONFIG_DIR" {
				return dir
			}
			return ""
		}, BuildInfo{Version: "test"})
	return result{out: out.String(), err: stderr.String(), code: code}
}

func TestDescribeDefaultsToReadableOverviewInTerminal(t *testing.T) {
	r := describeInTerminal(t)
	if r.code != 0 || r.err != "" {
		t.Fatalf("describe: %+v", r)
	}
	for _, text := range []string{"command overview", "objects", "objects files", "hub items", "hub orders", "archive", "unarchive", "--help", "describe --agent"} {
		if !strings.Contains(r.out, text) {
			t.Fatalf("overview missing %q: %s", text, r.out)
		}
	}
	if json.Valid([]byte(r.out)) || strings.Count(r.out, "\n") > 35 || strings.Contains(r.out, "...") {
		t.Fatalf("overview is not compact or has truncated actions: %s", r.out)
	}
}

func TestDescribeExplicitJSONInTerminalIsComplete(t *testing.T) {
	for _, flags := range [][]string{{"--output", "json"}, {"--agent"}} {
		r := describeInTerminal(t, flags...)
		if r.code != 0 {
			t.Fatalf("describe: %+v", r)
		}
		catalog := r.json(t).(map[string]any)
		commands, ok := catalog["commands"].([]any)
		if !ok || len(commands) < 50 {
			t.Fatalf("command catalog missing or truncated: %v", catalog["commands"])
		}
		flags, ok := catalog["global_flags"].([]any)
		if !ok || len(flags) == 0 || catalog["name"] != "seventhings" {
			t.Fatal("catalog is incomplete")
		}
	}
}

func TestDescribeRespectsExplicitOutputAndFilters(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags []string
		want  string
	}{
		{"json", []string{"--output", "json", "--fields", "name"}, "{\n  \"name\": \"seventhings\"\n}\n"},
		{"yaml", []string{"--output", "yaml", "--fields", "name"}, "name: seventhings\n"},
		{"table", []string{"--output", "table", "--fields", "name"}, "name  seventhings\n"},
		{"jq", []string{"--jq", ".name", "--raw"}, "seventhings\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := describeInTerminal(t, tc.flags...)
			if r.code != 0 || r.err != "" || r.out != tc.want {
				t.Fatalf("got %+v, want stdout %q", r, tc.want)
			}
		})
	}
}

func TestDescribeStillDefaultsToJSONInAgentMode(t *testing.T) {
	r := run(t, map[string]string{"SEVENTHINGS_CONFIG_DIR": t.TempDir()}, "", "describe")
	if r.code != 0 || !json.Valid([]byte(r.out)) {
		t.Fatalf("agent describe: %+v", r)
	}
}
