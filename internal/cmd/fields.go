package cmd

import (
	"context"
	"strings"

	"github.com/SeventhingsCompany/customer-api-cli/internal/exitcode"
	"github.com/SeventhingsCompany/customer-api-cli/internal/input"
	"github.com/SeventhingsCompany/customer-api-go/client"
	"github.com/SeventhingsCompany/customer-api-go/models"
	"github.com/spf13/cobra"
)

func (a *App) fieldsCmd() *cobra.Command {
	var template string
	c := &cobra.Command{
		Use:     "fields",
		Aliases: []string{"field-definitions"},
		Short:   "Inspect and manage field definitions (the schema of objects, rooms and persons)",
		Long: `Field definitions describe the fields of a template: asset (objects),
room or person. Use them to learn field keys before creating records, and
"fields missing" to validate a record before sending it.`,
	}
	c.PersistentFlags().StringVarP(&template, "template", "t", "asset", "template: asset, room or person")

	tmpl := func() (models.AssetTrackingTemplate, error) {
		t, err := statusValue(template, "template", models.AssetTrackingTemplateAsset, models.AssetTrackingTemplateRoom, models.AssetTrackingTemplatePerson)
		if err != nil {
			return "", err
		}
		return *t, nil
	}

	list := &cobra.Command{
		Use:   "list",
		Short: "List field definitions",
		Args:  cobra.NoArgs,
		RunE: a.show(func(ctx context.Context, cl *client.Client, _ []string) (any, error) {
			t, err := tmpl()
			if err != nil {
				return nil, err
			}
			return cl.FieldDefinitionsList(ctx, t)
		}),
	}

	mandatory := &cobra.Command{
		Use:   "mandatory",
		Short: "List mandatory fields that callers must supply",
		Args:  cobra.NoArgs,
		RunE: a.show(func(ctx context.Context, cl *client.Client, _ []string) (any, error) {
			t, err := tmpl()
			if err != nil {
				return nil, err
			}
			return cl.MandatoryFieldDefinitions(ctx, t)
		}),
	}

	var mData string
	var mSets []string
	missing := &cobra.Command{
		Use:   "missing",
		Short: "Check a record for missing mandatory fields (exit 5 if any are missing)",
		Example: `  seventhings fields missing --set inventory_name=Laptop
  seventhings fields missing --template person --data @person.json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := tmpl()
			if err != nil {
				return err
			}
			body, err := input.Body(mData, mSets, a.io.In)
			if err != nil {
				return err
			}
			cl, err := a.Client()
			if err != nil {
				return err
			}
			keys, err := cl.MissingMandatoryFields(cmd.Context(), t, body)
			if err != nil {
				return err
			}
			if keys == nil {
				keys = []string{}
			}
			if err := a.print(map[string]any{"missing": keys, "valid": len(keys) == 0}); err != nil {
				return err
			}
			if len(keys) > 0 {
				return &validationError{keys: keys}
			}
			return nil
		},
	}
	addBodyFlags(missing, &mData, &mSets)

	get := &cobra.Command{
		Use:   "get <uuid>",
		Short: "Get a field definition",
		Args:  cobra.ExactArgs(1),
		RunE: a.show(func(ctx context.Context, cl *client.Client, args []string) (any, error) {
			t, err := tmpl()
			if err != nil {
				return nil, err
			}
			return cl.FieldDefinitionGet(ctx, t, args[0])
		}),
	}

	var cData string
	var cSets []string
	create := &cobra.Command{
		Use:     "create",
		Short:   "Create a field definition",
		Example: `  seventhings fields create --set label="Warranty until" --set 'field_type:={"name":"DATE","constraints":[]}'`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := tmpl()
			if err != nil {
				return err
			}
			var in models.CreateFieldDefinition
			if err := input.Into(cData, cSets, a.io.In, &in); err != nil {
				return err
			}
			if in.Label == "" || in.FieldType.Name == "" {
				return exitcode.Usagef("label and field_type.name are required")
			}
			if in.Attributes == nil {
				in.Attributes = []models.FieldAttribute{}
			}
			if in.Relations == nil {
				in.Relations = []models.FieldRelation{}
			}
			if in.FieldType.Constraints == nil {
				in.FieldType.Constraints = []models.FieldValueConstraint{}
			}
			cl, err := a.Client()
			if err != nil {
				return err
			}
			uuid, err := cl.FieldDefinitionCreate(cmd.Context(), t, in)
			if err != nil {
				return err
			}
			return a.done("Created field definition "+uuid, map[string]any{"uuid": uuid})
		},
	}
	addBodyFlags(create, &cData, &cSets)

	var uData string
	var uSets []string
	update := &cobra.Command{
		Use:   "update <uuid>",
		Short: "Update a field definition (fields not given keep their current values)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := tmpl()
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
			cur, err := cl.FieldDefinitionGet(cmd.Context(), t, args[0])
			if err != nil {
				return err
			}
			var in models.UpdateFieldDefinition
			if err := input.Merge(cur, body, &in, nil); err != nil {
				return err
			}
			if err := cl.FieldDefinitionUpdate(cmd.Context(), t, args[0], in); err != nil {
				return err
			}
			return a.done("Updated field definition "+args[0], map[string]any{"uuid": args[0], "updated": true})
		},
	}
	addBodyFlags(update, &uData, &uSets)

	c.AddCommand(list, mandatory, missing, get, write(create), write(update))
	return c
}

// validationError reports missing mandatory fields (exit code 5).
type validationError struct{ keys []string }

func (e *validationError) Error() string {
	return "missing mandatory fields: " + strings.Join(e.keys, ", ")
}

// ExitCode lets exitcode.For map this error.
func (e *validationError) ExitCode() int { return exitcode.Invalid }
