package apt

import "github.com/omry/reploy/internal/providers"

const (
	MaterializationRecipeVersion = "apt-materialize-v1"
	MaterializationProfileV1     = "apt-dpkg-v1"
	MaterializationScratchV1     = "/tmp/reploy-apt-dpkg"
	MaterializationConfigV1      = MaterializationScratchV1 + "/apt.conf"
	MaterializationProfileUmask  = "0022"
)

// MaterializationChildEnvironmentV1 is the complete environment inherited by
// APT/dpkg children during the network-disabled installation transaction.
func MaterializationChildEnvironmentV1() providers.ChildEnvironmentProfile {
	return providers.ChildEnvironmentProfile{
		Schema: providers.ChildEnvironmentSchemaV1,
		Name:   MaterializationProfileV1, InheritNone: true, Umask: MaterializationProfileUmask,
		Variables: []providers.EnvironmentVariable{
			{Name: "APT_CONFIG", Value: MaterializationConfigV1},
			{Name: "DEBIAN_FRONTEND", Value: "noninteractive"},
			{Name: "HOME", Value: "/root"},
			{Name: "LANG", Value: "C"},
			{Name: "LC_ALL", Value: "C"},
			{Name: "PATH", Value: "/usr/sbin:/usr/bin:/sbin:/bin"},
			{Name: "TMPDIR", Value: MaterializationScratchV1},
		},
	}
}
