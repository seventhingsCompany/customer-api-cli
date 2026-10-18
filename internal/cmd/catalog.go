package cmd

import (
	"context"
	"iter"
	"maps"

	"github.com/SeventhingsCompany/customer-api-cli/internal/listflags"
	"github.com/SeventhingsCompany/customer-api-go/client"
	"github.com/SeventhingsCompany/customer-api-go/models"
	"github.com/spf13/cobra"
)

func (a *App) catalogItemsCmd() *cobra.Command {
	c := &cobra.Command{
		Use: "catalog-items", Aliases: []string{"catalog-item", "catalog"},
		Short: "Manage product catalog master data",
		Long: `Manage reusable object master data. Requires the product catalog feature
and appropriate permissions. Object field keys use the catalog_object_ prefix;
catalog metadata keys such as id have no prefix. Changes do not affect objects
previously created from a catalog item.`,
	}
	var lf listflags.Flags
	list := &cobra.Command{
		Use: "list", Short: "List catalog items", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts, err := lf.ListOptions()
			if err != nil {
				return err
			}
			cl, err := a.Client()
			if err != nil {
				return err
			}
			if lf.All {
				return streamAll(a, catalogItemsAll(cmd.Context(), cl, opts))
			}
			page, err := cl.CatalogItemsList(cmd.Context(), opts)
			if err != nil {
				return err
			}
			return a.print(page.Items)
		},
	}
	lf.Register(list.Flags(), true)
	get := &cobra.Command{
		Use: "get <uuid>", Short: "Get a catalog item", Args: cobra.ExactArgs(1),
		RunE: a.show(func(ctx context.Context, cl *client.Client, args []string) (any, error) {
			return cl.CatalogItemGet(ctx, args[0])
		}),
	}
	// Create takes a field map, just like objects/rooms. The SDK wraps it in fields.
	create := a.createCmd(fieldResource{
		use: "catalog-items", singular: "catalog item", create: (*client.Client).CatalogItemCreate,
	})
	create.Example = `  seventhings catalog-items create --set catalog_object_inventory_name=Laptop
  seventhings catalog-items create --data @catalog-fields.json`
	var data string
	var sets []string
	update := &cobra.Command{
		Use: "update <uuid>", Short: "Update catalog fields (fields not given keep their current values)",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := requireBody(data, sets, a)
			if err != nil {
				return err
			}
			cl, err := a.Client()
			if err != nil {
				return err
			}
			cur, err := cl.CatalogItemGet(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fields := maps.Clone(cur.Fields)
			if fields == nil {
				fields = models.Fields{}
			}
			maps.Copy(fields, body)
			if err := cl.CatalogItemUpdate(cmd.Context(), args[0], fields); err != nil {
				return err
			}
			return a.done("Updated catalog item "+args[0], map[string]any{"uuid": args[0], "updated": true})
		},
	}
	addBodyFlags(update, &data, &sets)
	c.AddCommand(list, get, create, write(update),
		a.uuidActionCmd("delete", "Delete a catalog item (existing objects are unchanged)", "catalog item", "Deleted", true, (*client.Client).CatalogItemDelete))
	return c
}

// catalogItemsAll follows the SDK's All conventions: page starts at one and
// options are copied. Use the response's page size, since the server may cap it.
func catalogItemsAll(ctx context.Context, cl *client.Client, opts *models.ListOptions) iter.Seq2[models.CatalogItem, error] {
	return func(yield func(models.CatalogItem, error) bool) {
		var o models.ListOptions
		if opts != nil {
			o = *opts
		}
		if o.PerPage <= 0 {
			o.PerPage = 100
		}
		seen := 0
		for o.Page = 1; ; o.Page++ {
			page, err := cl.CatalogItemsList(ctx, &o)
			if err != nil {
				yield(models.CatalogItem{}, err)
				return
			}
			for _, item := range page.Items {
				if !yield(item, nil) {
					return
				}
			}
			seen += len(page.Items)
			perPage := page.PerPage
			if perPage <= 0 {
				perPage = o.PerPage
			}
			if len(page.Items) < perPage || (page.Total > 0 && seen >= page.Total) {
				return
			}
		}
	}
}
