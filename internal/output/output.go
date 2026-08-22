// Package output renders command results in the format the user asked for.
package output

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"text/tabwriter"

	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

// Format is a rendering style.
type Format string

// The supported output formats.
const (
	FormatTable Format = "table"
	FormatJSON  Format = "json"
	FormatYAML  Format = "yaml"
	FormatCSV   Format = "csv"
	FormatTSV   Format = "tsv"
	FormatIDs   Format = "ids"
	FormatText  Format = "text"
)

// Formats lists every supported format, for help text and completions.
func Formats() []string {
	return []string{
		string(FormatTable), string(FormatJSON), string(FormatYAML),
		string(FormatCSV), string(FormatTSV), string(FormatIDs), string(FormatText),
	}
}

// ParseFormat validates a format name.
func ParseFormat(s string) (Format, error) {
	f := Format(strings.ToLower(strings.TrimSpace(s)))
	switch f {
	case FormatTable, FormatJSON, FormatYAML, FormatCSV, FormatTSV, FormatIDs, FormatText:
		return f, nil
	case "":
		return FormatTable, nil
	default:
		return "", fmt.Errorf("unknown output format %q; choose one of %s", s, strings.Join(Formats(), ", "))
	}
}

// Table is a rectangular result: one header row and zero or more data rows.
type Table struct {
	Headers []string
	Rows    [][]string
	// Text is the long-form rendering used by the "text" format. When empty,
	// the text format falls back to the table.
	Text string
}

// Printer writes results.
type Printer struct {
	Out       io.Writer
	Err       io.Writer
	Format    Format
	NoHeaders bool
	Color     bool
}

// New returns a Printer writing to stdout and stderr.
func New(format Format, color bool) *Printer {
	return &Printer{Out: os.Stdout, Err: os.Stderr, Format: format, Color: color}
}

// ColorEnabled resolves an auto/always/never preference against the terminal.
func ColorEnabled(pref string, out *os.File) bool {
	switch pref {
	case "always":
		return true
	case "never":
		return false
	default:
		if os.Getenv("NO_COLOR") != "" {
			return false
		}
		return term.IsTerminal(int(out.Fd())) //nolint:gosec // G115: a file descriptor always fits in an int
	}
}

// Print renders data. structured is what json and yaml serialise; table is what
// the row-oriented formats render. Either may be nil when the format in use
// does not need it.
func (p *Printer) Print(structured any, table *Table) error {
	switch p.Format {
	case FormatJSON:
		return p.printJSON(structured)
	case FormatYAML:
		return p.printYAML(structured)
	case FormatCSV:
		return p.printSeparated(table, ',')
	case FormatTSV:
		return p.printSeparated(table, '\t')
	case FormatIDs:
		return p.printIDs(table)
	case FormatText:
		if table != nil && table.Text != "" {
			_, err := fmt.Fprint(p.Out, ensureNewline(table.Text))
			return err
		}
		return p.printTable(table)
	default:
		return p.printTable(table)
	}
}

func (p *Printer) printJSON(v any) error {
	enc := json.NewEncoder(p.Out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("encode JSON: %w", err)
	}
	return nil
}

// printYAML routes through JSON so that the models' json tags — and the extra
// fields they preserve — decide the shape.
func (p *Printer) printYAML(v any) error {
	blob, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode for YAML: %w", err)
	}
	var generic any
	if err := yaml.Unmarshal(blob, &generic); err != nil {
		return fmt.Errorf("convert to YAML: %w", err)
	}
	enc := yaml.NewEncoder(p.Out)
	enc.SetIndent(2)
	if err := enc.Encode(generic); err != nil {
		return fmt.Errorf("write YAML: %w", err)
	}
	return enc.Close()
}

func (p *Printer) printTable(t *Table) error {
	if t == nil {
		return nil
	}
	w := tabwriter.NewWriter(p.Out, 0, 0, 2, ' ', 0)
	if !p.NoHeaders && len(t.Headers) > 0 {
		if _, err := fmt.Fprintln(w, p.headerLine(t.Headers)); err != nil {
			return err
		}
	}
	for _, row := range t.Rows {
		if _, err := fmt.Fprintln(w, strings.Join(sanitiseRow(row), "\t")); err != nil {
			return err
		}
	}
	return w.Flush()
}

func (p *Printer) headerLine(headers []string) string {
	upper := make([]string, len(headers))
	for i, h := range headers {
		upper[i] = strings.ToUpper(h)
	}
	line := strings.Join(upper, "\t")
	if p.Color {
		return "\x1b[1m" + line + "\x1b[0m"
	}
	return line
}

func (p *Printer) printSeparated(t *Table, sep rune) error {
	if t == nil {
		return nil
	}
	w := csv.NewWriter(p.Out)
	w.Comma = sep
	if !p.NoHeaders && len(t.Headers) > 0 {
		if err := w.Write(t.Headers); err != nil {
			return fmt.Errorf("write header row: %w", err)
		}
	}
	for _, row := range t.Rows {
		if err := w.Write(row); err != nil {
			return fmt.Errorf("write row: %w", err)
		}
	}
	w.Flush()
	return w.Error()
}

// printIDs writes the first column only, which makes results easy to pipe into
// xargs.
func (p *Printer) printIDs(t *Table) error {
	if t == nil {
		return nil
	}
	for _, row := range t.Rows {
		if len(row) == 0 {
			continue
		}
		if _, err := fmt.Fprintln(p.Out, row[0]); err != nil {
			return err
		}
	}
	return nil
}

// Warn writes an advisory line to stderr so it never pollutes piped output.
func (p *Printer) Warn(format string, args ...any) {
	if p.Err == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	if p.Color {
		msg = "\x1b[33m" + msg + "\x1b[0m"
	}
	_, _ = fmt.Fprintln(p.Err, msg)
}

// Structured reports whether the current format serialises the raw objects,
// which callers use to decide whether to build a table at all.
func (p *Printer) Structured() bool {
	return p.Format == FormatJSON || p.Format == FormatYAML
}

var (
	tagRe    = regexp.MustCompile(`<[^>]*>`)
	spacesRe = regexp.MustCompile(`[ \t]+`)
)

// StripHTML turns MarvelCDB's marked-up card text into plain text. Card text
// arrives with <b>, <i> and <br> in it, which is unreadable in a table cell.
func StripHTML(s string) string {
	if s == "" {
		return ""
	}
	s = strings.ReplaceAll(s, "<br>", "\n")
	s = strings.ReplaceAll(s, "<br/>", "\n")
	s = strings.ReplaceAll(s, "<br />", "\n")
	s = tagRe.ReplaceAllString(s, "")
	s = htmlUnescape(s)
	s = spacesRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// Flatten collapses whitespace so a multi-line value fits one table cell.
func Flatten(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	return strings.TrimSpace(spacesRe.ReplaceAllString(s, " "))
}

// Truncate shortens s to at most n characters, counting runes not bytes.
func Truncate(s string, n int) string {
	if n <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	return string(runes[:n-1]) + "…"
}

// sanitiseRow keeps a stray newline or tab from breaking column alignment.
func sanitiseRow(row []string) []string {
	out := make([]string, len(row))
	for i, cell := range row {
		out[i] = Flatten(cell)
	}
	return out
}

func ensureNewline(s string) string {
	if strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}
