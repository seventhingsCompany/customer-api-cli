package cmd

import (
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
		Short: "Print a machine-readable catalog of all commands (for agents)",
		Long: `Print a JSON catalog of every command with its flags, plus exit codes,
filter operators, environment variables and the agent-mode contract. Agents
should read this instead of parsing --help.`,
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
