package ui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	goqr "github.com/piglig/go-qr"
)

// The QR code hands a URL to a phone camera, so it is drawn to the symbol
// spec rather than to the theme.
//
// Two module rows share one text row through the upper half block, because a
// terminal cell is about 1:2 and one-cell-per-module renders a stretched
// symbol that does not decode. Both colours are set on every cell, including
// the quiet zone: an inverted symbol is legal under ISO 18004 but phone
// scanners only retry inverted behind an opt-in, so polarity has to be forced
// rather than inherited from a light or dark palette.
const (
	qrQuietZone = 4
	qrHalfBlock = "▀"
)

// The escape fragments the forced colours render as. Lipgloss folds the pair
// into one SGR sequence per cell, so a cell is identified by the two
// parameter prefixes and a dark module by its RGB triple.
const (
	qrForegroundANSI = "38;2;"
	qrBackgroundANSI = "48;2;"
	qrDarkANSI       = "2;0;0;0"
)

var (
	qrDark  = lipgloss.Color("#000000")
	qrLight = lipgloss.Color("#ffffff")
)

// QRCode renders text as a scannable half-block QR code. Check
// QRUnavailableReason first: this draws a code regardless, and a code the
// terminal will mangle is worse than no code at all.
func QRCode(text string) (string, error) {
	code, err := goqr.EncodeText(text, goqr.Medium)
	if err != nil {
		return "", err
	}

	size := code.Size()
	total := size + 2*qrQuietZone
	dark := func(x, y int) bool {
		mx, my := x-qrQuietZone, y-qrQuietZone
		if mx < 0 || my < 0 || mx >= size || my >= size {
			return false
		}
		return code.Module(mx, my)
	}

	// Four cells exist - the combinations of a dark or light module above a
	// dark or light module - so they are rendered once and reused.
	cell := map[[2]bool]string{}
	for _, top := range []bool{false, true} {
		for _, bottom := range []bool{false, true} {
			cell[[2]bool{top, bottom}] = lipgloss.NewStyle().
				Foreground(qrModuleColor(top)).
				Background(qrModuleColor(bottom)).
				Render(qrHalfBlock)
		}
	}

	var rows []string
	for y := 0; y < total; y += 2 {
		var row strings.Builder
		for x := range total {
			row.WriteString(cell[[2]bool{dark(x, y), y+1 < total && dark(x, y+1)}])
		}
		rows = append(rows, row.String())
	}
	return strings.Join(rows, "\n"), nil
}

// QRUnavailableReason explains why this terminal cannot show a scannable QR
// code, or returns "" when it can. The caller offers screen delivery instead:
// a code that cannot be scanned sends the user to a phone that will never
// read it, which is worse than never offering the option.
func QRUnavailableReason() string {
	return qrUnavailableReason(ColorProfile(), lookupEnv)
}

// Private

func qrModuleColor(dark bool) color.Color {
	if dark {
		return qrDark
	}
	return qrLight
}

func qrUnavailableReason(profile colorprofile.Profile, env func(string) string) string {
	// Below 16 colours the forced black and white are stripped, and the code
	// renders in the terminal's own foreground on its own background - which
	// is inverted on every dark theme.
	if profile < colorprofile.ANSI {
		return "this terminal reports no colour support, and the code needs a real black and white to scan"
	}
	if eastAsianAmbiguousWidth(env) {
		return "this locale renders block characters double width, which stretches the code out of shape"
	}
	return ""
}

// eastAsianAmbiguousWidth reports whether the terminal is likely to draw
// U+2580 double width. The half block is East Asian Width Ambiguous, so a CJK
// locale widens it and the symbol comes out twice as wide as it is tall.
func eastAsianAmbiguousWidth(env func(string) string) bool {
	if forced := env("RUNEWIDTH_EASTASIAN"); forced != "" {
		return forced != "0"
	}

	// Resolved the way the C library does: LC_ALL wins, then LC_CTYPE, then
	// LANG. Only the first one that is set counts.
	for _, name := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		locale := env(name)
		if locale == "" {
			continue
		}
		language, _, _ := strings.Cut(strings.ToLower(locale), "_")
		return language == "ja" || language == "zh" || language == "ko"
	}
	return false
}
