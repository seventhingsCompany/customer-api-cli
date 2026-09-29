package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/SeventhingsCompany/customer-api-cli/internal/exitcode"
	"github.com/SeventhingsCompany/customer-api-go/client"
	"github.com/SeventhingsCompany/customer-api-go/models"
	"github.com/spf13/cobra"
)

func usageNoBody() error {
	return exitcode.Usagef("no fields given; use --set key=value or --data")
}

func (a *App) objectsCmd() *cobra.Command {
	c := a.fieldResourceCmd(fieldResource{
		use: "objects", singular: "object", aliases: []string{"object", "obj"},
		list:   (*client.Client).ObjectsList,
		all:    (*client.Client).ObjectsAll,
		count:  (*client.Client).ObjectsCount,
		get:    (*client.Client).ObjectGet,
		create: (*client.Client).ObjectCreate,
		update: func(c *client.Client, ctx context.Context, uuid string, f map[string]any) (map[string]any, error) {
			return nil, c.ObjectPatch(ctx, uuid, f)
		},
		delete: (*client.Client).ObjectDelete,
		history: func(c *client.Client, ctx context.Context, uuid string, o *models.HistoryListOptions) (any, error) {
			return c.ObjectHistory(ctx, uuid, o)
		},
	})

	// get also accepts --barcode.
	get, _, _ := c.Find([]string{"get"})
	var barcode string
	get.Use = "get <uuid> | --barcode <code>"
	get.Short = "Get an object by UUID or barcode (barcode lookup includes archived objects)"
	get.Args = cobra.MaximumNArgs(1)
	get.Flags().StringVar(&barcode, "barcode", "", "look up by scancode instead of UUID (pass unescaped)")
	get.RunE = func(cmd *cobra.Command, args []string) error {
		if (len(args) == 1) == (barcode != "") {
			return exitcode.Usagef("give either a UUID or --barcode")
		}
		cl, err := a.Client()
		if err != nil {
			return err
		}
		var res map[string]any
		if barcode != "" {
			res, err = cl.ObjectGetByBarcode(cmd.Context(), barcode)
		} else {
			res, err = cl.ObjectGet(cmd.Context(), args[0])
		}
		if err != nil {
			return err
		}
		return a.print(res)
	}

	c.AddCommand(
		a.uuidActionCmd("archive", "Archive an object", "object", "Archived", true, (*client.Client).ObjectArchive),
		a.uuidActionCmd("unarchive", "Unarchive an object", "object", "Unarchived", false, (*client.Client).ObjectUnarchive),
		a.objectFilesCmd(),
	)
	return c
}

func (a *App) objectFilesCmd() *cobra.Command {
	c := &cobra.Command{Use: "files", Short: "Attach or detach files on an object"}
	mk := func(verb, short string, fn func(*client.Client, context.Context, string, []models.FileAttachment) (*client.Response, error)) *cobra.Command {
		var specs []string
		sub := &cobra.Command{
			Use:   verb + " <object-uuid> --file <field-key>=<file-uuid>...",
			Short: short,
			Long: short + `. The API answers 207 when only some attachments succeed; the
per-item result is printed and the exit code is 8.`,
			Example: fmt.Sprintf("  seventhings objects files %s <uuid> --file photos=<file-uuid> --file manual=<file-uuid>", verb),
			Args:    cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				if len(specs) == 0 {
					return exitcode.Usagef("at least one --file field-key=file-uuid is required")
				}
				atts := make([]models.FileAttachment, 0, len(specs))
				for _, s := range specs {
					k, v, ok := strings.Cut(s, "=")
					if !ok || k == "" || v == "" {
						return exitcode.Usagef("--file %q: want field-key=file-uuid", s)
					}
					atts = append(atts, models.FileAttachment{FieldKey: k, FileUUID: v})
				}
				if verb == "remove" {
					if err := a.confirm(fmt.Sprintf("Remove %d file(s) from object %s", len(atts), args[0])); err != nil {
						return err
					}
				}
				cl, err := a.Client()
				if err != nil {
					return err
				}
				resp, err := fn(cl, cmd.Context(), args[0], atts)
				if err != nil {
					return err
				}
				if a.dryRun {
					return nil
				}
				var result any = map[string]any{"uuid": args[0], "status": resp.StatusCode}
				if len(resp.Body) > 0 {
					var parsed any
					if json.Unmarshal(resp.Body, &parsed) == nil {
						result = parsed
					}
				}
				if err := a.printer.Print(result); err != nil {
					return err
				}
				if resp.StatusCode == 207 {
					return &exitcode.PartialError{}
				}
				return nil
			},
		}
		sub.Flags().StringArrayVar(&specs, "file", nil, "attachment as field-key=file-uuid (repeatable)")
		if verb == "remove" {
			return destructive(sub)
		}
		return write(sub)
	}
	c.AddCommand(
		mk("add", "Attach uploaded files to an object's file fields", (*client.Client).ObjectAddFiles),
		mk("remove", "Detach files from an object's file fields", (*client.Client).ObjectRemoveFiles),
	)
	return c
}
