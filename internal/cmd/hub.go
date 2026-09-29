package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/SeventhingsCompany/customer-api-cli/internal/exitcode"
	"github.com/SeventhingsCompany/customer-api-cli/internal/input"
	"github.com/SeventhingsCompany/customer-api-cli/internal/listflags"
	"github.com/SeventhingsCompany/customer-api-go/client"
	"github.com/SeventhingsCompany/customer-api-go/models"
	"github.com/spf13/cobra"
)

func (a *App) hubCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "hub",
		Aliases: []string{"circularity-hub"},
		Short:   "Circularity hub: resell objects, manage items and orders",
	}
	c.AddCommand(a.hubItemsCmd(), a.hubOrdersCmd())
	return c
}

func (a *App) hubItemsCmd() *cobra.Command {
	c := &cobra.Command{Use: "items", Aliases: []string{"item"}, Short: "Manage circularity hub items (numeric IDs)"}

	var lf listflags.Flags
	list := &cobra.Command{
		Use:   "list",
		Short: "List hub items",
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
			if lf.All {
				return streamAll(a, cl.CircularityHubItemsAll(cmd.Context(), opts))
			}
			items, err := cl.CircularityHubItemsList(cmd.Context(), opts)
			if err != nil {
				return err
			}
			return a.print(items)
		},
	}
	lf.Register(list.Flags(), true)

	get := &cobra.Command{
		Use:   "get <id>",
		Short: "Get a hub item",
		Args:  cobra.ExactArgs(1),
		RunE: a.show(func(ctx context.Context, cl *client.Client, args []string) (any, error) {
			id, err := intArg(args[0], "id")
			if err != nil {
				return nil, err
			}
			return cl.CircularityHubItemGet(ctx, id)
		}),
	}

	var uData string
	var uSets []string
	update := &cobra.Command{
		Use:   "update <id>",
		Short: "Update fields of a hub item (PATCH)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := intArg(args[0], "id")
			if err != nil {
				return err
			}
			body, err := requireBody(uData, uSets, a)
			if err != nil {
				return err
			}
			cl, err := a.Client()
			if err != nil {
				return err
			}
			if err := cl.CircularityHubItemUpdate(cmd.Context(), id, body); err != nil {
				return err
			}
			return a.done(fmt.Sprintf("Updated hub item %d", id), map[string]any{"id": id, "updated": true})
		},
	}
	addBodyFlags(update, &uData, &uSets)

	del := &cobra.Command{
		Use:   "delete <id>",
		Short: "Remove an item from the hub",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := intArg(args[0], "id")
			if err != nil {
				return err
			}
			if err := a.confirm(fmt.Sprintf("delete hub item %d", id)); err != nil {
				return err
			}
			cl, err := a.Client()
			if err != nil {
				return err
			}
			if err := cl.CircularityHubItemDelete(cmd.Context(), id); err != nil {
				return err
			}
			return a.done(fmt.Sprintf("Deleted hub item %d", id), map[string]any{"id": id, "deleted": true})
		},
	}

	var addData string
	var addSpecs []string
	add := &cobra.Command{
		Use:   "add-objects",
		Short: "Offer objects on the circularity hub",
		Long: `Offer objects on the circularity hub. Give each object as
--object <object-uuid>=<category>,<price>, or pass a JSON map
{"<object-uuid>": {"category": "...", "price": "..."}} with --data.
Use "hub items suggest-category" and "suggest-price" to pick values.`,
		Example: `  seventhings hub items add-objects --object <uuid>=furniture,120.00`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			entries := map[string]models.AddObjectEntry{}
			if addData != "" {
				if err := input.Into(addData, nil, a.io.In, &entries); err != nil {
					return err
				}
			}
			for _, s := range addSpecs {
				uuid, rest, ok := strings.Cut(s, "=")
				cat, price, ok2 := strings.Cut(rest, ",")
				if !ok || !ok2 || uuid == "" || cat == "" || price == "" {
					return exitcode.Usagef("--object %q: want <object-uuid>=<category>,<price>", s)
				}
				entries[uuid] = models.AddObjectEntry{Category: cat, Price: price}
			}
			if len(entries) == 0 {
				return exitcode.Usagef("give at least one --object or --data")
			}
			cl, err := a.Client()
			if err != nil {
				return err
			}
			if err := cl.CircularityHubAddObjects(cmd.Context(), entries); err != nil {
				return err
			}
			return a.done(fmt.Sprintf("Added %d object(s) to the hub", len(entries)), map[string]any{"added": len(entries)})
		},
	}
	add.Flags().StringVarP(&addData, "data", "d", "", "JSON map of object UUID to {category, price}; @file or -")
	add.Flags().StringArrayVar(&addSpecs, "object", nil, "<object-uuid>=<category>,<price> (repeatable)")

	var fo filterObjectFlags
	suggestCat := &cobra.Command{
		Use:     "suggest-category",
		Short:   "Suggest hub categories for the objects matching a filter",
		Example: `  seventhings hub items suggest-category --filter 'asset_uuid in <uuid1>,<uuid2>'`,
		Args:    cobra.NoArgs,
		RunE: a.show(func(ctx context.Context, cl *client.Client, _ []string) (any, error) {
			f, err := fo.build(a)
			if err != nil {
				return nil, err
			}
			return cl.CircularityHubSuggestCategory(ctx, f)
		}),
	}
	fo.register(suggestCat)

	var pData string
	var pSets []string
	suggestPrice := &cobra.Command{
		Use:     "suggest-price",
		Short:   "Suggest a resale price",
		Example: `  seventhings hub items suggest-price --set <object-uuid>=furniture`,
		Args:    cobra.NoArgs,
		RunE: a.show(func(ctx context.Context, cl *client.Client, _ []string) (any, error) {
			var in map[string]string
			if err := input.Into(pData, pSets, a.io.In, &in); err != nil {
				return nil, err
			}
			if len(in) == 0 {
				return nil, usageNoBody()
			}
			// Not cl.CircularityHubSuggestRestPrice: it decodes into
			// map[string]string, but the API returns numeric prices
			// (customer-api-go v1.4.0). Decode loosely until the SDK is fixed.
			b, err := json.Marshal(in)
			if err != nil {
				return nil, err
			}
			resp, err := cl.Post(ctx, "circularity-hub/suggest-rest-price", bytes.NewReader(b))
			if err != nil {
				return nil, err
			}
			var out map[string]any
			if err := client.DecodeJSON(resp, &out); err != nil {
				return nil, err
			}
			return out, nil
		}),
	}
	addBodyFlags(suggestPrice, &pData, &pSets)

	c.AddCommand(list, get, write(update), destructive(del), write(add), suggestCat, suggestPrice)
	return c
}

