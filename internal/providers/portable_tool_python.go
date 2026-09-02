package providers

import "github.com/omry/reploy/internal/portabletool"

// PythonPackageRootDistributionNameV1 is retained at the provider boundary
// for compatibility. The shared portable-tool contract owns the grammar.
func PythonPackageRootDistributionNameV1(requirement string) (string, error) {
	return portabletool.PythonPackageRootDistributionNameV1(requirement)
}

// PythonPackageRootRequirementsCompatibleV1 is retained at the provider
// boundary for compatibility. The shared portable-tool contract owns the
// compatibility grammar.
func PythonPackageRootRequirementsCompatibleV1(requirements []string) (bool, error) {
	return portabletool.PythonPackageRootRequirementsCompatibleV1(requirements)
}

func portableToolPythonSupportedIntersectionV1(supported [][]string) bool {
	return portabletool.PythonSupportedIntersectionV1(supported)
}
