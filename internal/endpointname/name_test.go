package endpointname

import (
	"strings"
	"testing"
)

func TestValidateAcceptsDockerStyleNameComponents(t *testing.T) {
	for _, value := range []string{
		"api",
		"api_v1",
		"api.v1",
		"api-v1",
		"api--v1",
		"api__internal",
		"2fa",
		strings.Repeat("a", maxLength),
	} {
		t.Run(value, func(t *testing.T) {
			if err := Validate(value); err != nil {
				t.Fatalf("Validate(%q) error = %v", value, err)
			}
		})
	}
}

func TestValidateRejectsNonComponents(t *testing.T) {
	for _, value := range []string{
		"",
		"API",
		"-api",
		"api-",
		"api/v1",
		"api:v1",
		"api@sha256",
		"api___v1",
		"api..v1",
		"café",
		strings.Repeat("a", maxLength+1),
	} {
		t.Run(value, func(t *testing.T) {
			if err := Validate(value); err == nil {
				t.Fatalf("Validate(%q) unexpectedly succeeded", value)
			}
		})
	}
}
