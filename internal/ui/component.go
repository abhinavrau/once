package ui

import (
	tea "charm.land/bubbletea/v2"
)

// Component is the interface for internal UI components. Only App satisfies
// tea.Model; sub-components use this narrower interface with plain string views.
type Component interface {
	Init() tea.Cmd
	Update(tea.Msg) (Component, tea.Cmd)
	View() string
}

// Busy is implemented by a Component that owns a background task the process
// must not abandon mid-flight, such as a deploy or a settings action. App
// guards its quit key while the current screen reports Busy, so ctrl+c can no
// longer kill the process between an old container's removal and the new
// one's start; a screen that doesn't implement Busy is always safe to quit.
type Busy interface {
	Busy() bool
}

// SecretDisplayer is implemented by a Component whose current frame may show
// a secret. While ShowingSecret is true, App turns terminal mouse tracking
// off so native selection works and the secret can be copied by selecting it;
// a screen that doesn't implement it never shows one.
type SecretDisplayer interface {
	ShowingSecret() bool
}

// MouseEvent is dispatched to sub-components after the root App resolves
// mouse coordinates against the mouse tracker's zone map.
type MouseEvent struct {
	X, Y    int
	Button  tea.MouseButton
	Target  string
	IsClick bool
}
