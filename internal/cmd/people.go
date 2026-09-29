package cmd

import (
	"context"
	"strings"

	"github.com/SeventhingsCompany/customer-api-cli/internal/exitcode"
	"github.com/SeventhingsCompany/customer-api-go/client"
	"github.com/SeventhingsCompany/customer-api-go/models"
	"github.com/spf13/cobra"
)

// sortOrderFlags are the page/sort flags of the persons and users endpoints,
// which take sort_by + order instead of the generic filter syntax.
type sortOrderFlags struct {
	page, perPage int
	sortBy, order string
	all           bool
}

func (f *sortOrderFlags) register(c *cobra.Command, sortHelp string) {
	c.Flags().IntVar(&f.page, "page", 0, "page number (1-based)")
	c.Flags().IntVar(&f.perPage, "per-page", 0, "page size")
	c.Flags().StringVar(&f.sortBy, "sort", "", sortHelp)
	c.Flags().StringVar(&f.order, "order", "", "asc or desc")
	c.Flags().BoolVar(&f.all, "all", false, "fetch every page (streams NDJSON in JSON mode)")
}

func (f *sortOrderFlags) orderPtr() (*models.UserSortOrder, error) {
	switch strings.ToLower(f.order) {
	case "":
		return nil, nil
	case "asc", "desc":
		o := models.UserSortOrder(strings.ToLower(f.order))
		return &o, nil
	}
	return nil, exitcode.Usagef("--order must be asc or desc")
}

func optInt(n int) *int {
	if n == 0 {
		return nil
	}
	return &n
}

func optStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (f *sortOrderFlags) personOptions() (*models.PersonListOptions, error) {
	order, err := f.orderPtr()
	if err != nil {
		return nil, err
	}
	return &models.PersonListOptions{Page: optInt(f.page), PerPage: optInt(f.perPage), SortBy: optStr(f.sortBy), Order: order}, nil
}

func (f *sortOrderFlags) userOptions() (*models.UserListOptions, error) {
	order, err := f.orderPtr()
	if err != nil {
		return nil, err
	}
	o := &models.UserListOptions{Page: optInt(f.page), PerPage: optInt(f.perPage), Order: order}
	switch f.sortBy {
	case "":
	case "id", "email":
		by := models.UserSortBy(f.sortBy)
		o.SortBy = &by
	default:
		return nil, exitcode.Usagef("--sort must be id or email")
	}
	return o, nil
}

// personOut returns the complete field map of a person; the typed struct
// omits custom fields.
func personOut(p *models.Person) any {
	if p.Fields != nil {
		return p.Fields
	}
	return p
}

