package python

import (
	"crypto/sha256"
	"fmt"
	"path"

	"github.com/omry/reploy/internal/blueprint"
)

const pythonDirectShebangLimitBytes = 127

// A leading underscore keeps digest roots disjoint from application names,
// whose provider identifier grammar starts with a letter.
const pythonLongApplicationDigestPrefix = "_"

// RuntimeRootV1 returns the deterministic, application-scoped Python runtime
// root. Short application identities retain their historical readable path.
// Long application identities use a full SHA-256 digest segment so the
// generated interpreter remains usable in a Linux direct shebang while the
// root remains unique and stable for the owning application identity.
func RuntimeRootV1(component string) (string, error) {
	if err := blueprint.ValidateContributionReference("Python runtime contribution", component); err != nil {
		return "", err
	}
	owner := blueprint.ContributionRuntimeOwner(component, blueprint.ContributionProviderPython)
	root := path.Join(InstallRoot, owner)
	if pythonDirectShebangPathFits(root) {
		return root, nil
	}
	if _, ok := blueprint.ApplicationContributionOwner(component, blueprint.ContributionProviderPython); !ok {
		return "", fmt.Errorf("Python runtime interpreter path %q exceeds the Linux direct shebang length limit", path.Join(root, "bin", "python"))
	}
	digest := sha256.Sum256([]byte(owner))
	digestSegment := pythonLongApplicationDigestPrefix + fmt.Sprintf("%x", digest)
	root = path.Join(InstallRoot, "application", digestSegment)
	if !pythonDirectShebangPathFits(root) {
		return "", fmt.Errorf("Python runtime interpreter path %q exceeds the Linux direct shebang length limit", path.Join(root, "bin", "python"))
	}
	return root, nil
}

func pythonDirectShebangPathFits(root string) bool {
	// distlib counts the #! prefix and terminating newline in the Linux limit.
	return len(path.Join(root, "bin", "python"))+3 <= pythonDirectShebangLimitBytes
}
