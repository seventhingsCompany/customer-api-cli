package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/SeventhingsCompany/customer-api-cli/internal/exitcode"
	"github.com/SeventhingsCompany/customer-api-cli/internal/input"
	"github.com/SeventhingsCompany/customer-api-cli/internal/listflags"
	"github.com/SeventhingsCompany/customer-api-go/client"
	"github.com/SeventhingsCompany/customer-api-go/models"
	"github.com/spf13/cobra"
)

// taskInputFlags are convenience flags layered over --data/--set. They map to
// the JSON keys of models.CreateTask / models.UpdateTask.
type taskInputFlags struct {
	data      string
	sets      []string
	title     string
	deadline  string
	comment   string
	assignees []string
	objects   []string
	notify    bool
}

func (f *taskInputFlags) register(c *cobra.Command) {
	addBodyFlags(c, &f.data, &f.sets)
	c.Flags().StringVar(&f.title, "title", "", "task title")
	c.Flags().StringVar(&f.deadline, "deadline", "", "deadline (ISO 8601, e.g. 2026-12-31 or 2026-12-31T17:00:00Z)")
	c.Flags().StringVar(&f.comment, "comment", "", "comment")
	c.Flags().StringArrayVar(&f.assignees, "assignee", nil, "assignee user UUID (repeatable)")
	c.Flags().StringArrayVar(&f.objects, "object", nil, "referenced object UUID (repeatable)")
	c.Flags().BoolVar(&f.notify, "notify", false, "notify assignees")
}

func (f *taskInputFlags) body(a *App, cmd *cobra.Command) (map[string]any, error) {
	body, err := input.Body(f.data, f.sets, a.io.In)
	if err != nil {
		return nil, err
	}
	fl := cmd.Flags()
	if fl.Changed("title") {
		body["title"] = f.title
	}
	if fl.Changed("deadline") {
		body["deadline"] = f.deadline
	}
	if fl.Changed("comment") {
		body["comment"] = f.comment
	}
	if fl.Changed("assignee") {
		body["assignees"] = f.assignees
	}
	if fl.Changed("object") {
		body["references"] = assetRefs(f.objects)
	}
	if fl.Changed("notify") {
		body["notify"] = f.notify
	}
	if len(body) == 0 {
		return nil, usageNoBody()
	}
	return body, nil
}

func assetRefs(uuids []string) []map[string]string {
	refs := make([]map[string]string, len(uuids))
	for i, u := range uuids {
		refs[i] = map[string]string{"type": "asset", "uuid": u}
	}
	return refs
}