func (a *App) hubOrdersCmd() *cobra.Command {
	c := &cobra.Command{Use: "orders", Aliases: []string{"order"}, Short: "Manage circularity hub orders (numeric IDs)"}

	var lf listflags.Flags
	list := &cobra.Command{
		Use:   "list",
		Short: "List hub orders",
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
			if !lf.All {
				orders, err := cl.CircularityHubOrdersList(cmd.Context(), opts)
				if err != nil {
					return err
				}
				return a.print(orders)
			}
			// The SDK has no iterator for orders; page until a short page.
			per := opts.PerPage
			if per == 0 {
				per = 100
				opts.PerPage = per
			}
			s := a.printer.Stream()
			for page := 1; ; page++ {
				opts.Page = page
				orders, err := cl.CircularityHubOrdersList(cmd.Context(), opts)
				if err != nil {
					_ = s.Close()
					return err
				}
				for _, o := range orders {
					if err := s.Add(o); err != nil {
						return err
					}
				}
				if len(orders) < per {
					return s.Close()
				}
			}
		},
	}
	lf.Register(list.Flags(), true)

	var items []string
	create := &cobra.Command{
		Use:     "create --item <id>...",
		Short:   "Create an order for hub items",
		Example: `  seventhings hub orders create --item 12 --item 15`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, err := csvInts(items, "--item")
			if err != nil {
				return err
			}
			if len(ids) == 0 {
				return exitcode.Usagef("at least one --item is required")
			}
			cl, err := a.Client()
			if err != nil {
				return err
			}
			id, err := cl.CircularityHubOrderCreate(cmd.Context(), ids)
			if err != nil {
				return err
			}
			return a.done(fmt.Sprintf("Created order %d", id), map[string]any{"id": id})
		},
	}
	create.Flags().StringArrayVar(&items, "item", nil, "hub item ID (repeatable or comma-separated)")

	get := &cobra.Command{
		Use:   "get <id>",
		Short: "Get an order",
		Args:  cobra.ExactArgs(1),
		RunE: a.show(func(ctx context.Context, cl *client.Client, args []string) (any, error) {
			id, err := intArg(args[0], "id")
			if err != nil {
				return nil, err
			}
			return cl.CircularityHubOrderGet(ctx, id)
		}),
	}

	var uData string
	var uSets []string
	update := &cobra.Command{
		Use:   "update <id>",
		Short: "Update an order (PATCH), e.g. complete or cancel it",
		Example: `  seventhings hub orders update 7 --set completed:=true
  seventhings hub orders update 7 --set cancelled:=true --set cancellation_reason="Out of stock"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := intArg(args[0], "id")
			if err != nil {
				return err
			}
			body, err := requireBody(uData, uSets, a)
			if err != nil {
				return err
			}
			if body["cancelled"] == true && body["cancellation_reason"] == nil {
				return exitcode.Usagef("cancelling an order requires cancellation_reason (--set cancellation_reason=...)")
			}
			cl, err := a.Client()
			if err != nil {
				return err
			}
			if err := cl.CircularityHubOrderUpdate(cmd.Context(), id, body); err != nil {
				return err
			}
			return a.done(fmt.Sprintf("Updated order %d", id), map[string]any{"id": id, "updated": true})
		},
	}
	addBodyFlags(update, &uData, &uSets)

	c.AddCommand(list, write(create), get, write(update))
	return c
}
