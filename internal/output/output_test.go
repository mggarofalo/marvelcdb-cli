package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

type sample struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

func newTestPrinter(format Format) (*Printer, *bytes.Buffer, *bytes.Buffer) {
	var out, errBuf bytes.Buffer
	return &Printer{Out: &out, Err: &errBuf, Format: format}, &out, &errBuf
}

func TestParseFormat(t *testing.T) {
	t.Parallel()

	for _, name := range Formats() {
		if _, err := ParseFormat(name); err != nil {
			t.Errorf("ParseFormat(%q) = %v", name, err)
		}
	}
	if got, err := ParseFormat(""); err != nil || got != FormatTable {
		t.Errorf("ParseFormat(\"\") = %q, %v; want table", got, err)
	}
	if got, err := ParseFormat("  JSON "); err != nil || got != FormatJSON {
		t.Errorf("ParseFormat with padding and case = %q, %v", got, err)
	}
	if _, err := ParseFormat("xml"); err == nil {
		t.Error("ParseFormat(\"xml\") should fail")
	}
}

func TestFormats(t *testing.T) {
	t.Parallel()

	data := []sample{{Code: "01001a", Name: "Spider-Man"}, {Code: "01050", Name: "Hulk"}}
	table := &Table{
		Headers: []string{"CODE", "NAME"},
		Rows:    [][]string{{"01001a", "Spider-Man"}, {"01050", "Hulk"}},
		Text:    "Spider-Man\nHulk\n",
	}

	tests := []struct {
		format   Format
		contains []string
		absent   []string
	}{
		{FormatTable, []string{"CODE", "Spider-Man", "Hulk"}, nil},
		{FormatJSON, []string{`"code": "01001a"`, `"name": "Hulk"`}, nil},
		{FormatYAML, []string{"code: 01001a", "name: Hulk"}, nil},
		{FormatCSV, []string{"CODE,NAME", "01001a,Spider-Man"}, nil},
		{FormatTSV, []string{"01001a\tSpider-Man"}, nil},
		{FormatIDs, []string{"01001a\n", "01050\n"}, []string{"Spider-Man"}},
		{FormatText, []string{"Spider-Man\nHulk"}, []string{"CODE"}},
	}

	for _, tt := range tests {
		t.Run(string(tt.format), func(t *testing.T) {
			t.Parallel()
			p, out, _ := newTestPrinter(tt.format)
			if err := p.Print(data, table); err != nil {
				t.Fatalf("Print: %v", err)
			}
			got := out.String()
			for _, want := range tt.contains {
				if !strings.Contains(got, want) {
					t.Errorf("output missing %q:\n%s", want, got)
				}
			}
			for _, unwanted := range tt.absent {
				if strings.Contains(got, unwanted) {
					t.Errorf("output should not contain %q:\n%s", unwanted, got)
				}
			}
		})
	}
}

// The text format is meant for reading; when a command has nothing long-form to
// show it should still print something useful.
func TestTextFormatFallsBackToTheTable(t *testing.T) {
	t.Parallel()

	p, out, _ := newTestPrinter(FormatText)
	if err := p.Print(nil, &Table{Headers: []string{"CODE"}, Rows: [][]string{{"01001a"}}}); err != nil {
		t.Fatalf("Print: %v", err)
	}
	if !strings.Contains(out.String(), "01001a") {
		t.Errorf("output = %q", out.String())
	}
}

func TestNoHeaders(t *testing.T) {
	t.Parallel()

	for _, format := range []Format{FormatTable, FormatCSV} {
		p, out, _ := newTestPrinter(format)
		p.NoHeaders = true
		if err := p.Print(nil, &Table{Headers: []string{"CODE"}, Rows: [][]string{{"01001a"}}}); err != nil {
			t.Fatalf("Print: %v", err)
		}
		if strings.Contains(out.String(), "CODE") {
			t.Errorf("%s output still has a header row: %q", format, out.String())
		}
	}
}

// Card text is full of ampersands and angle brackets; escaping them turns
// readable output into mojibake.
func TestJSONDoesNotEscapeHTML(t *testing.T) {
	t.Parallel()

	p, out, _ := newTestPrinter(FormatJSON)
	if err := p.Print(map[string]string{"text": "<b>Hero</b> & Ally"}, nil); err != nil {
		t.Fatalf("Print: %v", err)
	}
	if !strings.Contains(out.String(), "<b>Hero</b> & Ally") {
		t.Errorf("output = %s", out.String())
	}
}

