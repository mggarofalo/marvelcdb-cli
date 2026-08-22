package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mggarofalo/marvelcdb-cli/internal/api"
	"github.com/mggarofalo/marvelcdb-cli/internal/output"
)

// deckTable renders decks and decklists for the row-oriented formats.
func deckTable(decks []api.Deck) *output.Table {
	t := &output.Table{
		Headers: []string{"ID", "NAME", "HERO", "ASPECT", "CARDS", "UPDATED"},
		Rows:    make([][]string, 0, len(decks)),
	}
	for _, d := range decks {
		t.Rows = append(t.Rows, []string{
			fmt.Sprintf("%d", d.ID),
			d.Name,
			d.HeroName,
			d.Aspect(),
			fmt.Sprintf("%d", d.CardCount()),
			shortDate(d.DateUpdate),
		})
	}
	return t
}

// cardIndex maps card codes to cards, for resolving deck slots to names.
type cardIndex map[string]api.Card

// buildCardIndex fetches the corpus so deck slots can be shown as card names.
// A failure is not fatal: the deck still renders, just with codes only.
func buildCardIndex(ctx context.Context, client *api.Client) cardIndex {
	cards, err := client.Cards(ctx, true)
	if err != nil {
		return nil
	}
	idx := make(cardIndex, len(cards))
	for _, c := range cards {
		idx[c.Code] = c
	}
	return idx
}

// deckSheet renders a deck as readable text. When idx is nil the slot list
// falls back to bare card codes.
func deckSheet(d api.Deck, idx cardIndex) string {
	var b strings.Builder

	fmt.Fprintf(&b, "%s  [#%d]\n", d.Name, d.ID)
	meta := nonEmpty(d.HeroName, titleCase(d.Aspect()))
	if len(meta) > 0 {
		fmt.Fprintf(&b, "%s\n", strings.Join(meta, " · "))
	}
	fmt.Fprintf(&b, "%d cards", d.CardCount())
	if d.DateUpdate != "" {
		fmt.Fprintf(&b, " · updated %s", shortDate(d.DateUpdate))
	}
	if d.Problem != "" {
		fmt.Fprintf(&b, " · problem: %s", strings.ReplaceAll(d.Problem, "_", " "))
	}
	b.WriteString("\n")
	if d.Tags != "" {
		fmt.Fprintf(&b, "Tags: %s\n", d.Tags)
	}

	if len(d.Slots) > 0 {
		b.WriteString("\n")
		writeSlots(&b, "Deck", d.Slots, idx)
	}
	if len(d.IgnoreDeckLimitSlots) > 0 {
		b.WriteString("\n")
		writeSlots(&b, "Ignoring deck limit", d.IgnoreDeckLimitSlots, idx)
	}

	if d.DescriptionM != "" {
		fmt.Fprintf(&b, "\n%s\n", strings.TrimSpace(d.DescriptionM))
	}
	return b.String()
}

// writeSlots prints a slot list, grouped by card type when names are available.
func writeSlots(b *strings.Builder, heading string, slots api.Slots, idx cardIndex) {
	fmt.Fprintf(b, "%s\n", heading)

	type line struct {
		group string
		label string
		qty   int
		code  string
	}
	lines := make([]line, 0, len(slots))
	for code, qty := range slots {
		l := line{group: "Cards", label: code, qty: qty, code: code}
		if c, ok := idx[code]; ok {
			l.group = orDefault(c.TypeName, "Cards")
			l.label = c.Name
			if c.Subname != "" {
				l.label += " (" + c.Subname + ")"
			}
			if cost := c.Cost; cost != nil {
				l.label = fmt.Sprintf("%s [%d]", l.label, *cost)
			}
		}
		lines = append(lines, l)
	}
	sort.Slice(lines, func(i, j int) bool {
		if lines[i].group != lines[j].group {
			return lines[i].group < lines[j].group
		}
		if !strings.EqualFold(lines[i].label, lines[j].label) {
			return strings.ToLower(lines[i].label) < strings.ToLower(lines[j].label)
		}
		return lines[i].code < lines[j].code
	})

	current := ""
	for _, l := range lines {
		if l.group != current {
			current = l.group
			fmt.Fprintf(b, "  %s\n", current)
		}
		fmt.Fprintf(b, "    %2dx %-44s %s\n", l.qty, output.Truncate(l.label, 44), l.code)
	}
}

func shortDate(raw string) string {
	if raw == "" {
		return ""
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05Z0700", "2006-01-02"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.Format("2006-01-02")
		}
	}
	if len(raw) >= 10 {
		return raw[:10]
	}
	return raw
}

func titleCase(s string) string {
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func errUnknownSort(field, allowed string) error {
	return fmt.Errorf("unknown sort field %q; choose one of %s", field, allowed)
}
