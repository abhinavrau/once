// Package catalog holds the built-in list of known apps that both the TUI
// install screen and the CLI deploy command offer by name.
package catalog

import "github.com/basecamp/once/internal/docker"

type App struct {
	Name     string
	Alias    string
	ImageRef string
	Profile  docker.AppProfile
}

var Apps = []App{
	{
		Name:     "Campfire",
		Alias:    "campfire",
		ImageRef: "ghcr.io/basecamp/once-campfire",
		Profile:  docker.AppProfile{BaseName: "campfire"},
	},
	{
		Name:     "Fizzy",
		Alias:    "fizzy",
		ImageRef: "ghcr.io/basecamp/fizzy",
		Profile:  docker.AppProfile{BaseName: "fizzy"},
	},
	{
		Name:     "Vaultwarden",
		Alias:    "vaultwarden",
		ImageRef: "vaultwarden/server",
		Profile: docker.AppProfile{
			BaseName:   "vaultwarden",
			DataPath:   "/data",
			HealthPath: "/alive",
			URLEnvVar:  "DOMAIN",
		},
	},
	{
		Name:     "Writebook",
		Alias:    "writebook",
		ImageRef: "ghcr.io/basecamp/writebook",
		Profile:  docker.AppProfile{BaseName: "writebook"},
	},
}

// Resolve maps a catalog app name (e.g. "campfire") to its catalog entry,
// Profile included. An argument matching no catalog entry is returned as a
// bare image ref with a zero Profile.
func Resolve(s string) App {
	for _, app := range Apps {
		if s == app.Alias {
			return app
		}
	}
	return App{ImageRef: s}
}
