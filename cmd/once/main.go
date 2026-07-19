package main

import (
	"net/http"
	"os"

	"github.com/basecamp/once/internal/command"
	"github.com/basecamp/once/internal/logging"
)

func main() {
	logging.SetupStderr()
	http.DefaultTransport = logging.WrapTransport(http.DefaultTransport)

	if err := command.NewRootCommand().Execute(); err != nil {
		os.Exit(1)
	}
}
