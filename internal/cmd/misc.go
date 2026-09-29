package cmd

import (
	"fmt"
	"runtime"
	"sort"
	"strings"

	"github.com/SeventhingsCompany/customer-api-cli/internal/exitcode"
	"github.com/SeventhingsCompany/customer-api-cli/internal/listflags"
	"github.com/SeventhingsCompany/customer-api-cli/internal/output"
	"github.com/SeventhingsCompany/customer-api-cli/internal/ratelimit"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func (a *App) pingCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "ping",
		Short: "Check that the instance is reachable (no login needed)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := a.Client()
			if err != nil {
				return err
			}
			res, err := cl.Ping(cmd.Context())
			if err != nil {
				return err
			}
			return a.print(res)
		},
	}
	return annotate(c, annAuth, "none")
}

func (a *App) versionCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.print(map[string]any{
				"version": a.build.Version, "commit": a.build.Commit, "date": a.build.Date,
				"go": runtime.Version(), "platform": runtime.GOOS + "/" + runtime.GOARCH,
			})
		},
	}
	return annotate(c, annAuth, "none")
}

type flagDoc struct {
	Name      string `json:"name"`
	Shorthand string `json:"shorthand,omitempty"`
	Type      string `json:"type"`
	Default   string `json:"default,omitempty"`
	Usage     string `json:"usage"`
	Required  bool   `json:"required,omitempty"`
}

type commandDoc struct {
	Command      string    `json:"command"`
	Usage        string    `json:"usage"`
	Summary      string    `json:"summary"`
	Description  string    `json:"description,omitempty"`
	Examples     string    `json:"examples,omitempty"`
	Write        bool      `json:"write"`
	Destructive  bool      `json:"destructive"`
	RequiresAuth bool      `json:"requires_auth"`
	Flags        []flagDoc `json:"flags,omitempty"`
}

