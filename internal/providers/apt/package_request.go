package apt

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
)

const PackageRequestSchemaV1 = "apt-package-request-v1"

func CanonicalPackageRequestV1(request blueprint.APTPackageRequest) (providers.CanonicalPackageRequest, error) {
	if err := blueprint.ValidateAPTPackageRequest(request); err != nil {
		return providers.CanonicalPackageRequest{}, err
	}
	exports := make([]any, 0, len(request.Exports))
	names := make([]string, 0, len(request.Exports))
	for name := range request.Exports {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		exports = append(exports, canonical.Object{"name": name, "executable": request.Exports[name].Executable})
	}
	value := canonical.Object{"name": request.Name, "exports": exports}
	if request.Version != "" {
		value["version"] = request.Version
	}
	return providers.CanonicalPackageRequest{Schema: PackageRequestSchemaV1, Value: value}, nil
}

func ValidateCanonicalPackageRequestV1(request providers.CanonicalPackageRequest) error {
	if request.Schema != PackageRequestSchemaV1 {
		return fmt.Errorf("APT package request schema must be %q", PackageRequestSchemaV1)
	}
	name, ok := request.Value["name"].(string)
	if !ok {
		return fmt.Errorf("APT package request name must be a string")
	}
	version := ""
	if value, exists := request.Value["version"]; exists {
		var valid bool
		version, valid = value.(string)
		if !valid {
			return fmt.Errorf("APT package request version must be a string")
		}
	}
	exportValues, ok := request.Value["exports"].([]any)
	if !ok {
		return fmt.Errorf("APT package request exports must be an array")
	}
	if len(request.Value) != 2 && !(len(request.Value) == 3 && version != "") {
		return fmt.Errorf("APT package request contains unknown or empty optional fields")
	}
	exports := make(map[string]blueprint.ExecutableExport, len(exportValues))
	for index, value := range exportValues {
		object, ok := canonicalObject(value)
		if !ok || len(object) != 2 {
			return fmt.Errorf("APT package request export %d must contain name and executable", index)
		}
		exportName, nameOK := object["name"].(string)
		executable, executableOK := object["executable"].(string)
		if !nameOK || !executableOK {
			return fmt.Errorf("APT package request export %d must contain string name and executable", index)
		}
		if _, exists := exports[exportName]; exists {
			return fmt.Errorf("APT package request contains duplicate export %q", exportName)
		}
		exports[exportName] = blueprint.ExecutableExport{Executable: executable}
	}
	normalized, err := CanonicalPackageRequestV1(blueprint.APTPackageRequest{Name: name, Version: version, Exports: exports})
	if err != nil {
		return err
	}
	actual, err := providers.CanonicalPackageRequestBytes(request)
	if err != nil {
		return err
	}
	expected, err := providers.CanonicalPackageRequestBytes(normalized)
	if err != nil {
		return err
	}
	if !bytes.Equal(actual, expected) {
		return fmt.Errorf("APT package request is not canonically normalized")
	}
	return nil
}

func canonicalObject(value any) (canonical.Object, bool) {
	switch object := value.(type) {
	case canonical.Object:
		return object, true
	case map[string]any:
		return canonical.Object(object), true
	default:
		return nil, false
	}
}
