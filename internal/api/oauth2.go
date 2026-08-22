package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
)

// Problem codes MarvelCDB accepts on a deck save.
const (
	ProblemTooFewCards  = "too_few_cards"
	ProblemTooManyCards = "too_many_cards"
	ProblemInvalidCards = "invalid_cards"
	ProblemHero         = "hero"
)

// ValidProblems lists every accepted deck problem code.
var ValidProblems = []string{ProblemTooFewCards, ProblemTooManyCards, ProblemInvalidCards, ProblemHero}

// Aspects MarvelCDB accepts in a deck's meta field.
var Aspects = []string{"aggression", "justice", "leadership", "protection", "pool"}

// MyDecks returns every deck belonging to the authenticated user.
func (c *Client) MyDecks(ctx context.Context) ([]Deck, error) {
	var out []Deck
	err := c.do(ctx, request{method: "GET", path: "/api/oauth2/decks"}, &out)
	return out, err
}

// LoadDeck returns one deck belonging to the authenticated user, or one that
// its owner has chosen to share.
func (c *Client) LoadDeck(ctx context.Context, id int) (*Deck, error) {
	if id <= 0 {
		return nil, fmt.Errorf("deck id must be positive, got %d", id)
	}
	var out Deck
	err := c.do(ctx, request{
		method:       "GET",
		path:         "/api/oauth2/deck/load/" + strconv.Itoa(id),
		singleRecord: true,
	}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// NewDeckRequest describes a deck to create.
type NewDeckRequest struct {
	// Hero is the code of the hero's identity card, for example "01001a".
	Hero string
	// Name defaults to the hero's name when empty.
	Name string
	// Meta is a JSON object, conventionally {"aspect":"leadership"}.
	Meta string
}

// NewDeck creates an empty deck seeded with the hero's required cards. On
// success the result's message holds the new deck id.
func (c *Client) NewDeck(ctx context.Context, req NewDeckRequest) (MutationResult, error) {
	var zero MutationResult
	if err := validateCode(req.Hero, "hero code"); err != nil {
		return zero, err
	}
	if err := validateMeta(req.Meta); err != nil {
		return zero, err
	}

	form := url.Values{}
	form.Set("hero", req.Hero)
	if req.Name != "" {
		form.Set("name", req.Name)
	}
	if req.Meta != "" {
		form.Set("meta", req.Meta)
	}

	var out MutationResult
	err := c.do(ctx, request{method: "POST", path: "/api/oauth2/deck/new", form: form}, &out)
	if err != nil {
		return zero, err
	}
	return out, checkMutation(out, "create deck")
}

// SaveDeckRequest describes an update to an existing deck. Only Slots and
// Problem are always sent; the remaining fields are omitted when empty, and
// MarvelCDB then leaves the stored value alone.
type SaveDeckRequest struct {
	Slots       Slots
	Ignored     Slots
	Problem     string
	Name        string
	Description string
	Tags        string
	Meta        string
	DecklistID  int
	// XPSpent and XPAdjustment are only honoured for an upgraded deck.
	XPSpent      *int
	XPAdjustment *int
}

// SaveDeck writes deck contents. MarvelCDB requires the full slot list on every
// save: what you send replaces what was stored.
func (c *Client) SaveDeck(ctx context.Context, id int, req SaveDeckRequest) (MutationResult, error) {
	var zero MutationResult
	if id <= 0 {
		return zero, fmt.Errorf("deck id must be positive, got %d", id)
	}
	if len(req.Slots) == 0 {
		return zero, fmt.Errorf("slots are required: MarvelCDB replaces the deck contents with what you send")
	}
	if req.Problem != "" && !validProblem(req.Problem) {
		return zero, fmt.Errorf("problem %q must be one of %v", req.Problem, ValidProblems)
	}
	if err := validateMeta(req.Meta); err != nil {
		return zero, err
	}

	slots, err := json.Marshal(req.Slots)
	if err != nil {
		return zero, fmt.Errorf("encode slots: %w", err)
	}

	form := url.Values{}
	form.Set("slots", string(slots))
	form.Set("problem", req.Problem)
	if len(req.Ignored) > 0 {
		ignored, err := json.Marshal(req.Ignored)
		if err != nil {
			return zero, fmt.Errorf("encode ignored slots: %w", err)
		}
		form.Set("ignored", string(ignored))
	}
	setIfNotEmpty(form, "name", req.Name)
	setIfNotEmpty(form, "description_md", req.Description)
	setIfNotEmpty(form, "tags", req.Tags)
	setIfNotEmpty(form, "meta", req.Meta)
	if req.DecklistID > 0 {
		form.Set("decklist_id", strconv.Itoa(req.DecklistID))
	}
	if req.XPSpent != nil {
		form.Set("xp_spent", strconv.Itoa(*req.XPSpent))
	}
	if req.XPAdjustment != nil {
		form.Set("xp_adjustment", strconv.Itoa(*req.XPAdjustment))
	}

	var out MutationResult
	if err := c.do(ctx, request{
		method:       "PUT",
		path:         "/api/oauth2/deck/save/" + strconv.Itoa(id),
		form:         form,
		singleRecord: true,
	}, &out); err != nil {
		return zero, err
	}
	return out, checkMutation(out, "save deck")
}

// DeleteDeck removes a deck owned by the authenticated user.
func (c *Client) DeleteDeck(ctx context.Context, id int) (MutationResult, error) {
	var zero MutationResult
	if id <= 0 {
		return zero, fmt.Errorf("deck id must be positive, got %d", id)
	}
	var out MutationResult
	if err := c.do(ctx, request{
		method:       "DELETE",
		path:         "/api/oauth2/deck/delete/" + strconv.Itoa(id),
		singleRecord: true,
	}, &out); err != nil {
		return zero, err
	}
	return out, checkMutation(out, "delete deck")
}

// PublishDeckRequest describes a deck publication.
type PublishDeckRequest struct {
	Name        string
	Description string
	// TournamentID selects a tournament type; 0 leaves it unset.
	TournamentID int
	// Precedent names a predecessor decklist, by id or by a /view/<id> URL.
	//
	// MarvelCDB's handler has a bug on this path: supplying a numeric precedent
	// dereferences an undefined variable server-side and the request fails with
	// a 500. Leave it empty unless upstream has been fixed.
	Precedent string
}

// PublishDeck publishes a deck as a decklist. On success the result's message
// holds the new decklist id.
func (c *Client) PublishDeck(ctx context.Context, id int, req PublishDeckRequest) (MutationResult, error) {
	var zero MutationResult
	if id <= 0 {
		return zero, fmt.Errorf("deck id must be positive, got %d", id)
	}

	form := url.Values{}
	setIfNotEmpty(form, "name", req.Name)
	setIfNotEmpty(form, "description_md", req.Description)
	setIfNotEmpty(form, "precedent", req.Precedent)
	if req.TournamentID > 0 {
		form.Set("tournament_id", strconv.Itoa(req.TournamentID))
	}

	var out MutationResult
	if err := c.do(ctx, request{
		method:       "PUT",
		path:         "/api/oauth2/deck/publish/" + strconv.Itoa(id),
		form:         form,
		singleRecord: true,
	}, &out); err != nil {
		return zero, err
	}
	return out, checkMutation(out, "publish deck")
}

// checkMutation turns MarvelCDB's {"success":false,"msg":"..."} into an error.
// The endpoints return HTTP 200 for refusals, so the envelope is the only
// signal that something went wrong.
func checkMutation(res MutationResult, action string) error {
	if res.Success {
		return nil
	}
	msg := res.Message()
	if msg == "" {
		msg = "MarvelCDB rejected the request without giving a reason"
	}
	return fmt.Errorf("%s: %s", action, msg)
}

func validProblem(p string) bool {
	for _, v := range ValidProblems {
		if v == p {
			return true
		}
	}
	return false
}

// validateMeta checks meta is a JSON object and that any aspect it names is one
// MarvelCDB accepts. An unrecognised aspect makes the server abort the save
// silently, so it is worth catching here.
func validateMeta(meta string) error {
	if meta == "" {
		return nil
	}
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal([]byte(meta), &parsed); err != nil {
		return fmt.Errorf("meta must be a JSON object: %w", err)
	}
	raw, ok := parsed["aspect"]
	if !ok {
		return nil
	}
	var aspect string
	if err := json.Unmarshal(raw, &aspect); err != nil {
		return fmt.Errorf("meta aspect must be a string: %w", err)
	}
	for _, a := range Aspects {
		if a == aspect {
			return nil
		}
	}
	return fmt.Errorf("meta aspect %q must be one of %v", aspect, Aspects)
}

func setIfNotEmpty(v url.Values, key, value string) {
	if value != "" {
		v.Set(key, value)
	}
}