// YAML goes through JSON so the models' json tags decide the shape.
func TestYAMLUsesJSONTags(t *testing.T) {
	t.Parallel()

	type tagged struct {
		Field string `json:"json_name" yaml:"yaml_name"`
	}
	p, out, _ := newTestPrinter(FormatYAML)
	if err := p.Print(tagged{Field: "value"}, nil); err != nil {
		t.Fatalf("Print: %v", err)
	}
	if !strings.Contains(out.String(), "json_name") {
		t.Errorf("output = %q, want the json tag to win", out.String())
	}
}

func TestRowsWithNewlinesStayOnOneLine(t *testing.T) {
	t.Parallel()

	p, out, _ := newTestPrinter(FormatTable)
	table := &Table{Headers: []string{"A", "B"}, Rows: [][]string{{"one\ntwo", "three\tfour"}}}
	if err := p.Print(nil, table); err != nil {
		t.Fatalf("Print: %v", err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Errorf("expected a header and one row, got %d lines:\n%s", len(lines), out.String())
	}
}

func TestWarnGoesToStderr(t *testing.T) {
	t.Parallel()

	p, out, errBuf := newTestPrinter(FormatJSON)
	p.Warn("no cards matched %d filters", 3)
	if out.Len() != 0 {
		t.Errorf("warning polluted stdout: %q", out.String())
	}
	if !strings.Contains(errBuf.String(), "no cards matched 3 filters") {
		t.Errorf("stderr = %q", errBuf.String())
	}
}

func TestStructured(t *testing.T) {
	t.Parallel()

	for format, want := range map[Format]bool{
		FormatJSON: true, FormatYAML: true, FormatTable: false, FormatCSV: false,
	} {
		p, _, _ := newTestPrinter(format)
		if got := p.Structured(); got != want {
			t.Errorf("Structured() for %s = %v, want %v", format, got, want)
		}
	}
}

func TestStripHTML(t *testing.T) {
	t.Parallel()

	tests := []struct{ in, want string }{
		{"", ""},
		{"<b>Interrupt</b>: draw 1 card.", "Interrupt: draw 1 card."},
		{"line one<br>line two", "line one\nline two"},
		{"line one<br />line two", "line one\nline two"},
		{"Cost &mdash; 2", "Cost — 2"},
		{"&quot;quoted&quot;", `"quoted"`},
		{"  spaced   out  ", "spaced out"},
	}
	for _, tt := range tests {
		if got := StripHTML(tt.in); got != tt.want {
			t.Errorf("StripHTML(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestTruncateCountsRunes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		n    int
		want string
	}{
		{"short", 10, "short"},
		{"exactly-10", 10, "exactly-10"},
		{"much longer than ten", 10, "much long…"},
		{"café society", 6, "café …"},
		{"anything", 0, "anything"},
		{"anything", 1, "…"},
	}
	for _, tt := range tests {
		if got := Truncate(tt.in, tt.n); got != tt.want {
			t.Errorf("Truncate(%q, %d) = %q, want %q", tt.in, tt.n, got, tt.want)
		}
	}
	if got := len([]rune(Truncate("ααααααααααα", 5))); got != 5 {
		t.Errorf("Truncate produced %d runes, want 5", got)
	}
}

func TestFlatten(t *testing.T) {
	t.Parallel()

	if got := Flatten("a\r\nb\tc  d\n"); got != "a b c d" {
		t.Errorf("Flatten = %q", got)
	}
}

func TestPrintNilTableIsSafe(t *testing.T) {
	t.Parallel()

	for _, format := range []Format{FormatTable, FormatCSV, FormatIDs} {
		p, _, _ := newTestPrinter(format)
		if err := p.Print(nil, nil); err != nil {
			t.Errorf("Print(nil, nil) for %s = %v", format, err)
		}
	}
}

func TestJSONOutputIsValid(t *testing.T) {
	t.Parallel()

	p, out, _ := newTestPrinter(FormatJSON)
	if err := p.Print([]sample{{Code: "a"}}, nil); err != nil {
		t.Fatalf("Print: %v", err)
	}
	var decoded []sample
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("output was not valid JSON: %v", err)
	}
}
