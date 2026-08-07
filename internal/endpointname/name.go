// Package endpointname defines the shared logical workload-endpoint name
// contract used by blueprint resolution and runtime authorization.
package endpointname

import (
	"fmt"
	"regexp"
)

const maxLength = 128

// componentPattern follows one Docker Distribution image-name path component:
// lowercase alphanumeric segments separated by '.', '_', '__', or one or more
// '-'. It deliberately excludes full reference syntax such as '/', ':', and
// '@'.
var componentPattern = regexp.MustCompile(`^[a-z0-9]+(?:(?:__|[._]|-+)[a-z0-9]+)*$`)

func Validate(value string) error {
	if len(value) == 0 || len(value) > maxLength || !componentPattern.MatchString(value) {
		return fmt.Errorf("must be a Docker-style lowercase name component no longer than %d bytes, with alphanumeric segments separated by '.', '_', '__', or one or more '-'", maxLength)
	}
	return nil
}
