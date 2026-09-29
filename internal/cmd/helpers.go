package cmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"

	"github.com/SeventhingsCompany/customer-api-cli/internal/exitcode"
	"github.com/SeventhingsCompany/customer-api-cli/internal/filesave"
	"github.com/SeventhingsCompany/customer-api-cli/internal/input"
	"github.com/SeventhingsCompany/customer-api-cli/internal/listflags"
	"github.com/SeventhingsCompany/customer-api-go/client"
	"github.com/SeventhingsCompany/customer-api-go/models"
	"github.com/spf13/cobra"
)

// show builds a RunE that calls fn with an authenticated client and prints
// its result.
func (a *App) show(fn func(ctx context.Context, cl *client.Client, args []string) (any, error)) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		cl, err := a.Client()
		if err != nil {
			return err
		}
		v, err := fn(cmd.Context(), cl, args)
		if err != nil {
			return err
		}
		return a.print(v)
	}
}

// intArg parses a numeric ID argument.
func intArg(s, name string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0, exitcode.Usagef("%s must be a positive integer, got %q", name, s)
	}
	return n, nil
}

// filterObjectFlags builds a models.FilterObject from --filter/--sort flags
// or a --data JSON document.
type filterObjectFlags struct {
	filters []string
	sorts   []string
	data    string
}

func (f *filterObjectFlags) register(c *cobra.Command) {
	c.Flags().StringArrayVarP(&f.filters, "filter", "f", nil, `select records as "field op value" (repeatable)`)
	c.Flags().StringArrayVar(&f.sorts, "sort", nil, `sort as "field", "field:desc" or "-field"`)
	c.Flags().StringVarP(&f.data, "data", "d", "", `raw filter object JSON {"filter":{...},"sort":{...}}, @file or -`)
}

func (f *filterObjectFlags) build(a *App) (models.FilterObject, error) {
	var fo models.FilterObject
	if f.data != "" {
		if err := input.Into(f.data, nil, a.io.In, &fo); err != nil {
			return fo, err
		}
	}
	for _, s := range f.filters {
		e, err := listflags.ParseFilter(s)
		if err != nil {
			return fo, err
		}
		if fo.Filter == nil {
			fo.Filter = map[string]map[models.FilterOperator]any{}
		}
		if fo.Filter[e.Field] == nil {
			fo.Filter[e.Field] = map[models.FilterOperator]any{}
		}
		if e.Operator == models.FilterIn || e.Operator == models.FilterNin {
			fo.Filter[e.Field][e.Operator] = e.Values
		} else {
			fo.Filter[e.Field][e.Operator] = e.Values[0]
		}
	}
	for _, s := range f.sorts {
		field, dir, err := listflags.ParseSort(s)
		if err != nil {
			return fo, err
		}
		if fo.Sort == nil {
			fo.Sort = map[string]models.SortDirection{}
		}
		fo.Sort[field] = dir
	}
	if len(fo.Filter) == 0 {
		return fo, exitcode.Usagef("at least one --filter (or --data) is required")
	}
	return fo, nil
}

// writeBinary writes downloaded bytes to path, or stdout for "-". Raw bytes
// are never written to a terminal.
func (a *App) writeBinary(data []byte, path string, overwrite bool, meta map[string]any) error {
	if a.dryRun {
		return nil
	}
	if path == "" {
		return exitcode.Usagef("--out is required (a file path, or - for stdout)")
	}
	if path == "-" {
		if a.io.StdoutTTY {
			return exitcode.Usagef("refusing to write binary data to a terminal; redirect stdout or use --out <file>")
		}
		_, err := a.io.Out.Write(data)
		return err
	}
	if err := filesave.Write(path, data, overwrite); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return exitcode.Usagef("%s already exists; pass --overwrite to replace it", path)
		}
		return err
	}
	meta["path"] = path
	meta["bytes"] = len(data)
	return a.done(fmt.Sprintf("Wrote %d bytes to %s", len(data), path), meta)
}

func addOutFlag(c *cobra.Command, out *string, overwrite *bool) {
	c.Flags().StringVarP(out, "out", "O", "", "write to this file, or - for stdout")
	c.Flags().BoolVar(overwrite, "overwrite", false, "replace an existing output file")
}

// csvInts parses repeated/comma-separated integer flags.
func csvInts(vals []string, name string) ([]int, error) {
	var out []int
	for _, v := range vals {
		for p := range strings.SplitSeq(v, ",") {
			if p = strings.TrimSpace(p); p == "" {
				continue
			}
			n, err := intArg(p, name)
			if err != nil {
				return nil, err
			}
			out = append(out, n)
		}
	}
	return out, nil
}
