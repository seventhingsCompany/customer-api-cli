package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/SeventhingsCompany/customer-api-go/client"
)

func TestFlatRoomAndLocationIdentities(t *testing.T) {
	for _, r := range resources() {
		key := ""
		switch r.cli {
		case "rooms":
			key = "room_uuid"
		case "locations":
			key = "location_uuid"
		default:
			continue
		}
		t.Run(r.cli, func(t *testing.T) {
			for _, tc := range []struct {
				item Item
				want string
			}{
				{Item{key: "canonical-id"}, "canonical-id"},
				{Item{key: "canonical-id", "uuid": "custom-value"}, "canonical-id"},
				{Item{"uuid": "legacy-id"}, "legacy-id"},
				{Item{}, ""},
			} {
				if got := r.id(tc.item); got != tc.want {
					t.Errorf("id(%v) = %q, want %q", tc.item, got, tc.want)
				}
			}
		})
	}
}

func TestFlatRoomAndLocationActions(t *testing.T) {
	for _, tc := range []struct {
		group, singular, key, tab string
	}{
		{"rooms", "room", "room_uuid", "2"},
		{"locations", "location", "location_uuid", "3"},
	} {
		t.Run(tc.group, func(t *testing.T) {
			f := newFakeAPI(t)
			f.extra = map[string]http.HandlerFunc{
				"GET " + tc.group: func(w http.ResponseWriter, _ *http.Request) {
					_ = json.NewEncoder(w).Encode(Item{"items": []Item{{tc.key: "record-id", "uuid": "custom-value", "name": "Office"}}})
				},
				"GET " + tc.group + "/count": func(w http.ResponseWriter, _ *http.Request) {
					_ = json.NewEncoder(w).Encode(Item{"count": 1})
				},
				"GET " + tc.singular + "/record-id": func(w http.ResponseWriter, _ *http.Request) {
					_ = json.NewEncoder(w).Encode(Item{tc.key: "record-id", "name": "Full detail loaded"})
				},
				"DELETE " + tc.singular + "/record-id": func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNoContent)
				},
			}
			d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
			d.key(tc.tab)
			d.expect("Office")
			d.key("y")
			d.expect("Copied record-id")
			d.key("enter")
			d.expect("Full detail loaded")
			if !f.called("GET " + tc.singular + "/record-id?") {
				t.Fatal("details did not fetch the canonical UUID")
			}
			d.key("esc", "d", "y")
			d.expect("Deleted " + tc.singular + " record-id")
			if !f.called("DELETE " + tc.singular + "/record-id?") {
				t.Fatal("delete did not use the canonical UUID")
			}
		})
	}
}

func TestTaskCreateDoesNotDisableDefaultReminders(t *testing.T) {
	var body Item
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		w.Header().Set("Location", "/task/t1")
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	cl := client.NewWithToken(srv.URL, "t", client.WithRateLimit(0))
	for _, r := range resources() {
		if r.cli != "tasks" {
			continue
		}
		_, err := r.create(t.Context(), cl, Item{"title": "Check", "deadline": "2026-12-31", "assignees": []string{"u1"}, "references": []Item{{"type": "asset", "uuid": "o1"}}})
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := body["reminders"]; exists {
			t.Errorf("default reminders were overridden: %v", body)
		}
		return
	}
	t.Fatal("tasks resource not found")
}

func TestRentalUpdateKeepsTimedState(t *testing.T) {
	issue, due := "2026-12-01T09:00:00Z", "2026-12-01T12:00:00Z"
	var body Item
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(Item{
				"uuid": "r1", "title": "Timed", "renter": Item{"type": "plain", "value": "Jane"},
				"is_full_day": false, "issue_at": issue, "due_at": due, "issue_date": "2026-12-01", "due_date": "2026-12-01",
				"references": []Item{{"type": "asset", "uuid": "o1"}}, "attachments": []Item{{"uuid": "f1"}},
			})
			return
		}
		body = nil
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	cl := client.NewWithToken(srv.URL, "t", client.WithRateLimit(0))
	for _, r := range resources() {
		if r.cli != "rental-cases" {
			continue
		}
		if err := r.update(t.Context(), cl, "r1", Item{"comment": "Changed"}); err != nil {
			t.Fatal(err)
		}
		if body["is_full_day"] != false || body["issue_at"] != issue || body["due_at"] != due || !reflect.DeepEqual(body["attachments"], []any{"f1"}) {
			t.Errorf("timed state lost: %v", body)
		}
		if err := r.update(t.Context(), cl, "r1", Item{"due_date": "2026-12-02"}); err != nil {
			t.Fatal(err)
		}
		if body["due_at"] != nil || body["issue_at"] != nil || body["is_full_day"] != nil {
			t.Errorf("date move must allow the API to move the stored due time: %v", body)
		}
		return
	}
	t.Fatal("rentals resource not found")
}
