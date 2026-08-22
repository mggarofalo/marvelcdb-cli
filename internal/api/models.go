package api

import (
	"encoding/json"
	"strings"
	"time"
)

// Card is a single MarvelCDB card. Optional numeric and boolean fields are
// pointers so that an absent field and a zero value stay distinguishable.
type Card struct {
	Code     string `json:"code"`
	Name     string `json:"name,omitempty"`
	RealName string `json:"real_name,omitempty"`
	Subname  string `json:"subname,omitempty"`
	ID       *int   `json:"id,omitempty"`

	PackCode  string `json:"pack_code,omitempty"`
	PackName  string `json:"pack_name,omitempty"`
	PackWave  *int   `json:"pack_wave,omitempty"`
	PackLegal *bool  `json:"pack_legacy,omitempty"`

	TypeCode    string `json:"type_code,omitempty"`
	TypeName    string `json:"type_name,omitempty"`
	FactionCode string `json:"faction_code,omitempty"`
	FactionName string `json:"faction_name,omitempty"`

	CardSetCode         string `json:"card_set_code,omitempty"`
	CardSetName         string `json:"card_set_name,omitempty"`
	CardSetParentCode   string `json:"card_set_parent_code,omitempty"`
	CardSetTypeNameCode string `json:"card_set_type_name_code,omitempty"`

	Position    *int `json:"position,omitempty"`
	SetPosition *int `json:"set_position,omitempty"`
	Quantity    *int `json:"quantity,omitempty"`
	DeckLimit   *int `json:"deck_limit,omitempty"`
	Spoiler     *int `json:"spoiler,omitempty"`

	Text     string `json:"text,omitempty"`
	RealText string `json:"real_text,omitempty"`
	Flavor   string `json:"flavor,omitempty"`
	Errata   string `json:"errata,omitempty"`

	BackName   string `json:"back_name,omitempty"`
	BackText   string `json:"back_text,omitempty"`
	BackFlavor string `json:"back_flavor,omitempty"`

	Traits     string `json:"traits,omitempty"`
	RealTraits string `json:"real_traits,omitempty"`
	Stage      string `json:"stage,omitempty"`

	Cost        *int  `json:"cost,omitempty"`
	CostPerHero *bool `json:"cost_per_hero,omitempty"`
	CostStar    *bool `json:"cost_star,omitempty"`

	ResourcePhysical *int `json:"resource_physical,omitempty"`
	ResourceMental   *int `json:"resource_mental,omitempty"`
	ResourceEnergy   *int `json:"resource_energy,omitempty"`
	ResourceWild     *int `json:"resource_wild,omitempty"`

	Attack      *int  `json:"attack,omitempty"`
	AttackCost  *int  `json:"attack_cost,omitempty"`
	AttackStar  *bool `json:"attack_star,omitempty"`
	Thwart      *int  `json:"thwart,omitempty"`
	ThwartCost  *int  `json:"thwart_cost,omitempty"`
	ThwartStar  *bool `json:"thwart_star,omitempty"`
	Defense     *int  `json:"defense,omitempty"`
	DefenseCost *int  `json:"defense_cost,omitempty"`
	DefenseStar *bool `json:"defense_star,omitempty"`
	Recover     *int  `json:"recover,omitempty"`
	RecoverCost *int  `json:"recover_cost,omitempty"`
	RecoverStar *bool `json:"recover_star,omitempty"`

	Health         *int  `json:"health,omitempty"`
	HealthPerHero  *bool `json:"health_per_hero,omitempty"`
	HealthPerGroup *bool `json:"health_per_group,omitempty"`
	HealthStar     *bool `json:"health_star,omitempty"`
	HandSize       *int  `json:"hand_size,omitempty"`

	Scheme             *int  `json:"scheme,omitempty"`
	SchemeStar         *bool `json:"scheme_star,omitempty"`
	SchemeAcceleration *int  `json:"scheme_acceleration,omitempty"`
	SchemeAmplify      *int  `json:"scheme_amplify,omitempty"`
	SchemeCrisis       *int  `json:"scheme_crisis,omitempty"`
	SchemeHazard       *int  `json:"scheme_hazard,omitempty"`

	BaseThreat         *int  `json:"base_threat,omitempty"`
	BaseThreatFixed    *bool `json:"base_threat_fixed,omitempty"`
	BaseThreatPerGroup *bool `json:"base_threat_per_group,omitempty"`
	BaseThreatStar     *bool `json:"base_threat_star,omitempty"`

	EscalationThreat      *int  `json:"escalation_threat,omitempty"`
	EscalationThreatFixed *bool `json:"escalation_threat_fixed,omitempty"`
	EscalationThreatStar  *bool `json:"escalation_threat_star,omitempty"`

	Threat         *int  `json:"threat,omitempty"`
	ThreatFixed    *bool `json:"threat_fixed,omitempty"`
	ThreatPerGroup *bool `json:"threat_per_group,omitempty"`
	ThreatStar     *bool `json:"threat_star,omitempty"`

	Boost     *int  `json:"boost,omitempty"`
	BoostStar *bool `json:"boost_star,omitempty"`

	IsUnique    *bool `json:"is_unique,omitempty"`
	Hidden      *bool `json:"hidden,omitempty"`
	Permanent   *bool `json:"permanent,omitempty"`
	DoubleSided *bool `json:"double_sided,omitempty"`

	LinkedToCode string `json:"linked_to_code,omitempty"`
	LinkedToName string `json:"linked_to_name,omitempty"`
	LinkedCard   *Card  `json:"linked_card,omitempty"`

	DuplicateOfCode string   `json:"duplicate_of_code,omitempty"`
	DuplicateOfName string   `json:"duplicate_of_name,omitempty"`
	DuplicatedBy    []string `json:"duplicated_by,omitempty"`

	Illustrator  string `json:"illustrator,omitempty"`
	ImageSrc     string `json:"imagesrc,omitempty"`
	BackImageSrc string `json:"backimagesrc,omitempty"`
	OctgnID      string `json:"octgn_id,omitempty"`
	URL          string `json:"url,omitempty"`

	// Shapes MarvelCDB has never documented; kept verbatim.
	Meta             json.RawMessage `json:"meta,omitempty"`
	DeckOptions      json.RawMessage `json:"deck_options,omitempty"`
	DeckRequirements json.RawMessage `json:"deck_requirements,omitempty"`
	Restrictions     json.RawMessage `json:"restrictions,omitempty"`

	Extra map[string]json.RawMessage `json:"-"`
}

