package ui

import (
	"charm.land/lipgloss/v2"

	"github.com/basecamp/once/internal/docker"
)

// ErrorCopier is implemented by a Component whose current frame shows a
// message worth pasting somewhere else - an error, or a warning that names
// what to fix. App's copy key puts that text on the clipboard, because the
// alt screen keeps no scrollback and mouse tracking takes selection away:
// otherwise the only way into an issue or a search box is to retype it.
// An empty return means there is nothing on screen to copy.
type ErrorCopier interface {
	CopyableError() string
}

// errorWithCopyHint is how a screen renders an error message: the message in
// the error colour, followed by the key that copies it. Rendering it here
// rather than per screen keeps the hint from advertising a key that the
// screen does not actually answer.
func errorWithCopyHint(err error) string {
	return lipgloss.NewStyle().Foreground(Colors.Error).Render(docker.ErrorMessage(err)) +
		lipgloss.NewStyle().Foreground(Colors.Muted).Render("  ("+copyErrorHint()+")")
}

// copyHintLine is the same hint on a row of its own, for a screen whose
// messages are centered lines with no room for a suffix.
func copyHintLine(width int) string {
	hint := lipgloss.NewStyle().Foreground(Colors.Muted).Render(copyErrorHint())
	return lipgloss.PlaceHorizontal(width, lipgloss.Center, hint)
}

// Helpers

func copyErrorHint() string {
	return appKeys.CopyError.Help().Key + " to copy"
}

// errorText is what a screen showing one error hands the clipboard: the same
// message it renders, and nothing when it has no error.
func errorText(err error) string {
	if err == nil {
		return ""
	}
	return docker.ErrorMessage(err)
}

// copyableError is the message a component offers for copying: empty for a
// nil overlay, and for any component that shows no message.
func copyableError(c Component) string {
	e, ok := c.(ErrorCopier)
	if !ok {
		return ""
	}
	return e.CopyableError()
}
