package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/SeventhingsCompany/customer-api-cli/internal/exitcode"
	"github.com/SeventhingsCompany/customer-api-go/models"
)

// errorJSON is the stable error shape written to stderr in agent mode.
type errorJSON struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code     string `json:"code"`
	ExitCode int    `json:"exit_code"`
	Message  string `json:"message"`
	Status   int    `json:"status,omitempty"`
	Body     any    `json:"body,omitempty"`
}

func (a *App) printError(err error) {
	a.resolveMode()
	body := errorBody{Code: exitcode.Name(err), ExitCode: exitcode.For(err), Message: err.Error()}

	var apiErr *models.APIError
	if errors.As(err, &apiErr) {
		body.Status = apiErr.StatusCode
		body.Message = apiMessage(apiErr)
		var parsed any
		if json.Unmarshal([]byte(apiErr.Body), &parsed) == nil {
			body.Body = parsed
		} else if apiErr.Body != "" {
			body.Body = apiErr.Body
		}
	}

	if !a.interactive() {
		enc := json.NewEncoder(a.io.Err)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(errorJSON{Error: body})
		return
	}
	_, _ = fmt.Fprintf(a.io.Err, "Error: %s\n", body.Message)
	if apiErr != nil && body.Message != strings.TrimSpace(apiErr.Body) && apiErr.Body != "" {
		detail := apiErr.Body
		if len(detail) > 500 {
			detail = detail[:500] + "…"
		}
		_, _ = fmt.Fprintf(a.io.Err, "%s\n", detail)
	}
}

// apiMessage extracts a human message from common error body shapes
// (RFC 7807 problem details, {"message": ...}, {"detail": ...}).
func apiMessage(e *models.APIError) string {
	var m map[string]any
	if json.Unmarshal([]byte(e.Body), &m) == nil {
		for _, k := range []string{"detail", "message", "title", "error"} {
			if s, ok := m[k].(string); ok && s != "" {
				return fmt.Sprintf("%s (HTTP %d)", s, e.StatusCode)
			}
		}
	}
	status := e.Status
	if status == "" {
		status = fmt.Sprint(e.StatusCode)
	}
	return "HTTP " + status
}
