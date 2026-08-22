package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mggarofalo/marvelcdb-cli/internal/api"
)

func testCards() []api.Card {
	return []api.Card{
		{
			Code: "01050", Name: "Hulk", Subname: "Bruce Banner",
			TypeCode: "ally", TypeName: "Ally", FactionCode: "aggression", FactionName: "Aggression",
			PackCode: "core", CardSetCode: "hulk", Cost: ptr(2), Traits: "Avenger. Gamma.",
			Text: "Forced Response: discard the top card.", IsUnique: ptr(true),
		},
		{
			Code: "01067", Name: "Maria Hill",
			TypeCode: "ally", TypeName: "Ally", FactionCode: "leadership", FactionName: "Leadership",
			PackCode: "core", Cost: ptr(2), Traits: "S.H.I.E.L.D.", IsUnique: ptr(true),
		},
		{
			Code: "01086", Name: "First Aid",
			TypeCode: "event", TypeName: "Event", FactionCode: "protection", FactionName: "Protection",
			PackCode: "core", Cost: ptr(1), Text: "Heal 5 damage.", IsUnique: ptr(false),
		},
		{
			Code: "28011", Name: "Hulk", DuplicateOfCode: "01050",
			TypeCode: "ally", TypeName: "Ally", FactionCode: "aggression",
			PackCode: "shdw", Cost: ptr(2), Traits: "Avenger.",
		},
		{
			Code: "01130", Name: "Rhino", TypeCode: "villain", FactionCode: "encounter",
			PackCode: "core", Spoiler: ptr(1), Permanent: ptr(true),
		},
	}
}

func TestCardFilter(t *testing.T) {
	t.Parallel()

	yes, no := true, false

	tests := []struct {
		name   string
		filter cardFilter
		want   []string
	}{
		{"no filters hides reprints", cardFilter{}, []string{"01050", "01067", "01086", "01130"}},
		{"reprints on request", cardFilter{includeDuplicates: true}, []string{"01050", "01067", "01086", "28011", "01130"}},
		{"by type", cardFilter{types: []string{"ally"}}, []string{"01050", "01067"}},
		{"type is case-insensitive", cardFilter{types: []string{"ALLY"}}, []string{"01050", "01067"}},
		{"repeated faction widens", cardFilter{factions: []string{"aggression", "leadership"}}, []string{"01050", "01067"}},
		{"by pack", cardFilter{packs: []string{"core"}}, []string{"01050", "01067", "01086", "01130"}},
		{"by set", cardFilter{sets: []string{"hulk"}}, []string{"01050"}},
		{"by trait", cardFilter{traits: []string{"Avenger"}}, []string{"01050"}},
		{"trait matches any", cardFilter{traits: []string{"Gamma", "S.H.I.E.L.D."}}, []string{"01050", "01067"}},
		{"by name substring", cardFilter{name: "hill"}, []string{"01067"}},
		{"name matches the subname too", cardFilter{name: "banner"}, []string{"01050"}},
		{"by text", cardFilter{text: "heal"}, []string{"01086"}},
		{"search spans fields", cardFilter{search: "gamma"}, []string{"01050"}},
		{"exact cost", cardFilter{costs: []int{1}}, []string{"01086"}},
		{"max cost", cardFilter{maxCost: ptr(1)}, []string{"01086"}},
		{"min cost", cardFilter{minCost: ptr(2)}, []string{"01050", "01067"}},
		{"unique only", cardFilter{unique: &yes}, []string{"01050", "01067"}},
		{"non-unique only", cardFilter{unique: &no}, []string{"01086", "01130"}},
		{"permanent only", cardFilter{permanent: &yes}, []string{"01130"}},
		{"hide spoilers", cardFilter{hideSpoilers: true}, []string{"01050", "01067", "01086"}},
		{"filters combine", cardFilter{types: []string{"ally"}, maxCost: ptr(2), factions: []string{"leadership"}}, []string{"01067"}},
		{"no match", cardFilter{name: "galactus"}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := codesOf(tt.filter.apply(testCards()))
			assertCodes(t, got, tt.want)
		})
	}
}

// A cost filter must not silently match cards that have no printed cost.
func TestCostFiltersSkipCardsWithoutACost(t *testing.T) {
	t.Parallel()

	cards := []api.Card{{Code: "no-cost"}, {Code: "free", Cost: ptr(0)}}
	got := codesOf(cardFilter{maxCost: ptr(0)}.apply(cards))
	assertCodes(t, got, []string{"free"})
}

