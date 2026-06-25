package reploy

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var versionText string

var Version = strings.TrimSpace(versionText)
