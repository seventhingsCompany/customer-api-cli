package cmd

import (
	"context"
	"fmt"

	"github.com/SeventhingsCompany/customer-api-cli/internal/duplicate"
	"github.com/SeventhingsCompany/customer-api-cli/internal/exitcode"
	"github.com/SeventhingsCompany/customer-api-cli/internal/input"
	"github.com/SeventhingsCompany/customer-api-go/client"
	"github.com/spf13/cobra"
)

func (a *App) duplicateCmd(kind duplicate.Kind, create func(*client.Client, context.Context, map[string]any) (string, error)) *cobra.Command {
	var data string
	var sets []string
	c := &cobra.Command{
		Use: "duplicate <uuid>", Short: fmt.Sprintf("Duplicate a %s with optional field overrides", kind),
		Long: fmt.Sprintf(`Create a new %s from the source's writable scalar fields. IDs, audit
fields, barcodes, attachments, relationship lists and calculated fields are not
copied. Use --set or --data to override fields. Objects require a new barcode.
The source is unchanged. --dry-run reads the source but does not create a copy.`, kind),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			changes, err := input.Body(data, sets, a.io.In)
			if err != nil {
				return err
			}
			cl, err := a.Client()
			if err != nil {
				return err
			}
			draft, err := duplicate.Load(cmd.Context(), cl, kind, args[0])
			if err != nil {
				return err
			}
			body, err := draft.Body(changes)
			if err != nil {
				return exitcode.Usagef("%s", err)
			}
			id, err := create(cl, cmd.Context(), body)
			if err != nil {
				return err
			}
			return a.done(fmt.Sprintf("Duplicated %s as %s", kind, id), map[string]any{"uuid": id})
		},
	}
	addBodyFlags(c, &data, &sets)
	return write(c)
}
