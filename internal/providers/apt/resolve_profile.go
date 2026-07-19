package apt

import "github.com/omry/reploy/internal/providers"

const (
	ResolveProfileV1         = "apt-resolve-v1"
	ResolveConfigPath        = ResolverScratchDirectory + "/apt.conf"
	ResolveListsDirectory    = ResolverScratchDirectory + "/lists"
	ResolveArchivesDirectory = ResolverScratchDirectory + "/archives"
	ResolveOutputDirectory   = ResolverScratchDirectory + "/output"
	ResolveProfileUmask      = "0022"
	ResolveAdditiveConfigV1  = "Acquire::Languages \"none\";\nDpkg::Use-Pty \"0\";\n"
)

// ResolveChildEnvironmentV1 returns the complete closed environment supplied
// to APT resolver children. The ordering is canonical and part of the profile.
func ResolveChildEnvironmentV1() providers.ChildEnvironmentProfile {
	return providers.ChildEnvironmentProfile{
		Schema: providers.ChildEnvironmentSchemaV1,
		Name:   ResolveProfileV1, InheritNone: true, Umask: ResolveProfileUmask,
		Variables: []providers.EnvironmentVariable{
			{Name: "APT_CONFIG", Value: ResolveConfigPath},
			{Name: "DEBIAN_FRONTEND", Value: "noninteractive"},
			{Name: "HOME", Value: "/root"},
			{Name: "LANG", Value: "C"},
			{Name: "LC_ALL", Value: "C"},
			{Name: "PATH", Value: "/usr/sbin:/usr/bin:/sbin:/bin"},
			{Name: "TMPDIR", Value: ResolverScratchDirectory},
		},
	}
}

// ResolveUpdateArgvV1 is the fixed one-refresh command. It contains no
// confirmation or trust-relaxing option.
func ResolveUpdateArgvV1() []string {
	return []string{
		"/usr/bin/apt-get",
		"-o", "Dpkg::Use-Pty=0",
		"-o", "Acquire::Languages=none",
		"-o", "Dir::State::lists=" + ResolveListsDirectory,
		"update", "--error-on=any",
	}
}

// ResolvePlanPrefixArgvV1 is the fixed prefix for the read-only dependency
// planning pass. Typed root package operands are appended by the resolver.
// The marker interface is capability-gated by strict parsing of its complete
// stderr stream; distribution or APT version strings do not select it.
func ResolvePlanPrefixArgvV1() []string {
	return []string{
		"/usr/bin/apt-get",
		"-o", "Dpkg::Use-Pty=0",
		"-o", "Acquire::Languages=none",
		"-o", "Dir::State::lists=" + ResolveListsDirectory,
		"--solver", "internal",
		"--simulate",
		"-o", "Debug::pkgDepCache::Marker=1",
		"--no-remove", "--no-install-recommends",
		"-o", "APT::Install-Suggests=false",
		"install",
	}
}

// ResolveBaseStatePrefixArgvV1 is the fixed prefix for querying the exact dpkg
// tuple of every installed package referenced by the dependency plan. Literal
// binary package names are appended by the resolver.
func ResolveBaseStatePrefixArgvV1() []string {
	return []string{
		"/usr/bin/dpkg-query",
		"--show",
		"--showformat=${binary:Package}\t${Version}\t${Architecture}\t${Status}\n",
	}
}

// ResolveDownloadPrefixArgvV1 is the fixed prefix for one unsplit download
// transaction. Typed root package operands are appended by the resolver.
func ResolveDownloadPrefixArgvV1() []string {
	return []string{
		"/usr/bin/apt-get",
		"-o", "Dpkg::Use-Pty=0",
		"-o", "Acquire::Languages=none",
		"-o", "Dir::State::lists=" + ResolveListsDirectory,
		"-o", "Dir::Cache::archives=" + ResolveArchivesDirectory,
		"--assume-yes", "--download-only", "--no-remove", "--no-install-recommends",
		"-o", "APT::Install-Suggests=false",
		"install",
	}
}
