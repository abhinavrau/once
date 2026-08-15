package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	goqr "github.com/piglig/go-qr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A LAN handoff URL, the payload that has to fit.
const testQRURL = "https://192.168.100.250/r/K3PZQ7XW4A2BCDEF"

func TestQRCodeIsHalfBlocksOnly(t *testing.T) {
	code, err := QRCode(testQRURL)
	require.NoError(t, err)

	lines := strings.Split(code, "\n")
	require.NotEmpty(t, lines)
	for _, line := range lines {
		assert.Equal(t, strings.Repeat("▀", ansi.StringWidth(line)), ansi.Strip(line),
			"every cell is the upper half block, so two module rows share a text row")
	}
}

func TestQRCodeIsSquareAndKeepsItsQuietZone(t *testing.T) {
	code, err := QRCode(testQRURL)
	require.NoError(t, err)

	lines := strings.Split(code, "\n")
	width := ansi.StringWidth(lines[0])
	for _, line := range lines {
		assert.Equal(t, width, ansi.StringWidth(line))
	}
	// Two module rows per text row: the terminal cell is about 1:2, which is
	// what makes the modules square and the symbol scannable.
	assert.Equal(t, (width+1)/2, len(lines))

	// The quiet zone is 4 modules on every side, so the first two text rows
	// carry no dark module at all.
	for _, line := range lines[:qrQuietZone/2] {
		assert.NotContains(t, line, qrDarkANSI, "the top quiet zone is all light modules")
	}
}

// TestQRCodeRoundTripsToTheEncodersModules reads the painted cells back into
// a module bitmap the way a decoder pointed at the screen would, and compares
// it to the encoder's own. It is the test that catches the two mistakes that
// render a plausible-looking but unscannable code: swapping the half-block's
// top and bottom rows, and inverting dark and light.
func TestQRCodeRoundTripsToTheEncodersModules(t *testing.T) {
	code, err := QRCode(testQRURL)
	require.NoError(t, err)

	painted := parseQRModules(t, code)
	encoded, err := goqr.EncodeText(testQRURL, goqr.Medium)
	require.NoError(t, err)

	size := encoded.Size()
	require.Equal(t, size+2*qrQuietZone, len(painted[0]))

	for y := range painted {
		for x := range painted[y] {
			inside := x >= qrQuietZone && y >= qrQuietZone && x < size+qrQuietZone && y < size+qrQuietZone
			want := inside && encoded.Module(x-qrQuietZone, y-qrQuietZone)
			require.Equal(t, want, painted[y][x], "module at %d,%d", x, y)
		}
	}

	// A finder pattern is 7x7 dark with a light ring: proof the orientation
	// markers survived, and that the quiet zone offset is not off by one.
	assert.True(t, painted[qrQuietZone][qrQuietZone])
	assert.False(t, painted[qrQuietZone+7][qrQuietZone])
}

// parseQRModules turns the rendered half blocks back into a module grid: the
// foreground colour is the module above, the background the module below.
func parseQRModules(t *testing.T, code string) [][]bool {
	t.Helper()

	var modules [][]bool
	for _, line := range strings.Split(code, "\n") {
		var top, bottom []bool
		for _, cell := range strings.Split(line, "\x1b[m") {
			if cell == "" {
				continue
			}
			sgr, _, ok := strings.Cut(strings.TrimPrefix(cell, "\x1b["), "m")
			require.True(t, ok, "cell carries an SGR sequence")
			foreground, background, ok := strings.Cut(sgr, ";"+qrBackgroundANSI)
			require.True(t, ok, "cell sets a background")
			top = append(top, strings.Contains(foreground, qrDarkANSI))
			bottom = append(bottom, background == "0;0;0")
		}
		modules = append(modules, top, bottom)
	}
	return modules
}

func TestQRCodeFitsTheTerminalFloor(t *testing.T) {
	for _, payload := range []string{
		testQRURL,
		"https://once-admin.tail9dc1e2.ts.net/r/K3PZQ7XW4A2BCDEF",
	} {
		code, err := QRCode(payload)
		require.NoError(t, err)

		lines := strings.Split(code, "\n")
		// 24 rows total, minus the URL line and the help line under the code.
		assert.LessOrEqual(t, len(lines), 22, payload)
		assert.LessOrEqual(t, ansi.StringWidth(lines[0]), 80, payload)
	}
}

func TestQRCodeForcesBothColoursOnEveryCell(t *testing.T) {
	code, err := QRCode(testQRURL)
	require.NoError(t, err)

	// Half blocks need a foreground and a background per cell, and both must
	// be forced rather than inherited: an inverted symbol is spec-legal but
	// phone scanners do not retry inverted by default.
	cells := strings.Count(code, "▀")
	assert.Equal(t, cells, strings.Count(code, qrForegroundANSI))
	assert.Equal(t, cells, strings.Count(code, qrBackgroundANSI))
	assert.Contains(t, code, qrDarkANSI)
}

func TestQRUnavailableUnderADegradedColorProfile(t *testing.T) {
	none := func(string) string { return "" }

	assert.Empty(t, qrUnavailableReason(colorprofile.TrueColor, none))
	assert.Empty(t, qrUnavailableReason(colorprofile.ANSI256, none))
	assert.Empty(t, qrUnavailableReason(colorprofile.ANSI, none),
		"16 colours still give a real black and a real white")

	// Without colour the code renders in the theme's own foreground on the
	// theme's own background, which inverts it on a dark terminal. Suppress
	// rather than draw an unscannable code.
	assert.NotEmpty(t, qrUnavailableReason(colorprofile.Ascii, none))
	assert.NotEmpty(t, qrUnavailableReason(colorprofile.NoTTY, none))
}

func TestQRUnavailableInAnAmbiguousWidthLocale(t *testing.T) {
	env := func(vars map[string]string) func(string) string {
		return func(name string) string { return vars[name] }
	}

	// U+2580 is East Asian Width Ambiguous: a CJK locale renders it double
	// width, which stretches the symbol and guarantees a failed scan.
	for _, vars := range []map[string]string{
		{"LC_ALL": "ja_JP.UTF-8"},
		{"LC_CTYPE": "zh_CN.UTF-8"},
		{"LANG": "ko_KR.UTF-8"},
		{"RUNEWIDTH_EASTASIAN": "1"},
	} {
		assert.NotEmpty(t, qrUnavailableReason(colorprofile.TrueColor, env(vars)), vars)
	}

	// The most specific variable wins, the way the C library resolves them.
	assert.Empty(t, qrUnavailableReason(colorprofile.TrueColor, env(map[string]string{
		"LC_ALL": "en_US.UTF-8",
		"LANG":   "ja_JP.UTF-8",
	})))
	assert.Empty(t, qrUnavailableReason(colorprofile.TrueColor, env(map[string]string{
		"LANG": "en_US.UTF-8",
	})))
	assert.Empty(t, qrUnavailableReason(colorprofile.TrueColor, env(map[string]string{
		"RUNEWIDTH_EASTASIAN": "0",
	})))
}