// UnmarshalJSON decodes a card, retaining any fields this build does not know.
func (c *Card) UnmarshalJSON(b []byte) error {
	type alias Card
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*c = Card(a)
	extra, err := extraFields(b, a)
	if err != nil {
		return err
	}
	c.Extra = extra
	return nil
}

// MarshalJSON re-emits the card including fields retained by UnmarshalJSON.
func (c Card) MarshalJSON() ([]byte, error) {
	type alias Card
	return mergeExtras(alias(c), c.Extra)
}

// TraitList splits the printed trait line ("Avenger. Aerial.") into its parts.
//
// Traits are separated by ". ", not by ".", because a trait can contain its own
// periods: splitting "S.H.I.E.L.D." on "." shreds it into single letters.
func (c Card) TraitList() []string {
	raw := c.Traits
	if raw == "" {
		raw = c.RealTraits
	}
	var out []string
	for _, t := range strings.Split(raw, ". ") {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		// Drop the period that ends the last trait in the line.
		if strings.Count(t, ".") == 1 && strings.HasSuffix(t, ".") {
			t = strings.TrimSuffix(t, ".")
		}
		out = append(out, t)
	}
	return out
}

// Resources totals the printed resource icons on the card.
func (c Card) Resources() int {
	return intVal(c.ResourcePhysical) + intVal(c.ResourceMental) +
		intVal(c.ResourceEnergy) + intVal(c.ResourceWild)
}

