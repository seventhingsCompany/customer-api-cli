package mode

import "testing"

func TestResolve(t *testing.T) {
	tests := []struct {
		name    string
		flag    bool
		env     map[string]string
		in, out bool
		want    Mode
	}{
		{"tty", false, nil, true, true, Interactive},
		{"flag wins", true, map[string]string{"SEVENTHINGS_MODE": "interactive"}, true, true, Automation},
		{"env agent", false, map[string]string{"SEVENTHINGS_MODE": "agent"}, true, true, Automation},
		{"env interactive beats CI and pipe", false, map[string]string{"SEVENTHINGS_MODE": "interactive", "CI": "true"}, false, false, Interactive},
		{"CI", false, map[string]string{"CI": "true"}, true, true, Automation},
		{"CI=1", false, map[string]string{"CI": "1"}, true, true, Automation},
		{"CI=false", false, map[string]string{"CI": "false"}, true, true, Interactive},
		{"stdout piped", false, nil, true, false, Automation},
		{"stdin piped", false, nil, false, true, Automation},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := func(k string) string { return tt.env[k] }
			if got := Resolve(tt.flag, getenv, tt.in, tt.out); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
