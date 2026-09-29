package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/SeventhingsCompany/customer-api-cli/internal/exitcode"
	"github.com/SeventhingsCompany/customer-api-go/client"
	"github.com/SeventhingsCompany/customer-api-go/models"
	"github.com/spf13/cobra"
)

func (a *App) filesCmd() *cobra.Command {
	c := &cobra.Command{Use: "files", Aliases: []string{"file"}, Short: "Upload, inspect and download files"}

	list := &cobra.Command{
		Use:   "list",
		Short: "List files",
		Args:  cobra.NoArgs,
		RunE: a.show(func(ctx context.Context, cl *client.Client, _ []string) (any, error) {
			return cl.FilesList(ctx)
		}),
	}

	get := &cobra.Command{
		Use:   "get <uuid>",
		Short: "Get file metadata",
		Args:  cobra.ExactArgs(1),
		RunE: a.show(func(ctx context.Context, cl *client.Client, args []string) (any, error) {
			return cl.FileGet(ctx, args[0])
		}),
	}

	var name string
	upload := &cobra.Command{
		Use:   "upload <path>",
		Short: "Upload a file; prints its UUID for use with `objects files add`",
		Example: `  seventhings files upload manual.pdf
  uuid=$(seventhings files upload photo.jpg --jq .uuid --raw)`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := os.Open(args[0])
			if err != nil {
				return exitcode.Usagef("open %s: %v", args[0], err)
			}
			defer func() { _ = f.Close() }()
			if name == "" {
				name = filepath.Base(args[0])
			}
			cl, err := a.Client()
			if err != nil {
				return err
			}
			uuid, err := cl.FileUpload(cmd.Context(), name, f)
			if err != nil {
				return err
			}
			return a.done(fmt.Sprintf("Uploaded %s as %s", name, uuid), map[string]any{"uuid": uuid, "name": name})
		},
	}
	upload.Flags().StringVar(&name, "name", "", "file name to store (default: base name of path)")

	download := func(use, short string, fn func(*client.Client, context.Context, string) ([]byte, error)) *cobra.Command {
		var out string
		d := &cobra.Command{
			Use:   use + " <uuid> --out <path|->",
			Short: short,
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				if out == "" {
					return exitcode.Usagef("--out is required (a file path, or - for stdout)")
				}
				cl, err := a.Client()
				if err != nil {
					return err
				}
				data, err := fn(cl, cmd.Context(), args[0])
				if err != nil {
					return err
				}
				return a.writeBinary(data, out, map[string]any{"uuid": args[0]})
			},
		}
		addOutFlag(d, &out)
		return d
	}

	c.AddCommand(list, get, write(upload),
		download("download", "Download file contents", (*client.Client).FileGetData),
		download("thumbnail", "Download the file's thumbnail", (*client.Client).FileGetThumbnail),
	)
	return c
}

func (a *App) reportsCmd() *cobra.Command {
	c := &cobra.Command{Use: "reports", Aliases: []string{"report"}, Short: "Generate PDF reports"}

	templates := &cobra.Command{
		Use:   "templates",
		Short: "List report templates",
		Args:  cobra.NoArgs,
		RunE: a.show(func(ctx context.Context, cl *client.Client, _ []string) (any, error) {
			return cl.ReportTemplatesList(ctx)
		}),
	}

	var tmpl, out string
	var objects []string
	create := &cobra.Command{
		Use:     "create --template <uuid> --object <uuid>... --out <file.pdf|->",
		Short:   "Render a PDF report for objects",
		Example: `  seventhings reports create --template <template-uuid> --object <uuid> --object <uuid> --out report.pdf`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if tmpl == "" || len(objects) == 0 {
				return exitcode.Usagef("--template and at least one --object are required")
			}
			if out == "" {
				return exitcode.Usagef("--out is required (a file path, or - for stdout)")
			}
			cl, err := a.Client()
			if err != nil {
				return err
			}
			pdf, err := cl.ReportCreate(cmd.Context(), models.CreateReport{ReportTemplateUUID: tmpl, ObjectUUIDs: objects})
			if err != nil {
				return err
			}
			return a.writeBinary(pdf, out, map[string]any{"template": tmpl, "objects": len(objects)})
		},
	}
	create.Flags().StringVar(&tmpl, "template", "", "report template UUID (see `reports templates`)")
	create.Flags().StringSliceVar(&objects, "object", nil, "object UUID (repeatable or comma-separated)")
	addOutFlag(create, &out)

	c.AddCommand(templates, create)
	return c
}
