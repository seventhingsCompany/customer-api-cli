package cmd

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/SeventhingsCompany/customer-api-cli/internal/exitcode"
	"github.com/SeventhingsCompany/customer-api-cli/internal/ratelimit"
	"github.com/spf13/cobra"
)

// Annotation keys used by `describe`.
const (
	annDestructive = "destructive"
	annWrite       = "write"
	annAuth        = "auth" // "none" for commands that need no login
)

// Execute runs the CLI and returns the process exit code.
func Execute(ctx context.Context, args []string, stdio IO, getenv func(string) string, build BuildInfo) int {
	a := newApp(stdio, getenv, build)
	root := a.rootCmd()
	root.SetArgs(args)
	root.SetIn(stdio.In)
	root.SetOut(stdio.Out)
	root.SetErr(stdio.Err)

	err := root.ExecuteContext(ctx)
	if err == nil {
		return exitcode.OK
	}
	err = classifyCobraError(err)
	a.printError(err)
	return exitcode.For(err)
}

func (a *App) rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "seventhings",
		Short: "Command-line interface for the seventhings customer API",
		Long: `seventhings is the command-line interface for the seventhings customer API.

Two modes:
  interactive  for humans in a terminal: tables and confirmation prompts
  agent        for scripts, CI and AI agents: JSON only, never prompts,
               JSON errors on stderr, stable exit codes

Agent mode is selected with --agent, SEVENTHINGS_MODE=agent, CI=true, or
automatically when stdin or stdout is not a terminal.
Run "seventhings describe" for a machine-readable command catalog.`,
		SilenceErrors: true,
		SilenceUsage:  true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if !cmd.Flags().Changed("rate-limit") {
				a.rateLimit = -1
			}
			return a.setup()
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if !a.interactive() {
				return exitcode.Usagef("no command given; run `seventhings describe` or `seventhings --help`")
			}
			return a.runTUI(cmd.Context())
		},
	}
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &exitcode.UsageError{Msg: err.Error()}
	})

	pf := root.PersistentFlags()
	pf.StringVarP(&a.profileFlag, "profile", "p", "", "config profile (env SEVENTHINGS_PROFILE)")
	pf.StringVar(&a.urlFlag, "url", "", "instance URL, e.g. https://acme.seventhings.com (env SEVENTHINGS_BASE_URL)")
	pf.StringVarP(&a.outputFlag, "output", "o", "", "output format: json, ndjson, yaml, table (default: table interactive, json in agent mode)")
	pf.StringVar(&a.jqFlag, "jq", "", "filter output with a jq expression")
	pf.BoolVarP(&a.rawFlag, "raw", "r", false, "print string results without quotes (like jq -r)")
	pf.StringSliceVar(&a.fieldsFlag, "fields", nil, "only include these top-level fields (comma-separated)")
	pf.BoolVar(&a.agentFlag, "agent", false, "force agent mode (env SEVENTHINGS_MODE=agent)")
	pf.BoolVarP(&a.yes, "yes", "y", false, "confirm destructive actions without prompting")
	pf.BoolVarP(&a.quiet, "quiet", "q", false, "suppress success messages")
	pf.BoolVar(&a.debug, "debug", false, "log HTTP requests to stderr (tokens are never logged)")
	pf.BoolVar(&a.dryRun, "dry-run", false, "print write requests instead of sending them")
	pf.IntVar(&a.rateLimit, "rate-limit", ratelimit.DefaultPerMinute, "max requests per minute, 0 disables (env SEVENTHINGS_RATE_LIMIT)")

	root.AddCommand(
		a.authCmd(),
		a.configCmd(),
		a.pingCmd(),
		a.apiCmd(),
		a.objectsCmd(),
		a.roomsCmd(),
		a.locationsCmd(),
		a.personsCmd(),
		a.usersCmd(),
		a.tasksCmd(),
		a.rentalCasesCmd(),
		a.filesCmd(),
		a.fieldsCmd(),
		a.hubCmd(),
		a.reportsCmd(),
		a.describeCmd(),
		a.uiCmd(),
		a.versionCmd(),
	)
	_ = root.RegisterFlagCompletionFunc("output", cobra.FixedCompletions(
		[]string{"json", "ndjson", "yaml", "table"}, cobra.ShellCompDirectiveNoFileComp))
	// Enum flags on subcommands complete their allowed values.
	enums := map[string][]string{
		"template":       {"asset", "room", "person"},
		"status":         {"open", "closed"},
		"reference-type": {"asset"},
		"order":          {"asc", "desc"},
		"sso":            {"azure", "google", "onelogin"},
		"app-target":     {"web", "mobile"},
	}
	walk(root, func(c *cobra.Command) {
		for name, vals := range enums {
			if c.Flags().Lookup(name) != nil || c.PersistentFlags().Lookup(name) != nil {
				_ = c.RegisterFlagCompletionFunc(name, cobra.FixedCompletions(vals, cobra.ShellCompDirectiveNoFileComp))
			}
		}
	})
	// Every subcommand's arg validation errors are usage errors.
	walk(root, func(c *cobra.Command) {
		if c.Args != nil {
			inner := c.Args
			c.Args = func(cmd *cobra.Command, args []string) error {
				if err := inner(cmd, args); err != nil {
					return &exitcode.UsageError{Msg: err.Error()}
				}
				return nil
			}
		}
	})
	return root
}

func walk(c *cobra.Command, fn func(*cobra.Command)) {
	fn(c)
	for _, sub := range c.Commands() {
		walk(sub, fn)
	}
}

// classifyCobraError maps cobra's plain errors (unknown command, required
// flags) to usage errors.
func classifyCobraError(err error) error {
	if _, ok := errors.AsType[*exitcode.UsageError](err); ok {
		return err
	}
	msg := err.Error()
	for _, p := range []string{"unknown command", "unknown flag", "unknown shorthand", "required flag", "invalid argument", "accepts ", "requires "} {
		if strings.Contains(msg, p) {
			return &exitcode.UsageError{Msg: msg}
		}
	}
	return err
}

func annotate(c *cobra.Command, kv ...string) *cobra.Command {
	if c.Annotations == nil {
		c.Annotations = map[string]string{}
	}
	for i := 0; i+1 < len(kv); i += 2 {
		c.Annotations[kv[i]] = kv[i+1]
	}
	return c
}

func destructive(c *cobra.Command) *cobra.Command {
	return annotate(c, annDestructive, "true", annWrite, "true")
}

func write(c *cobra.Command) *cobra.Command { return annotate(c, annWrite, "true") }

// DocsRoot returns the command tree for generating man pages and
// completions. It is never executed.
func DocsRoot() *cobra.Command {
	a := newApp(IO{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}, func(string) string { return "" }, BuildInfo{})
	root := a.rootCmd()
	root.DisableAutoGenTag = true
	return root
}
