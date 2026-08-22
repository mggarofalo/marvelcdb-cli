package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// captured records what the fake MarvelCDB saw.
type captured struct {
	method string
	path   string
	form   url.Values
}

func newMutationClient(t *testing.T, response string, got *captured) *Client {
	t.Helper()
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		*got = captured{method: r.Method, path: r.URL.Path, form: form}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, response)
	}))
	return c
}

func TestNewDeckSendsForm(t *testing.T) {
	t.Parallel()

	var got captured
	c := newMutationClient(t, `{"success":true,"msg":4242}`, &got)

	res, err := c.NewDeck(context.Background(), NewDeckRequest{
		Hero: "01001a", Name: "Webhead", Meta: `{"aspect":"justice"}`,
	})
	if err != nil {
		t.Fatalf("NewDeck: %v", err)
	}
	if res.Message() != "4242" {
		t.Errorf("message = %q, want 4242", res.Message())
	}
	if got.method != http.MethodPost || got.path != "/api/oauth2/deck/new" {
		t.Errorf("request = %s %s, want POST /api/oauth2/deck/new", got.method, got.path)
	}
	if got.form.Get("hero") != "01001a" || got.form.Get("name") != "Webhead" {
		t.Errorf("form = %v", got.form)
	}
}

func TestSaveDeckSendsCompleteSlotList(t *testing.T) {
	t.Parallel()

	var got captured
	c := newMutationClient(t, `{"success":true,"msg":7}`, &got)

	xp := 3
	if _, err := c.SaveDeck(context.Background(), 7, SaveDeckRequest{
		Slots:   Slots{"01050": 2},
		Ignored: Slots{"01051": 1},
		Problem: ProblemTooFewCards,
		Name:    "Renamed",
		XPSpent: &xp,
	}); err != nil {
		t.Fatalf("SaveDeck: %v", err)
	}

	if got.method != http.MethodPut || got.path != "/api/oauth2/deck/save/7" {
		t.Errorf("request = %s %s", got.method, got.path)
	}
	var slots map[string]int
	if err := json.Unmarshal([]byte(got.form.Get("slots")), &slots); err != nil {
		t.Fatalf("slots were not JSON: %v", err)
	}
	if slots["01050"] != 2 {
		t.Errorf("slots = %v", slots)
	}
	if got.form.Get("problem") != ProblemTooFewCards {
		t.Errorf("problem = %q", got.form.Get("problem"))
	}
	if got.form.Get("xp_spent") != "3" {
		t.Errorf("xp_spent = %q, want 3", got.form.Get("xp_spent"))
	}
	if got.form.Get("ignored") == "" {
		t.Error("ignored slots were not sent")
	}
}

func TestSaveDeckRejectsBadInput(t *testing.T) {
	t.Parallel()

	c, err := New(WithBaseURL("https://example.invalid"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()

	tests := []struct {
		name string
		id   int
		req  SaveDeckRequest
		want string
	}{
		{"no slots", 1, SaveDeckRequest{}, "slots are required"},
		{"bad id", 0, SaveDeckRequest{Slots: Slots{"a": 1}}, "must be positive"},
		{"bad problem", 1, SaveDeckRequest{Slots: Slots{"a": 1}, Problem: "nope"}, "must be one of"},
		{"bad meta", 1, SaveDeckRequest{Slots: Slots{"a": 1}, Meta: "{"}, "JSON object"},
		{"bad aspect", 1, SaveDeckRequest{Slots: Slots{"a": 1}, Meta: `{"aspect":"chaos"}`}, "must be one of"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := c.SaveDeck(ctx, tt.id, tt.req)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to mention %q", err, tt.want)
			}
		})
	}
}

// MarvelCDB refuses writes with HTTP 200 and {"success":false}, so the envelope
// is the only signal that something went wrong.
func TestMutationFailureBecomesAnError(t *testing.T) {
	t.Parallel()

	var got captured
	c := newMutationClient(t, `{"success":false,"msg":"Slots invalid"}`, &got)

	_, err := c.SaveDeck(context.Background(), 7, SaveDeckRequest{
		Slots: Slots{"01050": 1}, Problem: ProblemHero,
	})
	if err == nil {
		t.Fatal("expected an error for success:false")
	}
	if !strings.Contains(err.Error(), "Slots invalid") {
		t.Errorf("error = %q, want it to carry the server's message", err)
	}
}

func TestDeleteAndPublishDeck(t *testing.T) {
	t.Parallel()

	var got captured
	c := newMutationClient(t, `{"success":true,"msg":99}`, &got)
	ctx := context.Background()

	if _, err := c.DeleteDeck(ctx, 7); err != nil {
		t.Fatalf("DeleteDeck: %v", err)
	}
	if got.method != http.MethodDelete || got.path != "/api/oauth2/deck/delete/7" {
		t.Errorf("delete request = %s %s", got.method, got.path)
	}

	if _, err := c.PublishDeck(ctx, 7, PublishDeckRequest{
		Name: "Public name", Description: "notes", TournamentID: 2,
	}); err != nil {
		t.Fatalf("PublishDeck: %v", err)
	}
	if got.method != http.MethodPut || got.path != "/api/oauth2/deck/publish/7" {
		t.Errorf("publish request = %s %s", got.method, got.path)
	}
	if got.form.Get("description_md") != "notes" || got.form.Get("tournament_id") != "2" {
		t.Errorf("publish form = %v", got.form)
	}
}

func TestValidateMeta(t *testing.T) {
	t.Parallel()

	tests := []struct {
		meta    string
		wantErr bool
	}{
		{"", false},
		{`{"aspect":"leadership"}`, false},
		{`{"something":"else"}`, false},
		{`{"aspect":"chaos"}`, true},
		{`{"aspect":5}`, true},
		{`not json`, true},
	}
	for _, tt := range tests {
		err := validateMeta(tt.meta)
		if (err != nil) != tt.wantErr {
			t.Errorf("validateMeta(%q) error = %v, wantErr %v", tt.meta, err, tt.wantErr)
		}
	}
}
