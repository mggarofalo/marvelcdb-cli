package api

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"time"
)

// Packs returns every product MarvelCDB knows about.
func (c *Client) Packs(ctx context.Context) ([]Pack, error) {
	var out []Pack
	err := c.do(ctx, request{method: "GET", path: "/api/public/packs/"}, &out)
	return out, err
}

// Factions returns the aspects and pseudo-factions.
func (c *Client) Factions(ctx context.Context) ([]Faction, error) {
	var out []Faction
	err := c.do(ctx, request{method: "GET", path: "/api/public/factions/"}, &out)
	return out, err
}

// Cards returns the whole card corpus. Player cards only by default;
// includeEncounter adds encounter-deck and villain cards, which roughly doubles
// the response.
func (c *Client) Cards(ctx context.Context, includeEncounter bool) ([]Card, error) {
	q := url.Values{}
	if includeEncounter {
		q.Set("encounter", "1")
	}
	var out []Card
	err := c.do(ctx, request{method: "GET", path: "/api/public/cards/", query: q}, &out)
	return out, err
}

// CardsByPack returns every card printed in one pack, encounter cards included.
func (c *Client) CardsByPack(ctx context.Context, packCode string) ([]Card, error) {
	if err := validateCode(packCode, "pack code"); err != nil {
		return nil, err
	}
	var out []Card
	err := c.do(ctx, request{
		method:       "GET",
		path:         "/api/public/cards/" + url.PathEscape(packCode) + ".json",
		singleRecord: true,
	}, &out)
	return out, err
}

// Card returns one card by code, for example "01001a".
func (c *Client) Card(ctx context.Context, code string) (*Card, error) {
	if err := validateCode(code, "card code"); err != nil {
		return nil, err
	}
	var out Card
	err := c.do(ctx, request{
		method:       "GET",
		path:         "/api/public/card/" + url.PathEscape(code) + ".json",
		singleRecord: true,
	}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// Faq returns the rules clarifications attached to a card.
func (c *Client) Faq(ctx context.Context, code string) ([]FaqEntry, error) {
	if err := validateCode(code, "card code"); err != nil {
		return nil, err
	}
	var out []FaqEntry
	err := c.do(ctx, request{
		method:       "GET",
		path:         "/api/public/faq/" + url.PathEscape(code) + ".json",
		singleRecord: true,
	}, &out)
	return out, err
}

// Taboos returns the banned/restricted list.
//
// MarvelCDB has this endpoint routed but it currently fails server-side; the
// error is reported rather than hidden.
func (c *Client) Taboos(ctx context.Context) ([]Taboo, error) {
	var out []Taboo
	err := c.do(ctx, request{method: "GET", path: "/api/public/taboos/"}, &out)
	return out, err
}

// Decklist returns one published decklist.
func (c *Client) Decklist(ctx context.Context, id int) (*Deck, error) {
	if id <= 0 {
		return nil, fmt.Errorf("decklist id must be positive, got %d", id)
	}
	var out Deck
	err := c.do(ctx, request{
		method:       "GET",
		path:         "/api/public/decklist/" + strconv.Itoa(id) + ".json",
		singleRecord: true,
	}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// DecklistsByDate returns every decklist published on one day. date must be
// YYYY-MM-DD, which is the only format the endpoint's route accepts.
func (c *Client) DecklistsByDate(ctx context.Context, date string) ([]Deck, error) {
	if !dateRe.MatchString(date) {
		return nil, fmt.Errorf("date %q must be YYYY-MM-DD", date)
	}
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return nil, fmt.Errorf("date %q is not a real date", date)
	}
	var out []Deck
	err := c.do(ctx, request{
		method: "GET",
		path:   "/api/public/decklists/by_date/" + date + ".json",
	}, &out)
	return out, err
}

// PopularDecklists returns MarvelCDB's current popular decklists.
func (c *Client) PopularDecklists(ctx context.Context) ([]PopularDecklist, error) {
	var out []PopularDecklist
	err := c.do(ctx, request{method: "GET", path: "/api/public/decklists/popular/"}, &out)
	return out, err
}

// PublicDeck returns one shared (unpublished) deck by id.
func (c *Client) PublicDeck(ctx context.Context, id int) (*Deck, error) {
	if id <= 0 {
		return nil, fmt.Errorf("deck id must be positive, got %d", id)
	}
	var out Deck
	err := c.do(ctx, request{
		method:       "GET",
		path:         "/api/public/deck/" + strconv.Itoa(id) + ".json",
		singleRecord: true,
	}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

var (
	dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	codeRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

// validateCode rejects codes that would need escaping, which always indicates a
// typo rather than a real MarvelCDB identifier.
func validateCode(code, label string) error {
	if code == "" {
		return fmt.Errorf("%s is required", label)
	}
	if !codeRe.MatchString(code) {
		return fmt.Errorf("%s %q may contain only letters, digits, hyphen and underscore", label, code)
	}
	return nil
}
