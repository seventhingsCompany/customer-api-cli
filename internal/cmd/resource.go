package cmd

import (
	"context"
	"fmt"
	"iter"

	"github.com/SeventhingsCompany/customer-api-cli/internal/input"
	"github.com/SeventhingsCompany/customer-api-cli/internal/listflags"
	"github.com/SeventhingsCompany/customer-api-go/client"
	"github.com/SeventhingsCompany/customer-api-go/models"
	"github.com/spf13/cobra"
)

// fieldResource describes a resource whose records are dynamic field maps
// (objects, rooms, locations). Nil funcs omit the subcommand.
type fieldResource struct {
	use, singular string
	aliases       []string

	list    func(*client.Client, context.Context, *models.ListOptions) ([]map[string]any, error)
	all     func(*client.Client, context.Context, *models.ListOptions) iter.Seq2[models.Fields, error]
	count   func(*client.Client, context.Context, *models.ListOptions) (int, error)
	get     func(*client.Client, context.Context, string) (map[string]any, error)
	create  func(*client.Client, context.Context, map[string]any) (string, error)
	update  func(*client.Client, context.Context, string, map[string]any) (map[string]any, error)
	delete  func(*client.Client, context.Context, string) error
	history func(*client.Client, context.Context, string, *models.HistoryListOptions) (any, error)
}

func (a *App) fieldResourceCmd(r fieldResource) *cobra.Command {
	root := &cobra.Command{Use: r.use, Aliases: r.aliases, Short: fmt.Sprintf("Manage %s", r.use)}
	if r.list != nil {
		root.AddCommand(a.listCmd(r))
	}
	if r.count != nil {
		root.AddCommand(a.countCmd(r))
	}
	if r.get != nil {
		root.AddCommand(&cobra.Command{
			Use:   "get <uuid>",
			Short: fmt.Sprintf("Get a %s by UUID", r.singular),
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				cl, err := a.Client()
				if err != nil {
					return err
				}
				res, err := r.get(cl, cmd.Context(), args[0])
				if err != nil {
					return err
				}
				return a.print(res)
			},
		})
	}
	if r.create != nil {
		root.AddCommand(a.createCmd(r))
	}
	if r.update != nil {
		root.AddCommand(a.updateCmd(r))
	}
	if r.delete != nil {
		root.AddCommand(a.uuidActionCmd("delete", fmt.Sprintf("Delete a %s", r.singular), r.singular, "Deleted", true, r.delete))
	}
	if r.history != nil {
		root.AddCommand(a.historyCmd(r.singular, r.history))
	}
	return root
}

func (a *App) listCmd(r fieldResource) *cobra.Command {
	var lf listflags.Flags
	c := &cobra.Command{
		Use:   "list",
		Short: fmt.Sprintf("List %s", r.use),
		Example: fmt.Sprintf(`  seventhings %[1]s list --filter 'name like Laptop' --sort -created_at
  seventhings %[1]s list --all --fields uuid,name > %[1]s.ndjson`, r.use),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := lf.ListOptions()
			if err != nil {
				return err
			}
			cl, err := a.Client()
			if err != nil {
				return err
			}
			if lf.All {
				return streamAll(a, r.all(cl, cmd.Context(), opts))
			}
			items, err := r.list(cl, cmd.Context(), opts)
			if err != nil {
				return err
			}
			return a.print(items)
		},
	}
	lf.Register(c.Flags(), true)
	return c
}

func (a *App) countCmd(r fieldResource) *cobra.Command {
	var lf listflags.Flags
	c := &cobra.Command{
		Use:   "count",
		Short: fmt.Sprintf("Count %s matching filters", r.use),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := lf.ListOptions()
			if err != nil {
				return err
			}
			cl, err := a.Client()
			if err != nil {
				return err
			}
			n, err := r.count(cl, cmd.Context(), opts)
			if err != nil {
				return err
			}
			return a.print(map[string]int{"count": n})
		},
	}
	c.Flags().StringArrayVarP(&lf.Filters, "filter", "f", nil, `filter as "field op value", repeatable`)
	return c
}

