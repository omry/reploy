package dockerdeploy

import (
	"testing"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
)

func pythonInterpreterFactsV2ForTest(version string) providers.CanonicalProviderData {
	return pythonprovider.CanonicalInterpreterFactsV2(pythonInspectionFactsV2ForTest(version, nil, nil))
}

func pythonInspectionFactsV2ForTest(version string, testedTags, compatibleTags []string) pythonprovider.InterpreterInspectionFactsV2 {
	if testedTags == nil {
		testedTags = []string{}
	}
	if compatibleTags == nil {
		compatibleTags = []string{}
	}
	return pythonprovider.InterpreterInspectionFactsV2{
		Version: version, Implementation: "cpython", ABI: "cp313",
		Libc: "glibc", LibcMajor: "2", LibcMinor: "35",
		TestedTags: append([]string{}, testedTags...), CompatibleTags: append([]string{}, compatibleTags...),
	}
}

func pythonInspectionOutputV2ForTest(version string, testedTags, compatibleTags []string) []byte {
	facts := pythonInspectionFactsV2ForTest(version, testedTags, compatibleTags)
	encoded, err := canonical.Marshal(facts)
	if err != nil {
		panic(err)
	}
	return encoded
}

func pythonInspectionOutputFromFactsV2ForTest(t *testing.T, data canonical.Envelope) []byte {
	t.Helper()
	facts, err := pythonprovider.DecodeInterpreterFactsV2(data)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := canonical.Marshal(facts)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
