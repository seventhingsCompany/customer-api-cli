// Package listflags turns --filter/--sort/--page/--per-page flags into SDK
// list options.
package listflags

import (
	"strings"

	"github.com/SeventhingsCompany/customer-api-cli/internal/exitcode"
	"github.com/SeventhingsCompany/customer-api-go/models"
	"github.com/spf13/pflag"
)

// Operators maps accepted --filter operator spellings to API operators.
var Operators = map[string]models.FilterOperator{
	"=": models.FilterEq, "==": models.FilterEq, "eq": models.FilterEq,
	"!=": models.FilterNeq, "neq": models.FilterNeq,
	">": models.FilterGt, "gt": models.FilterGt, "gt_or_null": models.FilterGtOrNull,
	">=": models.FilterGte, "gte": models.FilterGte, "gte_or_null": models.FilterGteOrNull,
	"<": models.FilterLt, "lt": models.FilterLt, "lt_or_null": models.FilterLtOrNull,
	"<=": models.FilterLte, "lte": models.FilterLte, "lte_or_null": models.FilterLteOrNull,
	"like": models.FilterLike, "~": models.FilterLike,
	"nlike": models.FilterNotLike, "not_like": models.FilterNotLike, "!~": models.FilterNotLike,
	"in": models.FilterIn, "nin": models.FilterNin, "not_in": models.FilterNin,
}

// Flags holds list flag values.
type Flags struct {
	Filters []string
	Sorts   []string
	Page    int
	PerPage int
	All     bool
}

// Register adds the flags to fs. filterable=false omits --filter (endpoints
// with typed filters instead).
func (f *Flags) Register(fs *pflag.FlagSet, filterable bool) {
	if filterable {
		fs.StringArrayVarP(&f.Filters, "filter", "f", nil,
			`filter as "field op value", repeatable; ops: = != > >= < <= like nlike in nin (in/nin take comma-separated values)`)
		fs.StringArrayVar(&f.Sorts, "sort", nil, `sort as "field", "field:desc" or "-field", repeatable`)
	}
	fs.IntVar(&f.Page, "page", 0, "page number (1-based)")
	fs.IntVar(&f.PerPage, "per-page", 0, "page size")
	fs.BoolVar(&f.All, "all", false, "fetch every page (streams NDJSON in JSON mode)")
}

// ListOptions converts to *models.ListOptions.
func (f *Flags) ListOptions() (*models.ListOptions, error) {
	o := models.NewListOptions().WithPage(f.Page).WithPerPage(f.PerPage)
	for _, s := range f.Filters {
		e, err := ParseFilter(s)
		if err != nil {
			return nil, err
		}
		o.Where(e)
	}
	for _, s := range f.Sorts {
		field, dir, err := ParseSort(s)
		if err != nil {
			return nil, err
		}
		o.SortBy(field, dir)
	}
	return o, nil
}

// ParseFilter parses `field op value`. The value is everything after the
// operator, so it may contain spaces.
func ParseFilter(s string) (models.FilterEntry, error) {
	parts := strings.Fields(s)
	if len(parts) < 2 {
		return models.FilterEntry{}, exitcode.Usagef("--filter %q: want \"field op value\"", s)
	}
	field, opStr := parts[0], strings.ToLower(parts[1])
	op, ok := Operators[opStr]
	if !ok {
		return models.FilterEntry{}, exitcode.Usagef("--filter %q: unknown operator %q", s, parts[1])
	}
	// Recover the raw value, preserving inner whitespace.
	rest := strings.TrimSpace(s)
	rest = strings.TrimSpace(strings.TrimPrefix(rest, field))
	rest = strings.TrimSpace(rest[len(parts[1]):])
	if len(parts) == 2 {
		rest = ""
	}

	values := []string{rest}
	if op == models.FilterIn || op == models.FilterNin {
		values = nil
		for v := range strings.SplitSeq(rest, ",") {
			if v = strings.TrimSpace(v); v != "" {
				values = append(values, v)
			}
		}
		if len(values) == 0 {
			return models.FilterEntry{}, exitcode.Usagef("--filter %q: %s needs at least one value", s, opStr)
		}
	}
	return models.FilterEntry{Field: field, Operator: op, Values: values}, nil
}

// ParseSort parses "field", "field:asc", "field:desc" or "-field".
func ParseSort(s string) (string, models.SortDirection, error) {
	if f, ok := strings.CutPrefix(s, "-"); ok && f != "" {
		return f, models.SortDESC, nil
	}
	field, dir, _ := strings.Cut(s, ":")
	if field == "" {
		return "", "", exitcode.Usagef("--sort %q: missing field", s)
	}
	switch strings.ToLower(dir) {
	case "", "asc":
		return field, models.SortASC, nil
	case "desc":
		return field, models.SortDESC, nil
	}
	return "", "", exitcode.Usagef("--sort %q: direction must be asc or desc", s)
}
