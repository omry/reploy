package dockerdeploy

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
)

var errIdentityPolicyBlueprintMismatchV1 = errors.New("identity policy selected blueprint does not match its build lock")

func validateDocumentIdentityPolicyV1(document blueprint.Document, lock deploy.BuildLockV1) error {
	policy := document.Environment.Runtime.IdentityPolicy
	if policy == nil && lock.IdentityPolicy == nil {
		return nil
	}
	if policy == nil || lock.IdentityPolicy == nil {
		return fmt.Errorf("identity policy requires matching immutable-image identity evidence")
	}
	digest, err := blueprint.DocumentDigestV1(document)
	if err != nil {
		return err
	}
	if digest != lock.BlueprintDigest {
		return errIdentityPolicyBlueprintMismatchV1
	}
	if err := deploy.ValidateIdentityPolicyLockV1(lock.IdentityPolicy, lock.FinalImage, lock.Platform); err != nil {
		return err
	}
	wanted, err := canonical.Marshal(policy)
	if err != nil {
		return err
	}
	locked, err := canonical.Marshal(lock.IdentityPolicy.Policy)
	if err != nil {
		return err
	}
	if !bytes.Equal(wanted, locked) {
		return fmt.Errorf("identity policy differs from locked image evidence")
	}
	return nil
}

// Authenticate the selected bytes before consulting their policy or inventory.
func validateSelectedIdentityPolicyV1(payload blueprint.ResolvedDocumentV1, lock deploy.BuildLockV1) error {
	document, err := blueprint.DecodeResolvedDocumentV1(payload)
	if err != nil {
		return err
	}
	if document.Environment.Runtime.IdentityPolicy == nil && lock.IdentityPolicy == nil {
		return nil
	}
	digest, err := blueprint.ResolvedDocumentDigestV1(payload)
	if err != nil {
		return err
	}
	if digest != lock.BlueprintDigest {
		return errIdentityPolicyBlueprintMismatchV1
	}
	return validateDocumentIdentityPolicyV1(document, lock)
}

// Include unused profiles and each command's selected qualified executable.
// Profile source/binary and argument bindings cannot change through rebinding.
func identityPolicyInventoryV1(document blueprint.Document) ([]byte, error) {
	profiles := map[string]blueprint.Executable{}
	commands := map[string]string{}
	for application, value := range document.Environment.Applications {
		for name, profile := range value.Executables {
			profiles[application+"."+name] = profile
		}
	}
	for name, command := range document.Environment.Commands {
		commands[name] = command.Executable
	}
	return canonical.Marshal(struct {
		Profiles map[string]blueprint.Executable
		Commands map[string]string
	}{profiles, commands})
}
