# seventhings CLI

[![CI](https://github.com/SeventhingsCompany/customer-api-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/SeventhingsCompany/customer-api-cli/actions/workflows/ci.yml)

Command-line interface and terminal UI for the seventhings Customer API (`/customer-api/v1`). It is built on the [Go SDK](https://github.com/SeventhingsCompany/customer-api-go) and covers the same endpoints.

It has two modes:

- **Interactive** (a person in a terminal): run `seventhings` without arguments to open a full-screen UI. Commands print tables and ask before destructive actions.
- **Agent** (scripts, CI, AI agents): JSON on stdout, never prompts, JSON errors on stderr, stable exit codes. Selected with `--agent`, `SEVENTHINGS_MODE=agent`, `CI=true`, or automatically when stdin or stdout is not a terminal.

## Installation

```sh
# Linux / macOS: installs to /usr/local/bin if writable, else ~/.local/bin
curl -fsSL https://raw.githubusercontent.com/SeventhingsCompany/customer-api-cli/main/install.sh | sh

# A specific version, or a custom directory
curl -fsSL https://raw.githubusercontent.com/SeventhingsCompany/customer-api-cli/main/install.sh | sh -s -- --version v1.0.0 --bin-dir ~/bin
```

```powershell
# Windows (PowerShell): installs to %LOCALAPPDATA%\Programs\seventhings and adds it to your PATH
irm https://raw.githubusercontent.com/SeventhingsCompany/customer-api-cli/main/install.ps1 | iex
```

The scripts check the download against `checksums.txt`. If [cosign](https://docs.sigstore.dev/cosign/system_config/installation/) is installed, `install.sh` also verifies the signature. Run the script again to update.

Alternatives:
- Archives for Linux, macOS and Windows (amd64, arm64) are attached to each [release](https://github.com/SeventhingsCompany/customer-api-cli/releases). They include man pages and shell completions.
- From source (Go 1.27.1+): `go install github.com/SeventhingsCompany/customer-api-cli/cmd/seventhings@latest`

### Verifying downloads

`checksums.txt` is signed with [Sigstore](https://www.sigstore.dev/) keyless signing from the release workflow:

```sh
cosign verify-blob checksums.txt \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp '^https://github.com/SeventhingsCompany/customer-api-cli/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
sha256sum --ignore-missing -c checksums.txt
```

## Quick start

```sh
seventhings auth login --url https://acme.seventhings.com --client-id <client-id> --username me@acme.com
seventhings objects list --filter 'inventory_name like Laptop' --sort -updated_at
seventhings objects get --barcode 102021
seventhings                      # open the interactive UI
```

Tokens are kept in the OS keyring, or in a `0600` file where no keyring exists. They are refreshed automatically. Use `--profile` (or `seventhings config use`) to switch between tenants.

For CI and agents, credentials can come from the environment instead:

| Variable | Purpose |
|---|---|
| `SEVENTHINGS_BASE_URL` | Instance URL |
| `SEVENTHINGS_TOKEN` | Access token (not refreshed) |
| `SEVENTHINGS_USERNAME`, `SEVENTHINGS_PASSWORD`, `SEVENTHINGS_CLIENT_ID` | Log in without a stored session |
| `SEVENTHINGS_PROFILE` | Profile name |
| `SEVENTHINGS_MODE` | `agent` or `interactive` |
| `SEVENTHINGS_RATE_LIMIT` | Requests per minute (default 200, `0` disables) |

## Commands

| Group | Commands |
|---|---|
| `objects` | list, count, get (UUID or `--barcode`), create, update, delete, archive, unarchive, files add/remove, history |
| `rooms`, `locations` | list, count, get, create, update, delete, history |
| `persons` | list, count, get (UUID or `--id`), create, update, delete, create-user, history |
| `users` | list, get |
| `tasks` | list, get, create, update, set-status, delete, history |
| `rental-cases` | list, get, create, update, delete, history |
| `files` | list, get, upload, download, thumbnail |
| `fields` | list, mandatory, missing, get, create, update |
| `hub items`, `hub orders` | circularity hub: list, get, update, delete, add-objects, suggestions, orders |
| `reports` | templates, create (PDF) |
| `api <METHOD> <path>` | any endpoint, raw |
| `auth`, `config` | login/logout/status/token/refresh, profiles |
| `describe` | machine-readable catalog of all commands, flags and exit codes |

Every command has `--help` and a man page (`man seventhings-objects-list`).

Common flags:
- `-o json|ndjson|yaml|table` sets the output format.
- `--jq <expr>` and `--raw` filter the output and print strings unquoted.
- `--fields a,b` keeps only the named top-level fields.
- `--dry-run` prints write requests instead of sending them.
- `--yes` confirms destructive actions without prompting.

Updates to tasks, rental cases and field definitions keep every field you don't give, even though the API replaces the whole record (PUT).

### Agent mode

```sh
seventhings describe                                   # discover commands, flags, exit codes
uuid=$(seventhings objects create --set inventory_name=Laptop --set barcode=SN-1 --jq .uuid --raw)
seventhings objects list --all --fields asset_uuid,barcode > objects.ndjson
seventhings objects delete "$uuid" --yes
```

Errors are a single JSON line on stderr:

```json
{"error":{"code":"not_found","exit_code":4,"message":"Object not found (HTTP 404)","status":404,"body":{...}}}
```

| Exit code | Meaning |
|---|---|
| 0 | success |
| 1 | generic or network error |
| 2 | usage: invalid arguments, missing input, or `--yes` required |
| 3 | not logged in, 401 or 403 |
| 4 | not found |
| 5 | 400, 409 or 422 (validation) |
| 6 | rate limited after retries |
| 7 | server error |
| 8 | partial success (HTTP 207) |

### Rate limiting

The API allows 200 requests per minute by default. The CLI enforces this client-side and retries `429` responses, honoring `Retry-After`. For tenants with a different limit, use `--rate-limit`, `SEVENTHINGS_RATE_LIMIT`, or `seventhings config set <profile> --profile-rate-limit N`. The limit applies per process.

## Interactive UI

`seventhings` (or `seventhings ui`) opens a full-screen UI with a tab for each resource:
- **Browse:** search with `/`, page with `[` and `]`, `enter` opens details, `h` shows history.
- **Edit:** create with `n` and edit with `e`. Forms are built from the tenant's field definitions and include pickers for linked records. Delete with `d`, which always asks first.
- **Resource actions:**
  - objects: `a`/`x` attach or remove a file, `t` creates a task, `o` offers the object on the circularity hub
  - tasks: `s` opens or closes a task
  - files: `D` downloads a file
  - hub items: `o` creates an order

Press `?` for all keys.

## Development

```sh
go test -race ./...
golangci-lint run ./...
govulncheck ./...
goreleaser release --snapshot --clean --skip=sign   # local release build
```

Releases are created by pushing a `vX.Y.Z` tag; see `.github/workflows/release.yml`.

## License

MIT
