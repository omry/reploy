package blueprint

import (
	"fmt"
	"regexp"
	"strings"
)

var portableNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

var windowsReservedNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

func resolveNames(environment EnvironmentSyntax) (string, string, error) {
	id := strings.TrimSpace(environment.ID)
	if err := validatePortableName("environment.id", id); err != nil {
		return "", "", err
	}
	controlScript := strings.TrimSpace(environment.ControlScript)
	if controlScript == "" {
		controlScript = id
	}
	if err := validatePortableName("environment.control_script", controlScript); err != nil {
		return "", "", err
	}
	return id, controlScript, nil
}

func validatePortableName(field string, name string) error {
	if !portableNamePattern.MatchString(name) || strings.HasSuffix(name, ".") {
		return fmt.Errorf("%s must be a portable basename using letters, numbers, '.', '_', or '-'", field)
	}
	stem, _, _ := strings.Cut(name, ".")
	if windowsReservedNames[strings.ToUpper(stem)] {
		return fmt.Errorf("%s must not use platform-reserved filename %q", field, name)
	}
	return nil
}
