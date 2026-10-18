# Changelog

## Unreleased

- Add `objects create --from-catalog` and `b` in Catalog lists/details to review
  and create an independent object with a new barcode and object-required fields.
- Add image galleries to record details: `[`/`]` navigate ordered, deduplicated
  images; captions show position, images load on demand, and `O` opens the
  selected image. List paging and CHub order navigation are unchanged.
- Show images in Catalog details, preferring `catalog_object_picture` and falling
  back to other image attachments. Support existing image protocols, caching,
  hide/show and opening catalog attachments in the system viewer.
- Combine Hub items and Hub orders under one **CHub** pane, with `v` switching
  between independent Items and Orders subviews. Move Catalog to `0`, retain
  `C` as a Catalog alias, and preserve existing CLI commands.
- Refresh both CHub subviews after hub writes and fit filter forms to short
  terminals, including the subview navigation row.
- Add `objects duplicate` and `catalog-items duplicate` with field overrides,
  dry-run support, and a new UUID result.
- Add `u` in object and catalog lists/details to review a prefilled copy before
  creating it. Require a new object barcode and omit server-managed fields,
  inherited barcodes, attachments, relationship lists and calculated fields.
- Retain duplicate form input after failed submissions for manual retry.

## v0.2.2

- Add the Catalog tab to the interactive UI, accessible with `C` or Tab after
  Hub orders, without changing existing numeric shortcuts.
- Support catalog browsing, search, filters, sorting, pagination, details,
  create/edit/delete, and copying IDs and CLI commands.
- Build catalog forms from the instance's object schema, translating field keys
  to the catalog namespace without imposing object-only mandatory fields.
- Preserve unspecified catalog fields through a fresh read before replacement
  updates, and retain failed form input for manual retry.
- Keep catalog and active-tab labels visible on narrow terminals.
- Show permission/feature errors without forcing a new login; expired sessions
  still open the login form.

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
