package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mggarofalo/marvelcdb-cli/internal/api"
	"github.com/mggarofalo/marvelcdb-cli/internal/output"
)

// cardFilter is the set of predicates `cards list` applies locally.
//
// MarvelCDB has no server-side query language, so the CLI fetches the corpus
// once, caches it, and filters here. That is why the cache matters so much.
type cardFilter struct {
	packs    []string
	types    []string
	factions []string
	sets     []string
	traits   []string

	name   string
	text   string
	search string

	costs     []int
	minCost   *int
	maxCost   *int
	unique    *bool
	permanent *bool

	includeDuplicates bool
	hideSpoilers      bool
}

func (f cardFilter) matches(c api.Card) bool {
	if !matchesAny(f.packs, c.PackCode) {
		return false
	}
	if !matchesAny(f.types, c.TypeCode) {
		return false
	}
	if !matchesAny(f.factions, c.FactionCode) {
		return false
	}
	if !matchesAny(f.sets, c.CardSetCode) {
		return false
	}
	if len(f.traits) > 0 && !hasAnyTrait(c, f.traits) {
		return false
	}
	if f.name != "" && !containsFold(c.Name+" "+c.RealName+" "+c.Subname, f.name) {
		return false
	}
	if f.text != "" && !containsFold(c.Text+" "+c.RealText+" "+c.BackText, f.text) {
		return false
	}
	if f.search != "" {
		haystack := strings.Join([]string{
			c.Name, c.RealName, c.Subname, c.Text, c.RealText,
			c.BackText, c.Traits, c.RealTraits, c.Flavor, c.Code,
		}, " ")
		if !containsFold(haystack, f.search) {
			return false
		}
	}
	if len(f.costs) > 0 {
		if c.Cost == nil || !containsInt(f.costs, *c.Cost) {
			return false
		}
	}
	if f.minCost != nil && (c.Cost == nil || *c.Cost < *f.minCost) {
		return false
	}
	if f.maxCost != nil && (c.Cost == nil || *c.Cost > *f.maxCost) {
		return false
	}
	if f.unique != nil && boolValue(c.IsUnique) != *f.unique {
		return false
	}
	if f.permanent != nil && boolValue(c.Permanent) != *f.permanent {
		return false
	}
	if !f.includeDuplicates && c.DuplicateOfCode != "" {
		return false
	}
	if f.hideSpoilers && c.Spoiler != nil && *c.Spoiler != 0 {
		return false
	}
	return true
}

// apply filters cards in place order, preserving the server's ordering.
func (f cardFilter) apply(cards []api.Card) []api.Card {
	out := make([]api.Card, 0, len(cards))
	for _, c := range cards {
		if f.matches(c) {
			out = append(out, c)
		}
	}
	return out
}

func matchesAny(allowed []string, value string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, a := range allowed {
		if strings.EqualFold(a, value) {
			return true
		}
	}
	return false
}

func hasAnyTrait(c api.Card, wanted []string) bool {
	traits := c.TraitList()
	for _, w := range wanted {
		want := normaliseTrait(w)
		for _, t := range traits {
			if normaliseTrait(t) == want {
				return true
			}
		}
	}
	return false
}

// normaliseTrait folds case and punctuation so that --trait shield, --trait
// S.H.I.E.L.D and --trait "S.H.I.E.L.D." all find the same cards.
func normaliseTrait(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r != '.' && r != ' ' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func containsFold(haystack, needle string) bool {
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}

func containsInt(haystack []int, needle int) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}

func boolValue(p *bool) bool { return p != nil && *p }

func intValue(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// sortFields are the orderings `cards list --sort` understands.
var sortFields = []string{"code", "name", "cost", "faction", "type", "pack", "set", "position"}

func sortCards(cards []api.Card, field string, reverse bool) error {
	var less func(a, b api.Card) bool
	switch strings.ToLower(field) {
	case "", "code":
		less = func(a, b api.Card) bool { return a.Code < b.Code }
	case "name":
		less = func(a, b api.Card) bool { return foldLess(a.Name, b.Name, a.Code, b.Code) }
	case "cost":
		less = func(a, b api.Card) bool {
			if intValue(a.Cost) != intValue(b.Cost) {
				return intValue(a.Cost) < intValue(b.Cost)
			}
			return a.Code < b.Code
		}
	case "faction":
		less = func(a, b api.Card) bool { return foldLess(a.FactionCode, b.FactionCode, a.Code, b.Code) }
	case "type":
		less = func(a, b api.Card) bool { return foldLess(a.TypeCode, b.TypeCode, a.Code, b.Code) }
	case "pack":
		less = func(a, b api.Card) bool { return foldLess(a.PackCode, b.PackCode, a.Code, b.Code) }
	case "set":
		less = func(a, b api.Card) bool { return foldLess(a.CardSetCode, b.CardSetCode, a.Code, b.Code) }
	case "position":
		less = func(a, b api.Card) bool {
			if intValue(a.Position) != intValue(b.Position) {
				return intValue(a.Position) < intValue(b.Position)
			}
			return a.Code < b.Code
		}
	default:
		return fmt.Errorf("unknown sort field %q; choose one of %s", field, strings.Join(sortFields, ", "))
	}
	sort.SliceStable(cards, func(i, j int) bool {
		if reverse {
			return less(cards[j], cards[i])
		}
		return less(cards[i], cards[j])
	})
	return nil
}

func foldLess(a, b, tieA, tieB string) bool {
	la, lb := strings.ToLower(a), strings.ToLower(b)
	if la != lb {
		return la < lb
	}
	return tieA < tieB
}

// cardTable renders cards for the row-oriented formats.
func cardTable(cards []api.Card, wide bool) *output.Table {
	headers := []string{"CODE", "NAME", "TYPE", "FACTION", "COST", "PACK"}
	if wide {
		headers = append(headers, "SET", "TRAITS", "TEXT")
	}
	t := &output.Table{Headers: headers, Rows: make([][]string, 0, len(cards))}
	for _, c := range cards {
		name := c.Name
		if c.Subname != "" {
			name += " (" + c.Subname + ")"
		}
		row := []string{
			c.Code,
			name,
			c.TypeName,
			c.FactionName,
			optionalInt(c.Cost),
			c.PackName,
		}
		if wide {
			row = append(row,
				c.CardSetName,
				c.Traits,
				output.Truncate(output.Flatten(output.StripHTML(c.Text)), 80),
			)
		}
		t.Rows = append(t.Rows, row)
	}
	return t
}

func optionalInt(p *int) string {
	if p == nil {
		return "-"
	}
	return fmt.Sprintf("%d", *p)
}
