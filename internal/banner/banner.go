// Package banner renders the Space Pirate Zero wordmark.
//
// The art is a specification, not decoration. It is pure 7-bit ASCII drawn from a
// nine-character allowlist, it fits a 40-column terminal, and it carries no trailing
// whitespace — so it renders identically in cmd.exe, a serial console, a CI log, a
// pager and a braille display. banner_test.go enforces every one of those claims.
//
// It is written to stderr and only when stderr is a terminal, because a banner on
// stdout would corrupt `hullcheck --json | jq`.
package banner

import (
	"io"
	"os"
	"strings"
)

// Full is the wordmark: SPACE / PIRATE / ZERO in slab caps, a starfield, and an
// orbital nucleus on the rule. 37 columns at its widest.
const Full = `   .        *            .         *

  ##### ##### ##### ##### #####
  #     #   # #   # #     #
  ##### ##### ##### #     #####
      # #     #   # #     #
  ##### #     #   # ##### #####

  ##### ##### ##### ##### ##### #####
  #   #   #   #   # #   #   #   #
  #####   #   ##### #####   #   #####
  #       #   #  #  #   #   #   #
  #     ##### #   # #   #   #   #####

  ##### ##### ##### #####
     #  #     #   # #   #
    #   ##### ##### #   #
   #    #     #  #  #   #
  ##### ##### #   # #####

  ------------------=[ o ]=---------
      *          .            *`

// Compact is used when the terminal is too narrow for the slab art.
const Compact = `  --=[ SPACE PIRATE ZERO ]=--`

// Minimal is the last resort for very narrow terminals.
const Minimal = `  SPZ // hullcheck`

// Width thresholds. Full is 37 wide and Compact 29, so each tier keeps a margin
// rather than fitting exactly — a banner that touches the last column wraps on
// terminals that count columns differently.
const (
	fullMinCols    = 40
	compactMinCols = 30
)

// glyphs is the 5x5 slab font the wordmark is drawn in. It exists so Decode can
// read the art back and prove it spells what we think it spells.
var glyphs = map[rune][5]string{
	'A': {"#####", "#   #", "#####", "#   #", "#   #"},
	'C': {"#####", "#    ", "#    ", "#    ", "#####"},
	'E': {"#####", "#    ", "#####", "#    ", "#####"},
	'I': {"#####", "  #  ", "  #  ", "  #  ", "#####"},
	'O': {"#####", "#   #", "#   #", "#   #", "#####"},
	'P': {"#####", "#   #", "#####", "#    ", "#    "},
	'R': {"#####", "#   #", "#####", "#  # ", "#   #"},
	'S': {"#####", "#    ", "#####", "    #", "#####"},
	'T': {"#####", "  #  ", "  #  ", "  #  ", "  #  "},
	'Z': {"#####", "   # ", "  #  ", " #   ", "#####"},
}

// For returns the widest banner that fits the given terminal width.
func For(cols int) string {
	switch {
	case cols >= fullMinCols:
		return Full
	case cols >= compactMinCols:
		return Compact
	default:
		return Minimal
	}
}

// Decode reads slab art back into the words it spells. Blocks are runs of five
// consecutive lines containing glyph ink; cells are five columns wide, separated
// by one. Lines are padded first, because the art carries no trailing whitespace.
func Decode(art string) []string {
	var words []string
	lines := strings.Split(art, "\n")
	for i := 0; i+4 < len(lines); {
		block := lines[i : i+5]
		if !allInk(block) {
			i++
			continue
		}
		if w := decodeBlock(block); w != "" {
			words = append(words, w)
		}
		i += 5
	}
	return words
}

func allInk(block []string) bool {
	for _, l := range block {
		if !strings.Contains(l, "#") {
			return false
		}
	}
	return true
}

func decodeBlock(block []string) string {
	width := 0
	for _, l := range block {
		if len(l) > width {
			width = len(l)
		}
	}
	rows := make([]string, 5)
	for r, l := range block {
		rows[r] = l + strings.Repeat(" ", width-len(l))
	}
	// Strip the shared left indent so cells start at column zero.
	indent := width
	for _, r := range rows {
		if n := len(r) - len(strings.TrimLeft(r, " ")); n < indent {
			indent = n
		}
	}
	for r := range rows {
		rows[r] = rows[r][indent:]
	}

	var sb strings.Builder
	for start := 0; start < len(rows[0]); start += 6 {
		end := start + 5
		if end > len(rows[0]) {
			return ""
		}
		var cell [5]string
		for r := range rows {
			cell[r] = rows[r][start:end]
		}
		ch, ok := lookup(cell)
		if !ok {
			return ""
		}
		sb.WriteRune(ch)
	}
	return sb.String()
}

func lookup(cell [5]string) (rune, bool) {
	for ch, g := range glyphs {
		if g == cell {
			return ch, true
		}
	}
	return 0, false
}

// Suppressed reports whether the banner should be withheld. Environment only, so
// it is testable without a terminal.
func Suppressed(getenv func(string) string, noBannerFlag, stderrIsTTY bool) bool {
	if noBannerFlag || !stderrIsTTY {
		return true
	}
	if getenv("CI") != "" || getenv("NO_COLOR") != "" || getenv("TERM") == "dumb" {
		return true
	}
	return false
}

// Write emits the banner to w unless suppressed. Errors are ignored on purpose:
// chrome must never be able to fail a run.
func Write(w io.Writer, cols int, noBannerFlag, stderrIsTTY bool) {
	if Suppressed(os.Getenv, noBannerFlag, stderrIsTTY) {
		return
	}
	_, _ = io.WriteString(w, For(cols)+"\n\n")
}
