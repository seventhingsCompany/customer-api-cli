package tui

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// errRemote: over SSH a viewer would open on the remote machine.
var errRemote = errors.New("this session runs over SSH, so a viewer would open on the remote machine; press D to download the file instead")

// openFile opens path with the system's default application. It does not
// wait for the viewer to exit.
func openFile(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", path)
	default:
		opener := ""
		for _, c := range []string{"xdg-open", "gio", "wslview"} {
			if p, err := exec.LookPath(c); err == nil {
				opener = p
				break
			}
		}
		switch filepath.Base(opener) {
		case "":
			return errors.New("no viewer found (install xdg-utils), or press D to download the file")
		case "gio":
			cmd = exec.Command(opener, "open", path)
		default:
			cmd = exec.Command(opener, path)
		}
	}
	cmd.Stdout, cmd.Stderr = nil, nil // keep the TUI screen clean
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }() // reap
	return nil
}

// viewerPath returns where a downloaded file is stored for viewing. The
// name is stable per file, so opening it again reuses the copy.
func viewerPath(uuid, name string) (string, error) {
	dir := filepath.Join(os.TempDir(), "seventhings-files")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	name = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) || r < 32 {
			return '_'
		}
		return r
	}, name)
	if name == "" {
		name = "file"
	}
	return filepath.Join(dir, uuid+"-"+name), nil
}

// isRemote reports whether the TUI runs in an SSH session.
func isRemote(env func(string) string) bool {
	return env("SSH_CONNECTION") != "" || env("SSH_TTY") != "" || env("SSH_CLIENT") != ""
}