func (a *App) describeCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "describe",
		Short: "Show a command overview, or a full catalog for agents",
		Long: `Show a compact command overview in an interactive terminal.

In agent mode, print a JSON catalog of every command with its flags, plus exit codes,
filter operators, environment variables and the agent-mode contract. Agents
should read this instead of parsing --help.

Use --agent or --output json for the full catalog. --jq and --fields also select
the full catalog, so filters work consistently in either mode.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			root := cmd.Root()
			var cmds []commandDoc
			walk(root, func(c *cobra.Command) {
				if c == root || !c.Runnable() || c.Hidden || c.Name() == "help" || strings.HasPrefix(c.CommandPath(), "seventhings completion") {
					return
				}
				d := commandDoc{
					Command:      strings.TrimPrefix(c.CommandPath(), "seventhings "),
					Usage:        c.UseLine(),
					Summary:      c.Short,
					Description:  c.Long,
					Examples:     c.Example,
					Write:        c.Annotations[annWrite] == "true",
					Destructive:  c.Annotations[annDestructive] == "true",
					RequiresAuth: !inherited(c, annAuth, "none"),
				}
				c.LocalNonPersistentFlags().VisitAll(func(f *pflag.Flag) {
					d.Flags = append(d.Flags, docFlag(f))
				})
				cmds = append(cmds, d)
			})
			if a.interactive() && a.outputFlag == "" && a.jqFlag == "" && len(a.fieldsFlag) == 0 && !a.rawFlag {
				return a.describeOverview(cmds)
			}
			// Explicit formats and projections get structured data, never the
			// abbreviated nested cells of the interactive table default.
			if a.outputFlag == "" {
				a.printer.Format = output.JSON
			}

			var global []flagDoc
			root.PersistentFlags().VisitAll(func(f *pflag.Flag) { global = append(global, docFlag(f)) })

			ops := make([]string, 0, len(listflags.Operators))
			for op := range listflags.Operators {
				ops = append(ops, op)
			}
			sort.Strings(ops)

			return a.printer.Print(map[string]any{
				"name":    "seventhings",
				"version": a.build.Version,
				"agent_mode": map[string]any{
					"enabled_by": []string{"--agent", "SEVENTHINGS_MODE=agent", "CI=true", "stdin or stdout not a terminal"},
					"guarantees": []string{
						"stdout carries only data: JSON by default, NDJSON for --all",
						"never prompts; missing input is exit code 2",
						"destructive commands require --yes",
						`errors are one JSON line on stderr: {"error":{"code","exit_code","message","status","body"}}`,
						"--dry-run prints write requests as JSON instead of sending them",
					},
				},
				"environment": map[string]string{
					"SEVENTHINGS_BASE_URL":         "instance URL",
					"SEVENTHINGS_TOKEN":            "access token (no refresh)",
					"SEVENTHINGS_USERNAME":         "username for non-interactive login (with SEVENTHINGS_PASSWORD)",
					"SEVENTHINGS_PASSWORD":         "password for non-interactive login",
					"SEVENTHINGS_CLIENT_ID":        "OAuth client ID",
					"SEVENTHINGS_PROFILE":          "profile name",
					"SEVENTHINGS_MODE":             "agent or interactive",
					"SEVENTHINGS_RATE_LIMIT":       "requests per minute (default 200, 0 disables)",
					"SEVENTHINGS_CONFIG_DIR":       "config directory",
					"SEVENTHINGS_CREDENTIAL_STORE": "keyring or file",
					"SEVENTHINGS_IMAGES":           "picture rendering in the UI: auto, kitty, iterm2, sixel, blocks, off",
				},
				"rate_limit": map[string]any{
					"default_per_minute": ratelimit.DefaultPerMinute,
					"burst":              ratelimit.DefaultBurst,
					"retries_on_429":     ratelimit.DefaultMaxRetries,
				},
				"output_formats":   output.Formats,
				"filter_operators": ops,
				"exit_codes":       exitcode.All,
				"global_flags":     global,
				"commands":         cmds,
			})
		},
	}
	return annotate(c, annAuth, "none")
}

// describeOverview groups subcommands into a small, untruncated human menu.
func (a *App) describeOverview(commands []commandDoc) error {
	groups := map[string][]string{}
	width := 0
	for _, command := range commands {
		group, action := command.Command, command.Summary
		if i := strings.LastIndexByte(group, ' '); i >= 0 {
			group, action = group[:i], group[i+1:]
		}
		groups[group] = append(groups[group], action)
		width = max(width, len(group))
	}
	var names []string
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)
	var text strings.Builder
	text.WriteString("seventhings — command overview\n\n")
	for _, name := range names {
		sort.Strings(groups[name])
		line := strings.Join(groups[name], ", ")
		prefix := fmt.Sprintf("  %-*s  ", width, name)
		indent := strings.Repeat(" ", len(prefix))
		// Wrap between words instead of shortening command names or lists.
		for len(prefix)+len(line) > 88 {
			cut := strings.LastIndexByte(line[:max(88-len(prefix), 1)], ' ')
			if cut <= 0 {
				break
			}
			text.WriteString(prefix + line[:cut] + "\n")
			line, prefix = line[cut+1:], indent
		}
		text.WriteString(prefix + line + "\n")
	}
	text.WriteString("\nDetails:   seventhings <command> --help\nFull JSON: seventhings describe --agent\n")
	_, err := fmt.Fprint(a.io.Out, text.String())
	return err
}

func inherited(c *cobra.Command, key, val string) bool {
	for ; c != nil; c = c.Parent() {
		if v, ok := c.Annotations[key]; ok {
			return v == val
		}
	}
	return false
}

func docFlag(f *pflag.Flag) flagDoc {
	d := flagDoc{Name: f.Name, Shorthand: f.Shorthand, Type: f.Value.Type(), Usage: f.Usage}
	if f.DefValue != "" && f.DefValue != "false" && f.DefValue != "[]" && f.DefValue != "0" {
		d.Default = f.DefValue
	}
	if ann, ok := f.Annotations[cobra.BashCompOneRequiredFlag]; ok && len(ann) > 0 && ann[0] == "true" {
		d.Required = true
	}
	return d
}
