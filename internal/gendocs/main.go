// Command gendocs writes man pages and shell completions for packaging.
// Run by goreleaser: go run ./internal/gendocs <outdir> [unix-timestamp]
package main

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/SeventhingsCompany/customer-api-cli/internal/cmd"
	"github.com/spf13/cobra/doc"
)

func main() {
	if len(os.Args) < 2 || len(os.Args) > 3 {
		fmt.Fprintln(os.Stderr, "usage: gendocs <outdir> [unix-timestamp]")
		os.Exit(2)
	}
	epoch := os.Getenv("SOURCE_DATE_EPOCH")
	if len(os.Args) == 3 {
		epoch = os.Args[2]
	}
	if err := run(os.Args[1], epoch); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(out, epoch string) error {
	man := filepath.Join(out, "man")
	comp := filepath.Join(out, "completions")
	for _, d := range []string{man, comp} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	root := cmd.DocsRoot()
	date := time.Now()
	if epoch != "" { // reproducible builds
		sec, err := strconv.ParseInt(epoch, 10, 64)
		if err != nil {
			return fmt.Errorf("timestamp %q: %w", epoch, err)
		}
		if sec > 0 { // goreleaser passes 0 when the commit date is unknown
			date = time.Unix(sec, 0).UTC()
		}
	}
	header := &doc.GenManHeader{Title: "SEVENTHINGS", Section: "1", Source: "seventhings", Manual: "seventhings manual", Date: &date}
	if err := doc.GenManTree(root, header, man); err != nil {
		return fmt.Errorf("man pages: %w", err)
	}
	if err := gzipAll(man, "*.1"); err != nil {
		return err
	}
	for name, gen := range map[string]func(string) error{
		"seventhings.bash": func(p string) error { return root.GenBashCompletionFileV2(p, true) },
		"_seventhings":     root.GenZshCompletionFile,
		"seventhings.fish": func(p string) error { return root.GenFishCompletionFile(p, true) },
		"seventhings.ps1":  root.GenPowerShellCompletionFileWithDesc,
	} {
		if err := gen(filepath.Join(comp, name)); err != nil {
			return fmt.Errorf("completion %s: %w", name, err)
		}
	}
	return nil
}

// gzipAll compresses matching files in dir and removes the originals. The
// gzip header carries no name or timestamp, so output is reproducible.
func gzipAll(dir, pattern string) error {
	files, err := filepath.Glob(filepath.Join(dir, pattern))
	if err != nil {
		return err
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		var buf bytes.Buffer
		zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		if err != nil {
			return err
		}
		if _, err := zw.Write(data); err != nil {
			return err
		}
		if err := zw.Close(); err != nil {
			return err
		}
		if err := os.WriteFile(f+".gz", buf.Bytes(), 0o644); err != nil {
			return err
		}
		if err := os.Remove(f); err != nil {
			return err
		}
	}
	return nil
}
