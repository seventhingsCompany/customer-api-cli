# Changelog

## v0.2.1

- Correct the case-sensitive Sigstore certificate identity in the installer and
  verification instructions. Signatures identify the organization as
  `seventhingsCompany`.
- Add an installer identity regression test.
- Includes all v0.2.0 changes below. The v0.2.0 publication was canceled before
  release assets were published; its tag remains unchanged.

## v0.2.0

### Added

- Upgrade the Go SDK to v1.5.1.
- Add product catalog commands: list, get, create, update and delete, including
  filtering, sorting and streaming all pages.
- Support timed rentals with `--issue-at`, `--due-at` and `--is-full-day`.
- Support custom thumbnail dimensions with `--width` and `--height`.

### Fixed

- Preserve unspecified catalog fields when updating an item.
- Keep omitted task reminders distinct from an explicit empty reminder list.
- Preserve timed rental state during updates, including date-only changes and
  conversion to full-day rentals.
- Use room and location UUID field keys in terminal UI record actions, retaining
  compatibility with older responses.
- Avoid double throttling from the SDK's new default rate limiter, and honor
  valid `Retry-After` delays without shortening them.
- Wait for slow terminal operations before asserting test results.

### Security

- Require Go 1.27.2 to include standard-library security fixes.
