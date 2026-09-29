package exitcode

import (
	"errors"
	"fmt"
	"testing"

	"github.com/SeventhingsCompany/customer-api-go/models"
)

func TestFor(t *testing.T) {
	api := func(status int) error { return fmt.Errorf("wrapped: %w", &models.APIError{StatusCode: status}) }
	tests := []struct {
		err  error
		want int
	}{
		{nil, OK},
		{errors.New("boom"), Error},
		{Usagef("bad"), Usage},
		{&AuthError{"no login"}, Auth},
		{&PartialError{}, Partial},
		{api(400), Invalid},
		{api(401), Auth},
		{api(403), Auth},
		{api(404), NotFound},
		{api(409), Invalid},
		{api(422), Invalid},
		{api(429), RateLimited},
		{api(500), Server},
		{api(503), Server},
	}
	for _, tt := range tests {
		if got := For(tt.err); got != tt.want {
			t.Errorf("For(%v) = %d, want %d", tt.err, got, tt.want)
		}
	}
	if n := Name(api(404)); n != "not_found" {
		t.Errorf("Name = %q", n)
	}
}