func (a *App) createCmd(r fieldResource) *cobra.Command {
	var data string
	var sets []string
	c := &cobra.Command{
		Use:   "create",
		Short: fmt.Sprintf("Create a %s", r.singular),
		Example: fmt.Sprintf(`  seventhings %[1]s create --set name=Laptop --set serial=SN-123
  seventhings %[1]s create --data @%[2]s.json
  seventhings %[1]s create --data - --dry-run < %[2]s.json`, r.use, r.singular),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := requireBody(data, sets, a)
			if err != nil {
				return err
			}
			cl, err := a.Client()
			if err != nil {
				return err
			}
			uuid, err := r.create(cl, cmd.Context(), body)
			if err != nil {
				return err
			}
			return a.done(fmt.Sprintf("Created %s %s", r.singular, uuid), map[string]any{"uuid": uuid})
		},
	}
	addBodyFlags(c, &data, &sets)
	return write(c)
}

func (a *App) updateCmd(r fieldResource) *cobra.Command {
	var data string
	var sets []string
	c := &cobra.Command{
		Use:   "update <uuid>",
		Short: fmt.Sprintf("Update fields of a %s (PATCH)", r.singular),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := requireBody(data, sets, a)
			if err != nil {
				return err
			}
			cl, err := a.Client()
			if err != nil {
				return err
			}
			res, err := r.update(cl, cmd.Context(), args[0], body)
			if err != nil {
				return err
			}
			// The API answers PATCH with {} on some instances; only print real records.
			if len(res) > 0 && !a.dryRun {
				return a.print(res)
			}
			return a.done(fmt.Sprintf("Updated %s %s", r.singular, args[0]), map[string]any{"uuid": args[0], "updated": true})
		},
	}
	addBodyFlags(c, &data, &sets)
	return write(c)
}

// uuidActionCmd builds `<verb> <uuid>` commands with no body.
func (a *App) uuidActionCmd(verb, short, singular, past string, isDestructive bool,
	fn func(*client.Client, context.Context, string) error,
) *cobra.Command {
	c := &cobra.Command{
		Use:   verb + " <uuid>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if isDestructive {
				if err := a.confirm(fmt.Sprintf("%s %s %s", verb, singular, args[0])); err != nil {
					return err
				}
			}
			cl, err := a.Client()
			if err != nil {
				return err
			}
			if err := fn(cl, cmd.Context(), args[0]); err != nil {
				return err
			}
			return a.done(fmt.Sprintf("%s %s %s", past, singular, args[0]),
				map[string]any{"uuid": args[0], verb + "d": true})
		},
	}
	if isDestructive {
		return destructive(c)
	}
	return write(c)
}

func (a *App) historyCmd(singular string,
	fn func(*client.Client, context.Context, string, *models.HistoryListOptions) (any, error),
) *cobra.Command {
	var opts models.HistoryListOptions
	c := &cobra.Command{
		Use:   "history <uuid>",
		Short: fmt.Sprintf("Show the change history of a %s (newest first)", singular),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := a.Client()
			if err != nil {
				return err
			}
			res, err := fn(cl, cmd.Context(), args[0], &opts)
			if err != nil {
				return err
			}
			return a.print(res)
		},
	}
	c.Flags().IntVar(&opts.Page, "page", 0, "page number (1-based)")
	c.Flags().IntVar(&opts.PerPage, "per-page", 0, "entries per page (API default 50, max 200)")
	return c
}

func addBodyFlags(c *cobra.Command, data *string, sets *[]string) {
	c.Flags().StringVarP(data, "data", "d", "", "JSON body: inline, @file, or - for stdin")
	c.Flags().StringArrayVar(sets, "set", nil, "set a field: key=string or key:=json (repeatable)")
}

func requireBody(data string, sets []string, a *App) (map[string]any, error) {
	body, err := input.Body(data, sets, a.io.In)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, usageNoBody()
	}
	return body, nil
}

// streamAll prints every item of an SDK iterator as it arrives.
func streamAll[T any](a *App, seq iter.Seq2[T, error]) error {
	s := a.printer.Stream()
	for item, err := range seq {
		if err != nil {
			_ = s.Close()
			return err
		}
		if err := s.Add(item); err != nil {
			return err
		}
	}
	return s.Close()
}
