package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/url"
	"strings"

	"github.com/SeventhingsCompany/customer-api-cli/internal/exitcode"
	"github.com/SeventhingsCompany/customer-api-cli/internal/input"
	"github.com/SeventhingsCompany/customer-api-go/client"
	"github.com/spf13/cobra"
)

func (a *App) apiCmd() *cobra.Command {
	var (
		data    string
		sets    []string
		query   []string
		include bool
	)
	c := &cobra.Command{
		Use:   "api <METHOD> <path>",
		Short: "Send a raw request to any endpoint",
		Long: `Send an authenticated request to any customer API endpoint. The path is
relative to /customer-api/v1. Use this for endpoints that have no dedicated
command yet. Rate limiting and token refresh apply as usual.

Query parameters given with -q are appended with brackets kept literal, so
PHP-style filters work: -q 'filter[name][like][]=Laptop'.`,
		Example: `  seventhings api GET objects/count
  seventhings api GET objects -q per_page=5 -q 'sort[name]=ASC'
  seventhings api PATCH object/<uuid> --set name=Renamed
  seventhings api POST object --data @object.json --include`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			method := strings.ToUpper(args[0])
			path := args[1]
			if len(query) > 0 {
				sep := "?"
				if strings.Contains(path, "?") {
					sep = "&"
				}
				parts := make([]string, 0, len(query))
				for _, q := range query {
					k, v, _ := strings.Cut(q, "=")
					parts = append(parts, k+"="+url.QueryEscape(v))
				}
				path += sep + strings.Join(parts, "&")
			}

			var body io.Reader
			switch {
			case len(sets) > 0:
				m, err := input.Body(data, sets, a.io.In)
				if err != nil {
					return err
				}
				b, _ := json.Marshal(m)
				body = bytes.NewReader(b)
			case data != "":
				b, err := input.Raw(data, a.io.In)
				if err != nil {
					return err
				}
				body = bytes.NewReader(b)
			}

			cl, err := a.Client()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			var resp *client.Response
			switch method {
			case "GET":
				resp, err = cl.Get(ctx, path)
			case "DELETE":
				resp, err = cl.Delete(ctx, path)
			case "POST":
				resp, err = cl.Post(ctx, path, body)
			case "PATCH":
				resp, err = cl.Patch(ctx, path, body)
			case "PUT":
				resp, err = cl.Put(ctx, path, body)
			default:
				return exitcode.Usagef("unsupported method %q (want GET, POST, PATCH, PUT or DELETE)", args[0])
			}
			if err != nil {
				return err
			}
			if a.dryRun && method != "GET" {
				return nil
			}

			var parsed any
			isJSON := len(resp.Body) > 0 && json.Unmarshal(resp.Body, &parsed) == nil
			if include {
				headers := map[string]string{}
				for k := range resp.Header {
					headers[k] = resp.Header.Get(k)
				}
				out := map[string]any{"status": resp.StatusCode, "headers": headers}
				if isJSON {
					out["body"] = parsed
				} else if len(resp.Body) > 0 {
					out["body"] = string(resp.Body)
				}
				return a.print(out)
			}
			switch {
			case isJSON:
				if err := a.print(parsed); err != nil {
					return err
				}
			case len(resp.Body) > 0:
				if _, err := a.io.Out.Write(resp.Body); err != nil {
					return err
				}
			default:
				out := map[string]any{"status": resp.StatusCode}
				if loc := resp.Header.Get("Location"); loc != "" {
					out["location"] = loc
				}
				if err := a.print(out); err != nil {
					return err
				}
			}
			if resp.StatusCode == 207 {
				return &exitcode.PartialError{}
			}
			return nil
		},
	}
	f := c.Flags()
	f.StringVarP(&data, "data", "d", "", "request body: JSON, @file, or - for stdin")
	f.StringArrayVar(&sets, "set", nil, "set a body field: key=string or key:=json (repeatable)")
	f.StringArrayVarP(&query, "query", "Q", nil, "query parameter key=value (repeatable)")
	f.BoolVarP(&include, "include", "i", false, "wrap output with status and headers")
	return c
}