func TestSortCards(t *testing.T) {
	t.Parallel()

	tests := []struct {
		field   string
		reverse bool
		want    []string
	}{
		{"code", false, []string{"01050", "01067", "01086", "01130", "28011"}},
		{"code", true, []string{"28011", "01130", "01086", "01067", "01050"}},
		{"name", false, []string{"01086", "01050", "28011", "01067", "01130"}},
		{"cost", false, []string{"01130", "01086", "01050", "01067", "28011"}},
		{"faction", false, []string{"01050", "28011", "01130", "01067", "01086"}},
		{"pack", false, []string{"01050", "01067", "01086", "01130", "28011"}},
	}
	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			t.Parallel()
			cards := testCards()
			if err := sortCards(cards, tt.field, tt.reverse); err != nil {
				t.Fatalf("sortCards: %v", err)
			}
			assertCodes(t, codesOf(cards), tt.want)
		})
	}

	if err := sortCards(testCards(), "nonsense", false); err == nil {
		t.Error("an unknown sort field should be rejected")
	}
}

func TestSortPacks(t *testing.T) {
	t.Parallel()

	packs := []api.Pack{
		{Code: "gob", Name: "The Green Goblin", Position: ptr(2), Available: "2019-12-20"},
		{Code: "core", Name: "Core Set", Position: ptr(1), Available: "2019-11-01"},
		{Code: "twc", Name: "The Wrecking Crew", Position: ptr(3), Available: "2020-02-07"},
	}
	if err := sortPacks(packs, "released", true); err != nil {
		t.Fatalf("sortPacks: %v", err)
	}
	if packs[0].Code != "twc" {
		t.Errorf("newest-first sort put %q first", packs[0].Code)
	}
	if err := sortPacks(packs, "nonsense", false); err == nil {
		t.Error("an unknown sort field should be rejected")
	}
}

func TestResolveDate(t *testing.T) {
	t.Parallel()

	now := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"2024-01-15", "2024-01-15", false},
		{"today", "2024-01-15", false},
		{"Yesterday", "2024-01-14", false},
		{"15/01/2024", "", true},
		{"", "", true},
	}
	for _, tt := range tests {
		got, err := resolveDate(tt.in, now)
		if (err != nil) != tt.wantErr {
			t.Errorf("resolveDate(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			continue
		}
		if got != tt.want {
			t.Errorf("resolveDate(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestParseIDs(t *testing.T) {
	t.Parallel()

	got, err := parseIDs([]string{"123", "https://marvelcdb.com/decklist/view/456"})
	if err != nil {
		t.Fatalf("parseIDs: %v", err)
	}
	if len(got) != 2 || got[0] != 123 || got[1] != 456 {
		t.Errorf("parseIDs = %v, want [123 456]", got)
	}
	if _, err := parseIDs([]string{"not-a-number"}); err == nil {
		t.Error("a non-numeric id should be rejected")
	}
}

func TestCollectSlots(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		pairs   []string
		want    api.Slots
		wantErr bool
	}{
		{"pairs", []string{"01050=2", "01051=1"}, api.Slots{"01050": 2, "01051": 1}, false},
		{"zero removes", []string{"01050=2", "01050=0"}, nil, false},
		{"no equals sign", []string{"01050"}, nil, true},
		{"non-numeric quantity", []string{"01050=two"}, nil, true},
		{"missing code", []string{"=2"}, nil, true},
		{"nothing at all", nil, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := collectSlots(tt.pairs, "")
			if (err != nil) != tt.wantErr {
				t.Fatalf("collectSlots error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("collectSlots = %v, want %v", got, tt.want)
			}
			for code, qty := range tt.want {
				if got[code] != qty {
					t.Errorf("slot %s = %d, want %d", code, got[code], qty)
				}
			}
		})
	}
}

func TestParseSlotsJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    int
		wantErr bool
	}{
		{"bare map", `{"01050":2}`, 1, false},
		{"whole deck object", `{"id":1,"slots":{"01050":2,"01051":1}}`, 2, false},
		{"not an object", `[1,2,3]`, 0, true},
		{"garbage", `nope`, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseSlotsJSON([]byte(tt.input))
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseSlotsJSON error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && len(got) != tt.want {
				t.Errorf("parseSlotsJSON = %v, want %d entries", got, tt.want)
			}
		})
	}
}