// Pack is a MarvelCDB product (a core set, hero pack, or scenario pack).
type Pack struct {
	ID        *int   `json:"id,omitempty"`
	Code      string `json:"code"`
	Name      string `json:"name,omitempty"`
	Position  *int   `json:"position,omitempty"`
	Available string `json:"available,omitempty"`
	Known     *int   `json:"known,omitempty"`
	Total     *int   `json:"total,omitempty"`
	URL       string `json:"url,omitempty"`

	Extra map[string]json.RawMessage `json:"-"`
}

// UnmarshalJSON decodes a pack, retaining unknown fields.
func (p *Pack) UnmarshalJSON(b []byte) error {
	type alias Pack
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*p = Pack(a)
	extra, err := extraFields(b, a)
	if err != nil {
		return err
	}
	p.Extra = extra
	return nil
}

// MarshalJSON re-emits the pack including retained fields.
func (p Pack) MarshalJSON() ([]byte, error) {
	type alias Pack
	return mergeExtras(alias(p), p.Extra)
}

// ReleaseDate parses Available, which MarvelCDB formats as YYYY-MM-DD.
func (p Pack) ReleaseDate() (time.Time, bool) {
	if p.Available == "" {
		return time.Time{}, false
	}
	t, err := time.Parse("2006-01-02", p.Available)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// Faction is an aspect (Leadership, Justice, ...) or a pseudo-faction such as
// Hero, Encounter, or Basic.
type Faction struct {
	Code      string `json:"code"`
	Name      string `json:"name,omitempty"`
	IsPrimary *bool  `json:"is_primary,omitempty"`
	OctgnID   string `json:"octgn_id,omitempty"`

	Extra map[string]json.RawMessage `json:"-"`
}

// UnmarshalJSON decodes a faction, retaining unknown fields.
func (f *Faction) UnmarshalJSON(b []byte) error {
	type alias Faction
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*f = Faction(a)
	extra, err := extraFields(b, a)
	if err != nil {
		return err
	}
	f.Extra = extra
	return nil
}

// MarshalJSON re-emits the faction including retained fields.
func (f Faction) MarshalJSON() ([]byte, error) {
	type alias Faction
	return mergeExtras(alias(f), f.Extra)
}

// Deck is a deck or a published decklist. MarvelCDB returns the same shape for
// /deck/{id}, /decklist/{id}, and the OAuth2 deck endpoints; the extra fields
// each one adds survive in Extra.
type Deck struct {
	ID           int    `json:"id"`
	Name         string `json:"name,omitempty"`
	DateCreation string `json:"date_creation,omitempty"`
	DateUpdate   string `json:"date_update,omitempty"`
	DescriptionM string `json:"description_md,omitempty"`
	UserID       *int   `json:"user_id,omitempty"`
	HeroCode     string `json:"hero_code,omitempty"`
	HeroName     string `json:"hero_name,omitempty"`
	Version      string `json:"version,omitempty"`
	Meta         string `json:"meta,omitempty"`
	Tags         string `json:"tags,omitempty"`
	Problem      string `json:"problem,omitempty"`

	Slots                Slots `json:"slots,omitempty"`
	IgnoreDeckLimitSlots Slots `json:"ignoreDeckLimitSlots,omitempty"`

	Extra map[string]json.RawMessage `json:"-"`
}

// UnmarshalJSON decodes a deck, retaining unknown fields.
func (d *Deck) UnmarshalJSON(b []byte) error {
	type alias Deck
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*d = Deck(a)
	extra, err := extraFields(b, a)
	if err != nil {
		return err
	}
	d.Extra = extra
	return nil
}

// MarshalJSON re-emits the deck including retained fields.
func (d Deck) MarshalJSON() ([]byte, error) {
	type alias Deck
	return mergeExtras(alias(d), d.Extra)
}

// Aspect reads the aspect out of the deck's JSON-encoded meta field.
func (d Deck) Aspect() string {
	if d.Meta == "" {
		return ""
	}
	var m struct {
		Aspect string `json:"aspect"`
	}
	if err := json.Unmarshal([]byte(d.Meta), &m); err != nil {
		return ""
	}
	return m.Aspect
}

// CardCount totals the cards in the deck's main slot list.
func (d Deck) CardCount() int {
	n := 0
	for _, qty := range d.Slots {
		n += qty
	}
	return n
}

// Slots maps card code to quantity. MarvelCDB serialises an empty slot list as
// null, and quantities are sometimes strings, so decoding is lenient.
type Slots map[string]int

// UnmarshalJSON accepts null, an object, or an empty array for a slot list.
func (s *Slots) UnmarshalJSON(b []byte) error {
	trimmed := strings.TrimSpace(string(b))
	if trimmed == "null" || trimmed == "[]" || trimmed == "" {
		*s = nil
		return nil
	}
	var loose map[string]json.Number
	if err := json.Unmarshal(b, &loose); err != nil {
		return err
	}
	out := make(Slots, len(loose))
	for code, qty := range loose {
		n, err := qty.Int64()
		if err != nil {
			return err
		}
		out[code] = int(n)
	}
	*s = out
	return nil
}

// PopularDecklist is one entry of /decklists/popular/, which wraps a decklist
// alongside presentation metadata.
type PopularDecklist struct {
	Decklist Deck            `json:"decklist"`
	HeroMeta json.RawMessage `json:"hero_meta,omitempty"`
	Meta     json.RawMessage `json:"meta,omitempty"`
	Faction  json.RawMessage `json:"faction,omitempty"`
}

// FaqEntry is one rules clarification attached to a card.
type FaqEntry struct {
	Code string `json:"code"`
	HTML string `json:"html,omitempty"`
	Text string `json:"text,omitempty"`

	Extra map[string]json.RawMessage `json:"-"`
}

// UnmarshalJSON decodes a FAQ entry, retaining unknown fields.
func (f *FaqEntry) UnmarshalJSON(b []byte) error {
	type alias FaqEntry
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*f = FaqEntry(a)
	extra, err := extraFields(b, a)
	if err != nil {
		return err
	}
	f.Extra = extra
	return nil
}

// MarshalJSON re-emits the FAQ entry including retained fields.
func (f FaqEntry) MarshalJSON() ([]byte, error) {
	type alias FaqEntry
	return mergeExtras(alias(f), f.Extra)
}

// Taboo is an entry from the banned/restricted list.
type Taboo struct {
	ID    *int            `json:"id,omitempty"`
	Code  string          `json:"code,omitempty"`
	Name  string          `json:"name,omitempty"`
	Date  string          `json:"date_start,omitempty"`
	Cards json.RawMessage `json:"cards,omitempty"`

	Extra map[string]json.RawMessage `json:"-"`
}

// UnmarshalJSON decodes a taboo entry, retaining unknown fields.
func (t *Taboo) UnmarshalJSON(b []byte) error {
	type alias Taboo
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*t = Taboo(a)
	extra, err := extraFields(b, a)
	if err != nil {
		return err
	}
	t.Extra = extra
	return nil
}

// MarshalJSON re-emits the taboo entry including retained fields.
func (t Taboo) MarshalJSON() ([]byte, error) {
	type alias Taboo
	return mergeExtras(alias(t), t.Extra)
}

// MutationResult is the {success, msg} envelope the OAuth2 write endpoints
// return. On success msg carries the affected deck or decklist id.
type MutationResult struct {
	Success bool            `json:"success"`
	Msg     json.RawMessage `json:"msg,omitempty"`
}

// Message renders msg as text whether the server sent a string or a number.
func (m MutationResult) Message() string {
	if len(m.Msg) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(m.Msg, &s); err == nil {
		return s
	}
	return strings.Trim(string(m.Msg), `"`)
}

func intVal(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}
