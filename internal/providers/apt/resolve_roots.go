package apt

import (
	"fmt"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/providers"
)

// ResolveRootOperandsV1 returns the sole APT expression permitted for each
// requested binary package: name or name=exact-version. Component ownership
// and executable exports do not alter the shared resolver transaction.
func ResolveRootOperandsV1(request providers.CanonicalProviderRequest) ([]string, error) {
	decoded, err := decodeCanonicalProviderRequestV1(request)
	if err != nil {
		return nil, err
	}
	byName := map[string]string{}
	for _, component := range decoded.Components {
		for _, pkg := range component.Packages {
			operand, err := pkg.Canonical()
			if err != nil {
				return nil, err
			}
			if strings.HasPrefix(operand, "-") {
				return nil, fmt.Errorf("APT root operand %q must not be option-like", operand)
			}
			if previous, exists := byName[pkg.Name]; exists && previous != operand {
				return nil, fmt.Errorf("APT root package %q has conflicting operands", pkg.Name)
			}
			byName[pkg.Name] = operand
		}
	}
	operands := make([]string, 0, len(byName))
	for _, operand := range byName {
		operands = append(operands, operand)
	}
	sort.Strings(operands)
	if len(operands) == 0 {
		return nil, fmt.Errorf("APT resolver requires at least one root package")
	}
	return operands, nil
}