func (a *App) personsCmd() *cobra.Command {
	c := &cobra.Command{Use: "persons", Aliases: []string{"person", "people"}, Short: "Manage persons"}

	var lf sortOrderFlags
	list := &cobra.Command{
		Use:   "list",
		Short: "List persons",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := lf.personOptions()
			if err != nil {
				return err
			}
			cl, err := a.Client()
			if err != nil {
				return err
			}
			if lf.all {
				s := a.printer.Stream()
				for p, err := range cl.PersonsAll(cmd.Context(), opts) {
					if err != nil {
						_ = s.Close()
						return err
					}
					if err := s.Add(personOut(&p)); err != nil {
						return err
					}
				}
				return s.Close()
			}
			res, err := cl.PersonsList(cmd.Context(), opts)
			if err != nil {
				return err
			}
			out := make([]any, len(res.Items))
			for i := range res.Items {
				out[i] = personOut(&res.Items[i])
			}
			return a.print(out)
		},
	}
	lf.register(list, "sort by field key")

	count := &cobra.Command{
		Use:   "count",
		Short: "Count persons",
		Args:  cobra.NoArgs,
		RunE: a.show(func(ctx context.Context, cl *client.Client, _ []string) (any, error) {
			n, err := cl.PersonsCount(ctx, nil)
			return map[string]int{"count": n}, err
		}),
	}

	var byID int
	get := &cobra.Command{
		Use:   "get <uuid> | --id <id>",
		Short: "Get a person by UUID or numeric ID",
		Args:  cobra.MaximumNArgs(1),
		RunE: a.show(func(ctx context.Context, cl *client.Client, args []string) (any, error) {
			if (len(args) == 1) == (byID != 0) {
				return nil, exitcode.Usagef("give either a UUID or --id")
			}
			var p *models.Person
			var err error
			if byID != 0 {
				p, err = cl.PersonGetByID(ctx, byID)
			} else {
				p, err = cl.PersonGet(ctx, args[0])
			}
			if err != nil {
				return nil, err
			}
			return personOut(p), nil
		}),
	}
	get.Flags().IntVar(&byID, "id", 0, "numeric person ID")

	res := fieldResource{
		use:      "persons",
		singular: "person",
		create:   (*client.Client).PersonCreate,
		update: func(c *client.Client, ctx context.Context, uuid string, f map[string]any) (map[string]any, error) {
			return nil, c.PersonPatch(ctx, uuid, f)
		},
		delete: (*client.Client).PersonDelete,
	}

	var fo filterObjectFlags
	createUser := &cobra.Command{
		Use:   "create-user",
		Short: "Create user accounts for the persons matching a filter",
		Example: `  seventhings persons create-user --filter 'email = jane@acme.com'
  seventhings persons create-user --filter 'department in IT,Facility' --dry-run`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			filter, err := fo.build(a)
			if err != nil {
				return err
			}
			cl, err := a.Client()
			if err != nil {
				return err
			}
			if err := cl.PersonCreateUser(cmd.Context(), filter); err != nil {
				return err
			}
			return a.done("Created users for matching persons", map[string]any{"created_users": true, "filter": filter.Filter})
		},
	}
	fo.register(createUser)

	c.AddCommand(list, count, get,
		a.createCmd(res), a.updateCmd(res),
		a.uuidActionCmd("delete", "Delete a person", "person", "Deleted", true, res.delete),
		write(createUser),
		a.historyCmd("person", func(c *client.Client, ctx context.Context, uuid string, o *models.HistoryListOptions) (any, error) {
			return c.PersonHistory(ctx, uuid, o)
		}),
	)
	return c
}

func (a *App) usersCmd() *cobra.Command {
	c := &cobra.Command{Use: "users", Aliases: []string{"user"}, Short: "List and look up users (read-only)"}

	var lf sortOrderFlags
	list := &cobra.Command{
		Use:   "list",
		Short: "List users",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := lf.userOptions()
			if err != nil {
				return err
			}
			cl, err := a.Client()
			if err != nil {
				return err
			}
			if lf.all {
				return streamAll(a, cl.UsersAll(cmd.Context(), opts))
			}
			res, err := cl.UsersList(cmd.Context(), opts)
			if err != nil {
				return err
			}
			return a.print(res.Items)
		},
	}
	lf.register(list, "sort by id or email")

	var byID int
	get := &cobra.Command{
		Use:   "get <uuid> | --id <id>",
		Short: "Get a user by UUID or numeric ID",
		Args:  cobra.MaximumNArgs(1),
		RunE: a.show(func(ctx context.Context, cl *client.Client, args []string) (any, error) {
			if (len(args) == 1) == (byID != 0) {
				return nil, exitcode.Usagef("give either a UUID or --id")
			}
			if byID != 0 {
				return cl.UserGetByID(ctx, byID)
			}
			return cl.UserGet(ctx, args[0])
		}),
	}
	get.Flags().IntVar(&byID, "id", 0, "numeric user ID")

	c.AddCommand(list, get)
	return c
}

// statusValue validates an enum flag value.
func statusValue[T ~string](s, flag string, allowed ...T) (*T, error) {
	if s == "" {
		return nil, nil
	}
	for _, v := range allowed {
		if string(v) == s {
			return &v, nil
		}
	}
	names := make([]string, len(allowed))
	for i, v := range allowed {
		names[i] = string(v)
	}
	return nil, exitcode.Usagef("--%s must be one of: %s", flag, strings.Join(names, ", "))
}
