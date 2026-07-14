package dockerdeploy

import (
	"context"
	"fmt"
	"testing"
)

func TestResolveDockerBaseIdentityPrefersRepositoryDigest(t *testing.T) {
	run := func(_ context.Context, args ...string) (string, error) {
		switch args[0] {
		case "pull":
			return "pulled", nil
		case "image":
			return `["python@sha256:abc"]` + "\tsha256:id\n", nil
		default:
			return "", fmt.Errorf("unexpected args: %v", args)
		}
	}
	identity, err := resolveDockerBaseIdentity(context.Background(), "python:3.13", run)
	if err != nil {
		t.Fatal(err)
	}
	if identity != "python@sha256:abc" {
		t.Fatalf("identity = %q", identity)
	}
}