func (a *App) tasksCmd() *cobra.Command {
	c := &cobra.Command{Use: "tasks", Aliases: []string{"task"}, Short: "Manage tasks"}

	var status, from, to, assignee, author, refType string
	list := &cobra.Command{
		Use:   "list",
		Short: "List tasks (not paginated; the API returns up to 10,000)",
		Args:  cobra.NoArgs,
		RunE: a.show(func(ctx context.Context, cl *client.Client, _ []string) (any, error) {
			st, err := statusValue(status, "status", models.TaskStatusOpen, models.TaskStatusClosed)
			if err != nil {
				return nil, err
			}
			rt, err := statusValue(refType, "reference-type", models.TaskReferenceTypeAsset)
			if err != nil {
				return nil, err
			}
			return cl.TasksList(ctx, &models.TaskListOptions{
				Status: st, ReferenceType: rt,
				DeadlineFrom: optStr(from), DeadlineTo: optStr(to), Assignee: optStr(assignee), Author: optStr(author),
			})
		}),
	}
	list.Flags().StringVar(&status, "status", "", "open or closed")
	list.Flags().StringVar(&from, "deadline-from", "", "deadline on or after (ISO 8601)")
	list.Flags().StringVar(&to, "deadline-to", "", "deadline on or before (ISO 8601)")
	list.Flags().StringVar(&assignee, "assignee", "", "assignee user UUID")
	list.Flags().StringVar(&author, "author", "", "author user UUID")
	list.Flags().StringVar(&refType, "reference-type", "", "reference type (asset)")

	get := &cobra.Command{
		Use:   "get <uuid>",
		Short: "Get a task",
		Args:  cobra.ExactArgs(1),
		RunE: a.show(func(ctx context.Context, cl *client.Client, args []string) (any, error) {
			return cl.TaskGet(ctx, args[0])
		}),
	}

	var cf taskInputFlags
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a task",
		Long: `Create a task. The API requires a title, a deadline, at least one assignee
and at least one referenced object.`,
		Example: `  seventhings tasks create --title "Check fire extinguisher" --deadline 2026-12-31 \
    --assignee <user-uuid> --object <object-uuid>
  seventhings tasks create --data @task.json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := cf.body(a, cmd)
			if err != nil {
				return err
			}
			var in models.CreateTask
			if err := input.Decode(body, &in); err != nil {
				return err
			}
			if in.Title == "" {
				return exitcode.Usagef("--title is required")
			}
			// Both are required by the API although the schema allows empty lists.
			if len(in.Assignees) == 0 {
				return exitcode.Usagef("at least one --assignee (user UUID) is required; see `seventhings users list`")
			}
			if len(in.References) == 0 {
				return exitcode.Usagef("at least one --object (object UUID) is required")
			}
			if in.Deadline == nil || *in.Deadline == "" {
				return exitcode.Usagef("--deadline is required")
			}
			emptyTaskSlices(&in.Assignees, &in.References, &in.Reminders)
			cl, err := a.Client()
			if err != nil {
				return err
			}
			uuid, err := cl.TaskCreate(cmd.Context(), in)
			if err != nil {
				return err
			}
			return a.done("Created task "+uuid, map[string]any{"uuid": uuid})
		},
	}
	cf.register(create)

	var uf taskInputFlags
	update := &cobra.Command{
		Use:   "update <uuid>",
		Short: "Update a task (fields not given keep their current values)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := uf.body(a, cmd)
			if err != nil {
				return err
			}
			cl, err := a.Client()
			if err != nil {
				return err
			}
			cur, err := cl.TaskGet(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			var in models.UpdateTask
			if err := input.Merge(cur, body, &in, input.AttachmentUUIDs); err != nil {
				return err
			}
			emptyTaskSlices(&in.Assignees, &in.References, &in.Reminders)
			if err := cl.TaskUpdate(cmd.Context(), args[0], in); err != nil {
				return err
			}
			return a.done("Updated task "+args[0], map[string]any{"uuid": args[0], "updated": true})
		},
	}
	uf.register(update)

	setStatus := &cobra.Command{
		Use:   "set-status <uuid> <open|closed>",
		Short: "Open or close a task",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := statusValue(args[1], "status", models.TaskStatusOpen, models.TaskStatusClosed)
			if err != nil {
				return err
			}
			cl, err := a.Client()
			if err != nil {
				return err
			}
			if err := cl.TaskUpdateStatus(cmd.Context(), args[0], *st); err != nil {
				return err
			}
			return a.done(fmt.Sprintf("Task %s is now %s", args[0], *st), map[string]any{"uuid": args[0], "status": *st})
		},
	}

	c.AddCommand(list, get, write(create), write(update), write(setStatus),
		a.uuidActionCmd("delete", "Delete a task", "task", "Deleted", true, (*client.Client).TaskDelete),
		a.historyCmd("task", func(c *client.Client, ctx context.Context, uuid string, o *models.HistoryListOptions) (any, error) {
			return c.TaskHistory(ctx, uuid, o)
		}),
	)
	return c
}

// emptyTaskSlices sends [] instead of null for list fields the user omitted.
func emptyTaskSlices(assignees *[]string, refs *[]models.TaskReferenceInput, reminders *[]models.TimeInterval) {
	if *assignees == nil {
		*assignees = []string{}
	}
	if *refs == nil {
		*refs = []models.TaskReferenceInput{}
	}
	if *reminders == nil {
		*reminders = []models.TimeInterval{}
	}
}

// rentalInputFlags are convenience flags for rental cases.
type rentalInputFlags struct {
	data               string
	sets               []string
	title, renter      string
	issueDate, dueDate string
	comment, respUser  string
	objects            []string
}

func (f *rentalInputFlags) register(c *cobra.Command) {
	addBodyFlags(c, &f.data, &f.sets)
	c.Flags().StringVar(&f.title, "title", "", "title")
	c.Flags().StringVar(&f.renter, "renter", "", `renter as "user:<user-uuid>" or "plain:<name>"`)
	c.Flags().StringVar(&f.issueDate, "issue-date", "", "issue date (ISO 8601)")
	c.Flags().StringVar(&f.dueDate, "due-date", "", "due date (ISO 8601)")
	c.Flags().StringVar(&f.comment, "comment", "", "comment")
	c.Flags().StringVar(&f.respUser, "responsible", "", "responsible user UUID")
	c.Flags().StringArrayVar(&f.objects, "object", nil, "rented object UUID (repeatable)")
}

func (f *rentalInputFlags) body(a *App, cmd *cobra.Command) (map[string]any, error) {
	body, err := input.Body(f.data, f.sets, a.io.In)
	if err != nil {
		return nil, err
	}
	fl := cmd.Flags()
	for flag, key := range map[string]string{"title": "title", "issue-date": "issue_date", "due-date": "due_date", "comment": "comment", "responsible": "responsible_user_uuid"} {
		if fl.Changed(flag) {
			v, _ := fl.GetString(flag)
			body[key] = v
		}
	}
	if fl.Changed("renter") {
		typ, val, ok := strings.Cut(f.renter, ":")
		if !ok || (typ != "user" && typ != "plain") || val == "" {
			return nil, exitcode.Usagef(`--renter must be "user:<user-uuid>" or "plain:<name>"`)
		}
		body["renter"] = map[string]string{"type": typ, "value": val}
	}
	if fl.Changed("object") {
		body["references"] = assetRefs(f.objects)
	}
	if len(body) == 0 {
		return nil, usageNoBody()
	}
	return body, nil
}

func (a *App) rentalCasesCmd() *cobra.Command {
	c := &cobra.Command{Use: "rental-cases", Aliases: []string{"rental-case", "rentals"}, Short: "Manage rental cases"}

	var lf listflags.Flags
	list := &cobra.Command{
		Use:   "list",
		Short: "List rental cases",
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
				return streamAll(a, cl.RentalCasesAll(cmd.Context(), opts))
			}
			items, err := cl.RentalCasesList(cmd.Context(), opts)
			if err != nil {
				return err
			}
			return a.print(items)
		},
	}
	lf.Register(list.Flags(), true)

	get := &cobra.Command{
		Use:   "get <uuid>",
		Short: "Get a rental case",
		Args:  cobra.ExactArgs(1),
		RunE: a.show(func(ctx context.Context, cl *client.Client, args []string) (any, error) {
			return cl.RentalCaseGet(ctx, args[0])
		}),
	}

	var cf rentalInputFlags
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a rental case",
		Long: `Create a rental case. The API requires a title, a renter, at least one
object, issue and due dates, and a responsible user.`,
		Example: `  seventhings rental-cases create --title "Beamer for workshop" --renter user:<user-uuid> \
    --object <object-uuid> --issue-date 2026-10-01 --due-date 2026-10-08 --responsible <user-uuid>`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := cf.body(a, cmd)
			if err != nil {
				return err
			}
			var in models.CreateRentalCase
			if err := input.Decode(body, &in); err != nil {
				return err
			}
			// The API requires all of these although the schema suggests otherwise.
			switch {
			case in.Title == "":
				return exitcode.Usagef("--title is required")
			case in.Renter == nil:
				return exitcode.Usagef(`--renter is required ("user:<user-uuid>" or "plain:<name>")`)
			case len(in.References) == 0:
				return exitcode.Usagef("at least one --object is required")
			case in.IssueDate == "" || in.DueDate == "":
				return exitcode.Usagef("--issue-date and --due-date are required")
			case in.ResponsibleUserUUID == "":
				return exitcode.Usagef("--responsible (user UUID) is required")
			}
			if in.References == nil {
				in.References = []models.RentalCaseReferenceInput{}
			}
			if in.Attachments == nil {
				in.Attachments = []string{}
			}
			cl, err := a.Client()
			if err != nil {
				return err
			}
			uuid, err := cl.RentalCaseCreate(cmd.Context(), in)
			if err != nil {
				return err
			}
			return a.done("Created rental case "+uuid, map[string]any{"uuid": uuid})
		},
	}
	cf.register(create)

	var uf rentalInputFlags
	update := &cobra.Command{
		Use:   "update <uuid>",
		Short: "Update a rental case (fields not given keep their current values)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := uf.body(a, cmd)
			if err != nil {
				return err
			}
			cl, err := a.Client()
			if err != nil {
				return err
			}
			cur, err := cl.RentalCaseGet(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			var in models.UpdateRentalCase
			if err := input.Merge(cur, body, &in, input.AttachmentUUIDs); err != nil {
				return err
			}
			if in.Attachments == nil {
				in.Attachments = []string{}
			}
			if err := cl.RentalCaseUpdate(cmd.Context(), args[0], in); err != nil {
				return err
			}
			return a.done("Updated rental case "+args[0], map[string]any{"uuid": args[0], "updated": true})
		},
	}
	uf.register(update)

	c.AddCommand(list, get, write(create), write(update),
		a.uuidActionCmd("delete", "Delete a rental case", "rental case", "Deleted", true, (*client.Client).RentalCaseDelete),
		a.historyCmd("rental case", func(c *client.Client, ctx context.Context, uuid string, o *models.HistoryListOptions) (any, error) {
			return c.RentalCaseHistory(ctx, uuid, o)
		}),
	)
	return c
}
