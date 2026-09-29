package cmd

import (
	"fmt"

	"github.com/SeventhingsCompany/customer-api-cli/internal/config"
	"github.com/SeventhingsCompany/customer-api-cli/internal/exitcode"
	"github.com/spf13/cobra"
)

func (a *App) configCmd() *cobra.Command {
	c := &cobra.Command{Use: "config", Short: "Manage profiles (instance URL, client ID, rate limit)"}

	list := &cobra.Command{
		Use:   "list",
		Short: "List profiles",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			active, _ := a.profile()
			rows := []map[string]any{}
			for _, n := range a.cfg.Names() {
				p := a.cfg.Profiles[n]
				row := map[string]any{"name": n, "url": p.URL, "client_id": p.ClientID, "username": p.Username, "active": n == active}
				if p.RateLimit != nil {
					row["rate_limit"] = *p.RateLimit
				}
				rows = append(rows, row)
			}
			return a.print(rows)
		},
	}

	use := &cobra.Command{
		Use:   "use <profile>",
		Short: "Set the default profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, ok := a.cfg.Profiles[args[0]]; !ok {
				return exitcode.Usagef("unknown profile %q", args[0])
			}
			a.cfg.CurrentProfile = args[0]
			if err := a.cfg.Save(); err != nil {
				return err
			}
			return a.done(fmt.Sprintf("Using profile %q", args[0]), map[string]any{"current_profile": args[0]})
		},
	}

	var url, clientID, username string
	var rateLimit int
	set := &cobra.Command{
		Use:   "set <profile>",
		Short: "Create or update a profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, ok := a.cfg.Profiles[args[0]]
			if !ok {
				p = &config.Profile{}
				a.cfg.Profiles[args[0]] = p
			}
			f := cmd.Flags()
			if f.Changed("url") {
				p.URL = url
			}
			if f.Changed("client-id") {
				p.ClientID = clientID
			}
			if f.Changed("username") {
				p.Username = username
			}
			if f.Changed("profile-rate-limit") {
				if rateLimit < 0 {
					p.RateLimit = nil
				} else {
					p.RateLimit = &rateLimit
				}
			}
			if a.cfg.CurrentProfile == "" {
				a.cfg.CurrentProfile = args[0]
			}
			if err := a.cfg.Save(); err != nil {
				return err
			}
			return a.done(fmt.Sprintf("Saved profile %q", args[0]), map[string]any{"profile": args[0], "config": p})
		},
	}
	set.Flags().StringVar(&url, "url", "", "instance URL")
	set.Flags().StringVar(&clientID, "client-id", "", "OAuth client ID")
	set.Flags().StringVar(&username, "username", "", "username")
	set.Flags().IntVar(&rateLimit, "profile-rate-limit", -1, "requests per minute for this tenant (-1 resets to the default)")
	// The local --url shadows the global --url, which selects the instance for API calls.

	del := &cobra.Command{
		Use:   "delete <profile>",
		Short: "Delete a profile and its stored tokens",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, ok := a.cfg.Profiles[args[0]]; !ok {
				return exitcode.Usagef("unknown profile %q", args[0])
			}
			if err := a.confirm(fmt.Sprintf("Delete profile %q", args[0])); err != nil {
				return err
			}
			delete(a.cfg.Profiles, args[0])
			if a.cfg.CurrentProfile == args[0] {
				a.cfg.CurrentProfile = ""
			}
			if err := a.credentialStore().Delete(args[0]); err != nil {
				return err
			}
			if err := a.cfg.Save(); err != nil {
				return err
			}
			return a.done(fmt.Sprintf("Deleted profile %q", args[0]), map[string]any{"profile": args[0], "deleted": true})
		},
	}

	path := &cobra.Command{
		Use:   "path",
		Short: "Print the config file path",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintln(a.io.Out, a.cfg.Path())
			return err
		},
	}

	for _, sub := range []*cobra.Command{list, use, set, del, path} {
		annotate(sub, annAuth, "none")
	}
	destructive(del)
	c.AddCommand(list, use, set, del, path)
	return c
}
