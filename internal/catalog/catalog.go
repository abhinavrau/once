// Package catalog holds the built-in list of known apps that both the TUI
// install screen and the CLI deploy command offer by name.
package catalog

type App struct {
	Name     string
	Alias    string
	ImageRef string
}

var Apps = []App{
	{Name: "Campfire", Alias: "campfire", ImageRef: "ghcr.io/basecamp/once-campfire"},
	{Name: "Fizzy", Alias: "fizzy", ImageRef: "ghcr.io/basecamp/fizzy"},
	{Name: "Writebook", Alias: "writebook", ImageRef: "ghcr.io/basecamp/writebook"},
}

// Resolve maps a catalog app name (e.g. "campfire") to its image ref.
// An argument matching no catalog entry is returned unchanged, so callers
// can treat it as an image ref.
func Resolve(s string) string {
	for _, app := range Apps {
		if s == app.Alias {
			return app.ImageRef
		}
	}
	return s
}
