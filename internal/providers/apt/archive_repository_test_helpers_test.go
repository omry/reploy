package apt

import "github.com/omry/reploy/internal/blueprint"

func aptArchiveTestPlatform(t interface{ Helper() }) blueprint.Platform {
	t.Helper()
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		panic(err)
	}
	return platform
}

func RequiredBaseToolsV1WithVersionsForTest() []RequiredToolEvidenceV1 {
	tools := RequiredBaseToolsV1()
	for index := range tools {
		if requiredToolVersionV1(tools[index].Name) {
			tools[index].Version = "test"
		}
	}
	return tools
}
