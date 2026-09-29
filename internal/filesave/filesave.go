// Package filesave commits downloads without truncating an existing file on failure.
package filesave

import (
	"fmt"
	"os"
	"path/filepath"
)

// Write saves data using a private temporary file beside the destination.
// With overwrite=false, a hard link commits the file only if the destination
// does not exist, avoiding a check-then-write race. Overwrites use Rename;
// platforms/filesystems that cannot replace the target safely return an error
// rather than deleting the old file first.
func Write(path string, data []byte, overwrite bool) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".seventhings-download-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("save %s: %w", path, err)
	}
	if overwrite {
		err = os.Rename(f.Name(), path)
	} else {
		err = os.Link(f.Name(), path)
	}
	return err
}
