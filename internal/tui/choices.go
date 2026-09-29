package tui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/SeventhingsCompany/customer-api-go/client"
	"github.com/SeventhingsCompany/customer-api-go/models"
)

// choiceSource names a list of records offered in a select field.
type choiceSource string

const (
	srcUserUUID   choiceSource = "users by UUID"
	srcUserID     choiceSource = "users by ID"
	srcPersonID   choiceSource = "persons"
	srcLocationID choiceSource = "locations"
	srcRoomID     choiceSource = "rooms"
)

// linkedSources maps linked field types to the records they point at. The
// API stores these links as numeric IDs.
var linkedSources = map[models.FieldTypeName]choiceSource{
	models.FieldTypeLinkedUser:     srcUserID,
	models.FieldTypeLinkedPerson:   srcPersonID,
	models.FieldTypeLinkedLocation: srcLocationID,
	models.FieldTypeLinkedRoom:     srcRoomID,
}

// maxChoices caps picker size; larger tenants fall back to typing the ID.
const maxChoices = 1000

var errTooManyChoices = errors.New("too many records for a picker")

// loadChoices returns (label, value) pairs for src.
func loadChoices(ctx context.Context, cl *client.Client, src choiceSource) ([][2]string, error) {
	var out [][2]string
	add := func(label, value string) error {
		if len(out) >= maxChoices {
			return errTooManyChoices
		}
		out = append(out, [2]string{label, value})
		return nil
	}
	switch src {
	case srcUserUUID, srcUserID:
		for u, err := range cl.UsersAll(ctx, nil) {
			if err != nil {
				return nil, err
			}
			label := u.Email
			if u.DisplayName != nil && *u.DisplayName != "" {
				label = *u.DisplayName
				if !strings.Contains(label, u.Email) {
					label += " <" + u.Email + ">"
				}
			}
			value := u.UUID
			if src == srcUserID {
				value = strconv.Itoa(u.ID)
			}
			if err := add(label, value); err != nil {
				return nil, err
			}
		}
	case srcPersonID:
		for p, err := range cl.PersonsAll(ctx, nil) {
			if err != nil {
				return nil, err
			}
			name := strings.TrimSpace(deref(p.Firstname) + " " + deref(p.Lastname))
			label := p.Email
			if name != "" {
				label = name + " <" + p.Email + ">"
			}
			if err := add(label, strconv.Itoa(p.ID)); err != nil {
				return nil, err
			}
		}
	case srcLocationID:
		for l, err := range cl.LocationsAll(ctx, nil) {
			if err != nil {
				return nil, err
			}
			label := l.Name()
			if city := str(l["city"]); city != "" {
				label += ", " + city
			}
			if err := add(label, str(l["id"])); err != nil {
				return nil, err
			}
		}
	case srcRoomID:
		for r, err := range cl.RoomsAll(ctx, nil) {
			if err != nil {
				return nil, err
			}
			label := r.Name()
			if num := str(r["number"]); num != "" {
				label += " (" + num + ")"
			}
			if err := add(label, str(r["id"])); err != nil {
				return nil, err
			}
		}
	default:
		return nil, fmt.Errorf("unknown choice source %q", src)
	}
	return out, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
