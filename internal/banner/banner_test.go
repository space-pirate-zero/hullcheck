package banner

import (
	"strings"
	"testing"
)

// The banner is a spec, not decoration. "Renders on any screen" is enforced here
// so it cannot quietly regress into Unicode, width creep or trailing whitespace.

const maxCols = 37

func TestBannerIsPureASCII(t *testing.T) {
	for _, r := range Full {
		if r > 127 {
			t.Fatalf("non-ASCII rune %q (U+%04X) in banner", r, r)
		}
	}
}

func TestBannerCharsetIsAllowlisted(t *testing.T) {
	const allowed = " #*-.=[]o\n"
	for _, r := range Full {
		if !strings.ContainsRune(allowed, r) {
			t.Fatalf("rune %q not in allowlist %q", r, allowed)
		}
	}
}

func TestBannerFitsNarrowTerminals(t *testing.T) {
	for i, line := range strings.Split(Full, "\n") {
		if n := len([]rune(line)); n > maxCols {
			t.Fatalf("line %d is %d cols, limit %d: %q", i+1, n, maxCols, line)
		}
	}
}

func TestBannerHasNoTrailingWhitespace(t *testing.T) {
	for i, line := range strings.Split(Full, "\n") {
		if line != strings.TrimRight(line, " \t") {
			t.Fatalf("line %d has trailing whitespace: %q", i+1, line)
		}
	}
}

func TestBannerSpellsTheStudioName(t *testing.T) {
	// The art must actually read SPACE / PIRATE / ZERO, stacked. Decoding the
	// slab glyphs back to letters proves the art, not just its shape.
	got := Decode(Full)
	want := []string{"SPACE", "PIRATE", "ZERO"}
	if len(got) != len(want) {
		t.Fatalf("decoded %d words %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("word %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestFallbacksFitTheirWidths(t *testing.T) {
	for _, tc := range []struct{ cols, limit int }{{80, maxCols}, {39, 29}, {27, 20}} {
		s := For(tc.cols)
		for _, line := range strings.Split(s, "\n") {
			if n := len([]rune(line)); n > tc.limit {
				t.Fatalf("at %d cols, line %q is %d wide, limit %d", tc.cols, line, n, tc.limit)
			}
		}
		if strings.TrimSpace(s) == "" {
			t.Fatalf("at %d cols the banner was empty", tc.cols)
		}
	}
}