func TestResolveMeta(t *testing.T) {
	t.Parallel()

	got, err := resolveMeta("", "Justice")
	if err != nil {
		t.Fatalf("resolveMeta: %v", err)
	}
	if got != `{"aspect":"justice"}` {
		t.Errorf("resolveMeta = %q", got)
	}

	if got, err := resolveMeta(`{"aspect":"hero"}`, ""); err != nil || got != `{"aspect":"hero"}` {
		t.Errorf("resolveMeta passed through = %q, %v", got, err)
	}
	if _, err := resolveMeta(`{}`, "justice"); err == nil {
		t.Error("--aspect together with --meta should be rejected")
	}
}

func TestAbsoluteURL(t *testing.T) {
	t.Parallel()

	tests := []struct{ base, path, want string }{
		{"https://marvelcdb.com", "/bundles/cards/01001a.png", "https://marvelcdb.com/bundles/cards/01001a.png"},
		{"https://marvelcdb.com/", "bundles/x.png", "https://marvelcdb.com/bundles/x.png"},
		{"https://marvelcdb.com", "https://cdn.example.com/x.png", "https://cdn.example.com/x.png"},
	}
	for _, tt := range tests {
		if got := absoluteURL(tt.base, tt.path); got != tt.want {
			t.Errorf("absoluteURL(%q, %q) = %q, want %q", tt.base, tt.path, got, tt.want)
		}
	}
}

