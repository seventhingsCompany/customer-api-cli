// Package exitcode defines the CLI's stable exit codes and maps errors to them.
// The codes are part of the automation contract; never renumber them.
package exitcode

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/SeventhingsCompany/customer-api-go/models"
)

const (
	OK          = 0
	Error       = 1
	Usage       = 2
	Auth        = 3
	NotFound    = 4
	Invalid     = 5
	RateLimited = 6
	Server      = 7
	Partial     = 8
)

// All describes every exit code, for `describe` and the docs.
var All = []struct {
	Code        int    `json:"code"`
	Name        string `json:"name"`
	Description string `json:"description"`
}{
	{OK, "ok", "success"},
	{Error, "error", "generic or network error"},
	{Usage, "usage", "invalid arguments, missing input, or confirmation required (--yes)"},
	{Auth, "auth", "not logged in, 401 or 403"},
	{NotFound, "not_found", "404"},
	{Invalid, "invalid", "400, 409 or 422"},
	{RateLimited, "rate_limited", "429 after retries"},
	{Server, "server", "5xx"},
	{Partial, "partial", "207 multi-status: some items failed"},
}

// UsageError is a user error in arguments or input.
type UsageError struct{ Msg string }

func (e *UsageError) Error() string { return e.Msg }

// Usagef returns a *UsageError.
func Usagef(format string, args ...any) error {
	return &UsageError{Msg: fmt.Sprintf(format, args...)}
}

// AuthError means no usable credentials are available.
type AuthError struct{ Msg string }

func (e *AuthError) Error() string { return e.Msg }

// PartialError reports a 207 multi-status response. The result body has
// already been printed.
type PartialError struct{}

func (e *PartialError) Error() string { return "partial success: some items failed (HTTP 207)" }

// Coder is implemented by errors that choose their own exit code.
type Coder interface {
	error
	ExitCode() int
}

// For returns the exit code for err.
func For(err error) int {
	if err == nil {
		return OK
	}
	if c, ok := errors.AsType[Coder](err); ok {
		return c.ExitCode()
	}
	var usage *UsageError
	var authErr *AuthError
	var partial *PartialError
	var apiErr *models.APIError
	switch {
	case errors.As(err, &usage):
		return Usage
	case errors.As(err, &authErr):
		return Auth
	case errors.As(err, &partial):
		return Partial
	case errors.As(err, &apiErr):
		return forStatus(apiErr.StatusCode)
	}
	return Error
}

func forStatus(status int) int {
	switch {
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return Auth
	case status == http.StatusNotFound:
		return NotFound
	case status == http.StatusTooManyRequests:
		return RateLimited
	case status >= 500:
		return Server
	case status >= 400:
		return Invalid
	}
	return Error
}

// Name returns the machine-readable error code used in JSON error output.
func Name(err error) string {
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	code := For(err)
	for _, c := range All {
		if c.Code == code {
			return c.Name
		}
	}
	return "error"
}