func TestHumanBytes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{2048, "2.0 KB"},
		{5 * 1024 * 1024, "5.0 MB"},
	}
	for _, tt := range tests {
		if got := humanBytes(tt.in); got != tt.want {
			t.Errorf("humanBytes(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestShortDate(t *testing.T) {
	t.Parallel()

	tests := []struct{ in, want string }{
		{"2024-01-15T00:10:13+00:00", "2024-01-15"},
		{"2024-01-15", "2024-01-15"},
		{"", ""},
		{"garbage", "garbage"},
	}
	for _, tt := range tests {
		if got := shortDate(tt.in); got != tt.want {
			t.Errorf("shortDate(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func codesOf(cards []api.Card) []string {
	out := make([]string, 0, len(cards))
	for _, c := range cards {
		out = append(out, c.Code)
	}
	return out
}

func assertCodes(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("codes = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("codes = %v, want %v", got, want)
		}
	}
}

func ptr[T any](v T) *T { return &v }

// MarvelCDB writes traits like "Avenger. S.H.I.E.L.D." — a trait can carry its
// own periods, so the separator is ". ", not ".".
func TestTraitMatchingHandlesInitialisms(t *testing.T) {
	t.Parallel()

	card := api.Card{Code: "01067", Traits: "S.H.I.E.L.D. Avenger."}
	traits := card.TraitList()
	if len(traits) != 2 || traits[0] != "S.H.I.E.L.D" || traits[1] != "Avenger" {
		t.Fatalf("TraitList = %#v, want [S.H.I.E.L.D Avenger]", traits)
	}

	for _, query := range []string{"S.H.I.E.L.D.", "s.h.i.e.l.d", "SHIELD", "shield"} {
		if !hasAnyTrait(card, []string{query}) {
			t.Errorf("--trait %q did not match %v", query, traits)
		}
	}
	if hasAnyTrait(card, []string{"Aerial"}) {
		t.Error("--trait Aerial should not match")
	}
}

func TestCardSheetShowsStatsAndStripsMarkup(t *testing.T) {
	t.Parallel()

	card := api.Card{
		Code: "01001a", Name: "Spider-Man", Subname: "Peter Parker",
		TypeName: "Hero", FactionName: "Hero", CardSetName: "Spider-Man",
		Traits: "Avenger.", Attack: ptr(2), Thwart: ptr(1), Defense: ptr(3),
		Health: ptr(10), HandSize: ptr(5), DeckLimit: ptr(1),
		ResourcePhysical: ptr(1), ResourceWild: ptr(2),
		Text:     "<b>Interrupt</b>: draw 1 card.",
		Flavor:   "Your friendly neighborhood Spider-Man!",
		Errata:   "Removed the label.",
		BackName: "Peter Parker", BackText: "<i>Alter-ego</i>",
		PackName: "Core Set", Illustrator: "Someone",
		LinkedToCode: "01001b", LinkedToName: "Peter Parker",
		URL: "https://marvelcdb.com/card/01001a",
	}

	sheet := cardSheet(card)
	for _, want := range []string{
		"Spider-Man — Peter Parker", "[01001a]", "Attack:", "Health:",
		"1 physical, 2 wild", "Interrupt: draw 1 card.", "reverse",
		"Errata:", "Illustrator: Someone", "Linked to:",
	} {
		if !strings.Contains(sheet, want) {
			t.Errorf("card sheet missing %q:\n%s", want, sheet)
		}
	}
	// The sheet is plain text; markup must not survive.
	if strings.Contains(sheet, "<b>") || strings.Contains(sheet, "<i>") {
		t.Errorf("card sheet leaked HTML:\n%s", sheet)
	}
	// A stat the card does not print must not appear as a zero.
	if strings.Contains(sheet, "Scheme:") {
		t.Errorf("card sheet invented a scheme value:\n%s", sheet)
	}
}

func TestDeckSheetGroupsByType(t *testing.T) {
	t.Parallel()

	deck := api.Deck{
		ID: 1, Name: "Test deck", HeroName: "Spider-Man",
		Meta:  `{"aspect":"justice"}`,
		Tags:  "justice",
		Slots: api.Slots{"01067": 2, "01086": 1, "unknown": 1},
	}
	idx := cardIndex{
		"01067": {Code: "01067", Name: "Maria Hill", TypeName: "Ally", Cost: ptr(2)},
		"01086": {Code: "01086", Name: "First Aid", TypeName: "Event", Cost: ptr(1)},
	}

	sheet := deckSheet(deck, idx)
	for _, want := range []string{"Test deck", "[#1]", "Spider-Man · Justice", "4 cards", "Ally", "Event", "Maria Hill", "unknown"} {
		if !strings.Contains(sheet, want) {
			t.Errorf("deck sheet missing %q:\n%s", want, sheet)
		}
	}

	// Without an index the sheet still renders, using bare codes.
	bare := deckSheet(deck, nil)
	if !strings.Contains(bare, "01067") {
		t.Errorf("deck sheet without an index:\n%s", bare)
	}
}

func TestCollectSlotsFromFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "deck.json")
	if err := os.WriteFile(path, []byte(`{"id":1,"slots":{"01067":2,"01086":1}}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Explicit --slot pairs layer on top of the file.
	got, err := collectSlots([]string{"01086=3"}, path)
	if err != nil {
		t.Fatalf("collectSlots: %v", err)
	}
	if got["01067"] != 2 || got["01086"] != 3 {
		t.Errorf("slots = %v, want the file merged with the flag", got)
	}

	if _, err := collectSlots(nil, filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Error("a missing slots file should be an error")
	}
}

func TestCardTableColumns(t *testing.T) {
	t.Parallel()

	cards := []api.Card{{
		Code: "01067", Name: "Maria Hill", Subname: "Agent",
		TypeName: "Ally", FactionName: "Leadership", PackName: "Core Set",
		CardSetName: "S.H.I.E.L.D.", Traits: "S.H.I.E.L.D.",
		Text: "<b>Response</b>: draw.", Cost: ptr(2),
	}}

	narrow := cardTable(cards, false)
	if len(narrow.Headers) != 6 {
		t.Errorf("narrow table has %d columns, want 6", len(narrow.Headers))
	}
	if narrow.Rows[0][1] != "Maria Hill (Agent)" {
		t.Errorf("name cell = %q", narrow.Rows[0][1])
	}

	wide := cardTable(cards, true)
	if len(wide.Headers) != 9 {
		t.Errorf("wide table has %d columns, want 9", len(wide.Headers))
	}
	if strings.Contains(wide.Rows[0][8], "<b>") {
		t.Errorf("text column leaked HTML: %q", wide.Rows[0][8])
	}

	// A card with no printed cost shows a dash, never a misleading zero.
	noCost := cardTable([]api.Card{{Code: "x"}}, false)
	if noCost.Rows[0][4] != "-" {
		t.Errorf("missing cost rendered as %q, want -", noCost.Rows[0][4])
	}
}
