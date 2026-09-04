package toolcatalog

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	pep440 "github.com/aquasecurity/go-pep440-version"
	"github.com/aquasecurity/go-version/pkg/semver"
	"github.com/omry/reploy/internal/toolrequest"
)

// Candidate selection for portable tool requests.
//
// Selection is per requirement: it turns one request into the ordered list of
// release candidates that requirement could be satisfied by, each carrying the
// contribution union its own references produce. Choosing among candidates so
// that separate requirements do not conflict in a shared provider domain is
// joint solving, which belongs to PTD-10.

// CanonicalBindingDemandV1 and CanonicalRequirementGroupV1 remain exported
// from toolcatalog for solver callers while their catalog-independent parser
// representation lives in internal/toolrequest.
type CanonicalBindingDemandV1 = toolrequest.CanonicalBindingDemandV1
type CanonicalRequirementGroupV1 = toolrequest.CanonicalRequirementGroupV1

// ClientCapabilitiesV1 describes the running client a candidate must satisfy.
type ClientCapabilitiesV1 struct {
	ReployVersion      string
	ResolverPrimitives []string
}

// ReleaseCandidateV1 is one release that could satisfy a request on a target,
// with the contributions its own references select.
type ReleaseCandidateV1 struct {
	Scope         string
	Manifest      ReleaseManifestV1
	Contract      ReleaseContractV1
	Target        TargetRecordV1
	Fixture       IntegrationFixtureRecordV1
	Profiles      []ValidationProfileRecordV1
	Bindings      []string
	Selections    map[string][]string
	Contributions []RecordReferenceV1
	Exports       []ToolExportV1
}

// invalidDefinitionV1 marks a rejection as invalid definition data rather than
// an intrinsic conflict with this request. The distinction is what may fall
// back: a candidate this request cannot use is removed so an older release can
// be tried, while a definition that is internally invalid fails the request,
// because falling back would hide it behind an older release that happens to be
// well formed.
type invalidDefinitionV1 struct{ err error }

func (invalid invalidDefinitionV1) Error() string { return invalid.err.Error() }

func (invalid invalidDefinitionV1) Unwrap() error { return invalid.err }

// candidateRejectionV1 records why one candidate was removed, so a request that
// satisfies none can report the incompatible requirements rather than a bare
// failure.
type candidateRejectionV1 struct {
	Manifest string
	Reason   error
}

// SelectReleaseCandidatesV1 normalizes the request, enumerates the releases it
// authorizes, and reduces them against the running client and observed target.
// Candidates are returned newest-first, so an older release that can satisfy the
// request survives when the newest cannot.
func (catalog *CatalogV1) SelectReleaseCandidatesV1(group CanonicalRequirementGroupV1,
	observed TargetIdentityV1, client ClientCapabilitiesV1, activeBindings []string) ([]ReleaseCandidateV1, error) {
	if err := validateCanonicalRequirementGroupV1(group); err != nil {
		return nil, err
	}
	if err := validateCanonicalBindingSetV1("active bindings", activeBindings, true); err != nil {
		return nil, err
	}
	if err := validateTargetIdentityV1(observed); err != nil {
		return nil, fmt.Errorf("observed target: %w", err)
	}
	toolKey, exists := catalog.tools[group.Tool]
	if !exists {
		return nil, fmt.Errorf("portable tool %q is not defined", group.Tool)
	}
	tool, ok := catalog.records[toolKey].Value.(*ToolRecordV1)
	if !ok {
		return nil, fmt.Errorf("portable tool %q does not resolve to a tool record", group.Tool)
	}

	constraints, revision, err := normalizedVersionDemandV1(tool, group.VersionConstraints,
		group.DefinitionRevision)
	if err != nil {
		return nil, err
	}
	ordered, err := catalog.enumerateReleaseCandidatesV1(tool, constraints, revision)
	if err != nil {
		return nil, err
	}
	if len(ordered) == 0 {
		return nil, fmt.Errorf("portable tool %q has no release matching constraints %v revision %q",
			group.Tool, constraints, revision)
	}

	candidates := make([]ReleaseCandidateV1, 0, len(ordered))
	rejected := make([]candidateRejectionV1, 0, len(ordered))
	for _, manifestKey := range ordered {
		candidate, err := catalog.reduceCandidateV1(manifestKey, group, observed, client, activeBindings)
		if err != nil {
			var invalid invalidDefinitionV1
			if errors.As(err, &invalid) {
				return nil, fmt.Errorf("release %q is invalid definition data: %w", manifestKey.ID, err)
			}
			rejected = append(rejected, candidateRejectionV1{Manifest: manifestKey.ID, Reason: err})
			continue
		}
		candidates = append(candidates, candidate)
	}
	if len(candidates) == 0 {
		reasons := make([]string, 0, len(rejected))
		for _, rejection := range rejected {
			reasons = append(reasons, fmt.Sprintf("%s: %v", rejection.Manifest, rejection.Reason))
		}
		return nil, fmt.Errorf("portable tool %q has no candidate satisfying the canonical group: %s",
			group.Tool, strings.Join(reasons, "; "))
	}
	return candidates, nil
}

func validateCanonicalRequirementGroupV1(group CanonicalRequirementGroupV1) error {
	if err := toolrequest.ValidateResolutionScopeContextV1(group.Scope, group.Context); err != nil {
		return err
	}
	if !validRecordIdentifierV1(group.Tool) {
		return fmt.Errorf("tool name %q is invalid", group.Tool)
	}
	for index, constraint := range group.VersionConstraints {
		if !validRecordTokenV1(constraint) || index > 0 && group.VersionConstraints[index-1] >= constraint {
			return fmt.Errorf("tool version constraints must be nonempty, unique, and sorted")
		}
	}
	if group.DefinitionRevision != "" {
		if err := validateCanonicalDecimalV1("tool definition revision", group.DefinitionRevision, true); err != nil {
			return err
		}
	}
	if group.Binding.All {
		if group.Binding.Infer || len(group.Binding.Explicit) != 0 {
			return fmt.Errorf("all-bindings demand cannot carry inference or explicit bindings")
		}
	} else {
		if !group.Binding.Infer && len(group.Binding.Explicit) == 0 {
			return fmt.Errorf("binding demand must retain inference, explicit bindings, or all")
		}
		if err := validateCanonicalBindingSetV1("explicit bindings", group.Binding.Explicit, true); err != nil {
			return err
		}
	}
	for dimension, values := range group.Selections {
		if !validRecordIdentifierV1(dimension) || len(values) == 0 {
			return fmt.Errorf("selection map is not canonical")
		}
		for index, value := range values {
			if !validRecordIdentifierV1(value) || index > 0 && values[index-1] >= value {
				return fmt.Errorf("selection dimension %q must use a nonempty sorted set", dimension)
			}
		}
	}
	return nil
}

func validateCanonicalBindingSetV1(field string, values []string, allowEmpty bool) error {
	if !allowEmpty && len(values) == 0 {
		return fmt.Errorf("%s must not be empty", field)
	}
	for index, value := range values {
		if !validRecordIdentifierV1(value) || index > 0 && values[index-1] >= value {
			return fmt.Errorf("%s must be unique and sorted", field)
		}
	}
	return nil
}

// normalizedVersionDemandV1 gives an opaque group an exact coordinate. Compact
// revision suffixes are resolved later against the loaded exact-coordinate map;
// public parsing cannot distinguish them from scheme-native syntax.
func normalizedVersionDemandV1(tool *ToolRecordV1, constraints []string,
	revision string) ([]string, string, error) {
	if tool.VersionScheme != "opaque" {
		return append([]string{}, constraints...), revision, nil
	}
	if len(constraints) != 0 {
		return append([]string{}, constraints...), revision, nil
	}
	if tool.DefaultVersion == "" {
		return nil, "", fmt.Errorf("opaque tool %q declares no default version", tool.Name)
	}
	return []string{tool.DefaultVersion}, revision, nil
}

func compactStringsV1(values []string) []string {
	if len(values) < 2 {
		return values
	}
	result := values[:1]
	for _, value := range values[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}

// releaseEntryV1 is one release a tool advertises, decoded once so the tool-wide
// coordinate lookup and the per-release constraint test read the same data.
type releaseEntryV1 struct {
	key      recordKeyV1
	manifest *ReleaseManifestV1
	revision uint64
}

// toolReleasesV1 decodes every release a tool record names. Decoding covers the
// whole index rather than only the releases a constraint matches, so malformed
// release data fails every request for that tool instead of only the requests
// whose constraint happens to reach it.
func (catalog *CatalogV1) toolReleasesV1(tool *ToolRecordV1) ([]releaseEntryV1, error) {
	entries := make([]releaseEntryV1, 0, len(tool.Releases))
	for _, reference := range tool.Releases {
		key := recordKeyV1{ID: reference.ID, Digest: reference.Digest}
		record, exists := catalog.records[key]
		if !exists {
			return nil, fmt.Errorf("release %q at digest %s is not in the catalog", reference.ID, reference.Digest)
		}
		manifest, ok := record.Value.(*ReleaseManifestV1)
		if !ok {
			return nil, fmt.Errorf("release %q is not a manifest", reference.ID)
		}
		parsed, err := strconv.ParseUint(manifest.Revision, 10, 63)
		if err != nil {
			return nil, fmt.Errorf("release %q declares a non-numeric revision %q", manifest.ID, manifest.Revision)
		}
		entries = append(entries, releaseEntryV1{key: key, manifest: manifest, revision: parsed})
	}
	return entries, nil
}

// requestedVersionV1 is a request's version constraint classified against the
// tool's collision-free map of exact coordinates and aliases. Classification is
// tool-wide because whether a token is a coordinate or a comparison expression
// is a property of the tool rather than of the release the token is tested
// against: a per-release operator heuristic reads a coordinate carrying
// scheme-native punctuation, such as a PEP 440 epoch or an opaque coordinate
// containing `~`, as a comparison expression, and then fails against every
// release that is not the one being named.
type requestedVersionV1 struct {
	// Constrained reports that the request carried a version constraint at
	// all. It is a separate answer from the token being empty, because an
	// equality operator naming nothing, `==`, has an empty token and is not an
	// unconstrained request.
	Constrained bool
	// Expression is the complete scheme-native constraint. Token may remove an
	// equality operator for exact lookup, but ordered-scheme parsing must retain
	// it for forms such as PEP 440 prefix equality.
	Expression string
	// Token is the constraint with any equality operator removed.
	Token string
	// Exact reports that Token names a version or alias the tool advertises.
	Exact bool
	// Equality reports that the request was written as an equality constraint.
	// Ordered schemes may still evaluate non-exact scheme-native forms such as
	// PEP 440 prefix equality; opaque equality only names an exact coordinate.
	Equality bool
}

// classifyRequestedVersionV1 resolves a request's version token against every
// coordinate and alias the tool advertises before any operator is interpreted.
func classifyRequestedVersionV1(releases []releaseEntryV1, constraint string) requestedVersionV1 {
	if constraint == "" {
		return requestedVersionV1{}
	}
	requested := requestedVersionV1{Constrained: true, Expression: constraint, Token: constraint,
		Equality: strings.HasPrefix(constraint, "==")}
	if requested.Equality {
		requested.Token = strings.TrimSpace(strings.TrimPrefix(constraint, "=="))
	}
	for _, release := range releases {
		if matchesRequestedVersionV1(release.manifest.Version, release.manifest.Aliases, requested.Token) {
			requested.Exact = true
			break
		}
	}
	return requested
}

// normalizeCompactRevisionDemandV1 splits a compact revision only when the
// prefix resolves through the tool-wide exact coordinate and alias map. This
// leaves every scheme-native comparison expression, including compound SemVer
// tilde terms, intact for its scheme parser.
func normalizeCompactRevisionDemandV1(releases []releaseEntryV1, constraints []string,
	revision string) ([]string, string, error) {
	normalized := append([]string{}, constraints...)
	for index, constraint := range normalized {
		separator := strings.LastIndex(constraint, "~")
		if separator <= 0 {
			continue
		}
		compactRevision := constraint[separator+1:]
		if validateCanonicalDecimalV1("compact definition revision", compactRevision, true) != nil {
			continue
		}
		base := constraint[:separator]
		if !classifyRequestedVersionV1(releases, base).Exact {
			continue
		}
		if revision != "" && revision != compactRevision {
			return nil, "", fmt.Errorf("compact definition revision %q conflicts with structured revision %q",
				compactRevision, revision)
		}
		revision = compactRevision
		normalized[index] = base
	}
	sort.Strings(normalized)
	return compactStringsV1(normalized), revision, nil
}

// enumerateReleaseCandidatesV1 lists the releases a canonical constraint group
// authorizes, newest first. Every retained constraint is interpreted under the
// tool's immutable scheme and the intersection must be nonempty before a
// definition-revision pin is applied.
func (catalog *CatalogV1) enumerateReleaseCandidatesV1(tool *ToolRecordV1, constraints []string,
	revision string) ([]recordKeyV1, error) {
	releases, err := catalog.toolReleasesV1(tool)
	if err != nil {
		return nil, err
	}
	if tool.VersionScheme != "opaque" {
		constraints, revision, err = normalizeCompactRevisionDemandV1(releases, constraints, revision)
		if err != nil {
			return nil, err
		}
	}
	requested := make([]requestedVersionV1, 0, len(constraints))
	hasExactCoordinate := false
	for _, constraint := range constraints {
		classified := classifyRequestedVersionV1(releases, constraint)
		if err := validateRequestedVersionConstraintV1(tool.VersionScheme, classified); err != nil {
			return nil, err
		}
		requested = append(requested, classified)
		hasExactCoordinate = hasExactCoordinate || classified.Exact
	}
	// A revision corrects one exact upstream version. Accepting a pin without
	// one would apply that revision to whichever version happened to sort
	// first, which is not the release the pin names.
	if revision != "" && !hasExactCoordinate {
		return nil, fmt.Errorf("definition revision %q requires an exact upstream version in the constraint group",
			revision)
	}
	intersection := make([]releaseEntryV1, 0, len(releases))
	for _, release := range releases {
		satisfiesAll := true
		if len(requested) == 0 {
			satisfiesAll, err = releaseSatisfiesConstraintV1(tool.VersionScheme, release.manifest.Version,
				release.manifest.Aliases, requestedVersionV1{})
			if err != nil {
				return nil, err
			}
		}
		for _, constraint := range requested {
			satisfies, constraintErr := releaseSatisfiesConstraintV1(tool.VersionScheme,
				release.manifest.Version, release.manifest.Aliases, constraint)
			if constraintErr != nil {
				return nil, constraintErr
			}
			if !satisfies {
				satisfiesAll = false
				break
			}
		}
		if !satisfiesAll {
			continue
		}
		intersection = append(intersection, release)
	}
	if len(intersection) == 0 {
		return nil, fmt.Errorf("version constraint group %v has an empty intersection under the %s scheme",
			constraints, tool.VersionScheme)
	}

	matched := make([]releaseEntryV1, 0, len(intersection))
	for _, release := range intersection {
		if revision != "" && release.manifest.Revision != revision {
			continue
		}
		matched = append(matched, release)
	}
	// Ordered schemes try descending scheme-native version then descending
	// definition revision. An opaque request has one exact version, so the
	// version comparison is inert and revisions decide. Every eligible revision
	// is retained rather than reduced to the newest, so joint solving can fall
	// back to an older one when the newest conflicts.
	sort.SliceStable(matched, func(left int, right int) bool {
		if matched[left].manifest.Version != matched[right].manifest.Version {
			precedence := compareToolVersionsV1(tool.VersionScheme,
				matched[left].manifest.Version, matched[right].manifest.Version)
			if precedence != 0 {
				return precedence > 0
			}
		}
		return matched[left].revision > matched[right].revision
	})
	keys := make([]recordKeyV1, 0, len(matched))
	for _, item := range matched {
		keys = append(keys, item.key)
	}
	return keys, nil
}

// validateRequestedVersionConstraintV1 parses one retained comparison before
// enumeration starts. This prevents a preceding disjoint constraint from
// short-circuiting evaluation and hiding malformed later input.
func validateRequestedVersionConstraintV1(scheme string, requested requestedVersionV1) error {
	if !requested.Constrained || requested.Exact || scheme == "opaque" && requested.Equality {
		return nil
	}
	switch scheme {
	case "semver":
		if _, err := semver.NewConstraints(requested.Expression); err != nil {
			if requested.Equality {
				return nil
			}
			return fmt.Errorf("version constraint %q is invalid under %s", requested.Expression, scheme)
		}
	case "integer":
		if _, err := parseIntegerConstraintV1(requested.Expression); err != nil {
			if requested.Equality {
				return nil
			}
			return err
		}
	case "pep440":
		if _, err := pep440.NewSpecifiers(requested.Expression); err != nil {
			if requested.Equality {
				return nil
			}
			return fmt.Errorf("version constraint %q is invalid under PEP 440", requested.Expression)
		}
	case "opaque":
		if strings.HasPrefix(requested.Token, ">") || strings.HasPrefix(requested.Token, "<") ||
			strings.HasPrefix(requested.Token, "!=") || strings.HasPrefix(requested.Token, "~=") {
			return fmt.Errorf("version scheme %q has no ordering, so constraint %q cannot be evaluated",
				scheme, requested.Token)
		}
	}
	return nil
}

// matchesRequestedVersionV1 reports whether a release answers to an exact
// coordinate, by version or by one of its advertised aliases.
func matchesRequestedVersionV1(upstream string, aliases []string, requested string) bool {
	return upstream == requested || containsRecordValueV1(aliases, requested)
}

// isPrereleaseVersionV1 reports whether a coordinate is a prerelease under its
// scheme. The integer scheme has no prerelease form, and an opaque coordinate
// has no ordering for a release to be ahead of.
func isPrereleaseVersionV1(scheme string, upstream string) (bool, error) {
	switch scheme {
	case "semver":
		parsed, err := semver.Parse(upstream)
		if err != nil {
			return false, fmt.Errorf("release version %q is not valid SemVer", upstream)
		}
		return len(parsed.PreRelease()) > 0, nil
	case "pep440":
		parsed, err := pep440.Parse(upstream)
		if err != nil {
			return false, fmt.Errorf("release version %q is not valid PEP 440", upstream)
		}
		return parsed.IsPreRelease(), nil
	}
	return false, nil
}

type integerConstraintTermV1 struct {
	operator string
	bound    uint64
}

// parseIntegerConstraintV1 parses every term before any coordinate is tested,
// so an unsatisfied earlier term cannot hide malformed later input.
func parseIntegerConstraintV1(constraint string) ([]integerConstraintTermV1, error) {
	terms := make([]integerConstraintTermV1, 0, strings.Count(constraint, ",")+1)
	for _, term := range strings.Split(constraint, ",") {
		term = strings.TrimSpace(term)
		operator := ""
		for _, candidate := range []string{">=", "<=", "!=", "==", ">", "<"} {
			if strings.HasPrefix(term, candidate) {
				operator = candidate
				break
			}
		}
		bound, err := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(term, operator)), 10, 63)
		if err != nil {
			return nil, fmt.Errorf("version constraint %q is invalid under the integer scheme", constraint)
		}
		terms = append(terms, integerConstraintTermV1{operator: operator, bound: bound})
	}
	return terms, nil
}

// integerConstraintSatisfiedV1 evaluates a comma-separated comparison
// expression against an integer coordinate. The integer scheme is ordered but
// is not SemVer: a bare decimal such as `21` is not a semantic version, so a
// SemVer constraint evaluator rejects every release the expression should have
// matched rather than comparing it.
func integerConstraintSatisfiedV1(upstream string, constraint string) (bool, error) {
	terms, err := parseIntegerConstraintV1(constraint)
	if err != nil {
		return false, err
	}
	value, err := strconv.ParseUint(upstream, 10, 63)
	if err != nil {
		return false, nil
	}
	for _, term := range terms {
		operator := term.operator
		bound := term.bound
		satisfied := value == bound
		switch operator {
		case ">=":
			satisfied = value >= bound
		case "<=":
			satisfied = value <= bound
		case ">":
			satisfied = value > bound
		case "<":
			satisfied = value < bound
		case "!=":
			satisfied = value != bound
		}
		if !satisfied {
			return false, nil
		}
	}
	return true, nil
}

// releaseSatisfiesConstraintV1 tests one release against an already classified
// request. An unconstrained request selects a stable version, so a prerelease
// is eligible only where the request names it. A token the tool advertises is
// an exact coordinate. Anything else is a comparison expression, which an
// ordered scheme evaluates scheme-natively and an opaque scheme cannot answer.
func releaseSatisfiesConstraintV1(scheme string, upstream string, aliases []string,
	requested requestedVersionV1) (bool, error) {
	if !requested.Constrained {
		prerelease, err := isPrereleaseVersionV1(scheme, upstream)
		if err != nil {
			return false, err
		}
		return !prerelease, nil
	}
	if requested.Exact {
		return matchesRequestedVersionV1(upstream, aliases, requested.Token), nil
	}
	if requested.Equality && scheme == "opaque" {
		// The tool advertises no such coordinate, so equality matches nothing
		// rather than falling through to an ordering comparison. A bare `==`
		// names nothing and so matches nothing, rather than widening to every
		// release the way an absent constraint does.
		return false, nil
	}
	switch scheme {
	case "semver":
		constraints, err := semver.NewConstraints(requested.Expression)
		if err != nil {
			if requested.Equality {
				return false, nil
			}
			return false, fmt.Errorf("version constraint %q is invalid under %s", requested.Expression, scheme)
		}
		version, err := semver.Parse(upstream)
		if err != nil {
			return false, nil
		}
		return constraints.Check(version), nil
	case "integer":
		satisfied, err := integerConstraintSatisfiedV1(upstream, requested.Expression)
		if err != nil && requested.Equality {
			return false, nil
		}
		return satisfied, err
	case "pep440":
		specifiers, err := pep440.NewSpecifiers(requested.Expression)
		if err != nil {
			if requested.Equality {
				return false, nil
			}
			return false, fmt.Errorf("version constraint %q is invalid under PEP 440", requested.Expression)
		}
		version, err := pep440.Parse(upstream)
		if err != nil {
			return false, nil
		}
		return specifiers.Check(version), nil
	case "opaque":
		if strings.HasPrefix(requested.Token, ">") || strings.HasPrefix(requested.Token, "<") ||
			strings.HasPrefix(requested.Token, "!=") || strings.HasPrefix(requested.Token, "~=") {
			return false, fmt.Errorf("version scheme %q has no ordering, so constraint %q cannot be evaluated",
				scheme, requested.Token)
		}
		return matchesRequestedVersionV1(upstream, aliases, requested.Token), nil
	}
	return false, fmt.Errorf("version scheme %q has no ordering, so constraint %q cannot be evaluated",
		scheme, requested.Token)
}

// reduceCandidateV1 removes a candidate the canonical group cannot use, before
// any joint solving sees it. Every rejection here is intrinsic to this group.
func (catalog *CatalogV1) reduceCandidateV1(manifestKey recordKeyV1, group CanonicalRequirementGroupV1,
	observed TargetIdentityV1, client ClientCapabilitiesV1, activeBindings []string) (ReleaseCandidateV1, error) {
	view, err := catalog.resolvedViewV1(manifestKey)
	if err != nil {
		return ReleaseCandidateV1{}, err
	}
	manifest := catalog.records[manifestKey].Value.(*ReleaseManifestV1)
	contractRecord, err := resolvedRecordV1(view, manifest.Contract)
	if err != nil {
		return ReleaseCandidateV1{}, err
	}
	contract := contractRecord.Value.(*ReleaseContractV1)

	if err := verifyClientSatisfiesContractV1(contract, client); err != nil {
		return ReleaseCandidateV1{}, err
	}
	if !containsRecordValueV1(contract.Contexts, group.Context) {
		return ReleaseCandidateV1{}, fmt.Errorf("context %q is not supported", group.Context)
	}
	target, err := selectExactTargetV1(view, manifest, observed)
	if err != nil {
		return ReleaseCandidateV1{}, err
	}
	tuple, err := resolveCandidateSupportTupleV1(contract, target, group, activeBindings)
	if err != nil {
		return ReleaseCandidateV1{}, err
	}
	if err := validateTupleContributionsV1(view, contract, target, tuple); err != nil {
		return ReleaseCandidateV1{}, err
	}
	contributions, exports, err := candidateContributionsV1(view, contract, target, tuple)
	if err != nil {
		return ReleaseCandidateV1{}, err
	}
	fixtureRecord, profileRecords, err := candidateValidationRecordsV1(view, target, tuple)
	if err != nil {
		return ReleaseCandidateV1{}, err
	}
	profiles := make([]ValidationProfileRecordV1, 0, len(profileRecords))
	for _, profile := range profileRecords {
		profiles = append(profiles, cloneValidationProfileV1(profile))
	}
	// Every record placed in a candidate is cloned, so a returned candidate
	// cannot alias loaded catalog state.
	return ReleaseCandidateV1{
		Scope:         group.Scope,
		Manifest:      cloneReleaseManifestV1(manifest),
		Contract:      cloneReleaseContractV1(contract),
		Target:        cloneTargetRecordV1(target),
		Fixture:       cloneIntegrationFixtureV1(fixtureRecord),
		Profiles:      profiles,
		Bindings:      append([]string{}, tuple.Bindings...),
		Selections:    cloneSelectionMapV1(tuple.Selections),
		Contributions: contributions,
		Exports:       exports,
	}, nil
}

// compareToolVersionsV1 orders two coordinates under a tool's version scheme.
// Ordered schemes compare scheme-natively; an opaque scheme has no ordering, so
// its coordinates compare equal and revision decides.
func compareToolVersionsV1(scheme string, left string, right string) int {
	switch scheme {
	case "semver":
		leftValue, leftErr := semver.Parse(left)
		rightValue, rightErr := semver.Parse(right)
		if leftErr != nil || rightErr != nil {
			return strings.Compare(left, right)
		}
		return leftValue.Compare(rightValue)
	case "pep440":
		leftValue, leftErr := pep440.Parse(left)
		rightValue, rightErr := pep440.Parse(right)
		if leftErr != nil || rightErr != nil {
			return strings.Compare(left, right)
		}
		return leftValue.Compare(rightValue)
	case "integer":
		leftValue, leftErr := strconv.ParseUint(left, 10, 63)
		rightValue, rightErr := strconv.ParseUint(right, 10, 63)
		if leftErr != nil || rightErr != nil {
			return strings.Compare(left, right)
		}
		switch {
		case leftValue < rightValue:
			return -1
		case leftValue > rightValue:
			return 1
		}
		return 0
	}
	return 0
}

// verifyClientSatisfiesContractV1 removes a candidate the running client cannot
// use, by supported version or by resolver primitive. This is what lets an
// older release survive when the newest one demands more than the client has.
func verifyClientSatisfiesContractV1(contract *ReleaseContractV1, client ClientCapabilitiesV1) error {
	if contract.SupportedReploy != "" && client.ReployVersion == "" {
		return fmt.Errorf("client declares no Reploy version, so contract requirement %q cannot be proven",
			contract.SupportedReploy)
	}
	if contract.SupportedReploy != "" {
		constraints, err := semver.NewConstraints(contract.SupportedReploy)
		if err != nil {
			return fmt.Errorf("contract supported Reploy constraint %q is invalid", contract.SupportedReploy)
		}
		version, err := semver.Parse(client.ReployVersion)
		if err != nil {
			return fmt.Errorf("client Reploy version %q is invalid", client.ReployVersion)
		}
		if !constraints.Check(version) {
			return fmt.Errorf("client Reploy %s does not satisfy %q", client.ReployVersion, contract.SupportedReploy)
		}
	}
	for _, primitive := range contract.ResolverPrimitives {
		if !containsRecordValueV1(client.ResolverPrimitives, primitive) {
			return fmt.Errorf("client does not provide resolver primitive %q", primitive)
		}
	}
	return nil
}

// selectExactTargetV1 requires exactly one target leaf matching the observed
// base. Several matching leaves is invalid definition data rather than a
// fallback choice, so it fails rather than picking one.
//
// A loaded catalog cannot reach that failure: leaves match the observed base by
// exact identity, so two matching leaves means two leaves declaring the same
// identity, which the release graph walker rejects for the whole catalog before
// any request is selected. The check stays as a defence, and reports invalid
// definition data so that reaching it fails the request rather than removing
// the candidate and answering from an older release.
func selectExactTargetV1(view map[string]loadedRecordV1, manifest *ReleaseManifestV1,
	observed TargetIdentityV1) (*TargetRecordV1, error) {
	var selected *TargetRecordV1
	for _, reference := range manifest.Targets {
		record, err := resolvedRecordV1(view, reference)
		if err != nil {
			return nil, err
		}
		target, ok := record.Value.(*TargetRecordV1)
		if !ok {
			return nil, fmt.Errorf("release target %q is not a target record", reference.ID)
		}
		if target.Target != observed {
			continue
		}
		if selected != nil {
			return nil, invalidDefinitionV1{err: fmt.Errorf("release targets %q and %q both match the observed base",
				selected.ID, target.ID)}
		}
		selected = target
	}
	if selected == nil {
		return nil, fmt.Errorf("no target leaf matches the observed base")
	}
	return selected, nil
}

// resolveRequestedBindingsV1 applies the cumulative canonical demand after
// target selection. Inference uses active application providers in the same
// scope or, when none match, the sole advertised binding.
func resolveRequestedBindingsV1(schema BindingSetSchemaV1, target *TargetRecordV1,
	demand CanonicalBindingDemandV1, active []string) ([]string, error) {
	advertised := make([]string, 0, len(target.Bindings))
	for _, binding := range target.Bindings {
		advertised = append(advertised, binding.Name)
	}
	sort.Strings(advertised)

	selected := make([]string, 0, len(advertised))
	if demand.All {
		if len(advertised) == 0 {
			return nil, fmt.Errorf("all bindings were requested but the target advertises none")
		}
		selected = append([]string{}, advertised...)
	} else {
		selected = append(selected, demand.Explicit...)
	}
	if demand.Infer {
		inferred := make([]string, 0, len(active))
		for _, binding := range active {
			if containsRecordValueV1(advertised, binding) {
				inferred = append(inferred, binding)
			}
		}
		if len(inferred) == 0 {
			switch len(advertised) {
			case 0:
			case 1:
				inferred = append(inferred, advertised[0])
			default:
				return nil, fmt.Errorf("binding omission is ambiguous; target supports %s",
					strings.Join(advertised, ", "))
			}
		}
		selected = append(selected, inferred...)
	}
	sort.Strings(selected)
	selected = compactStringsV1(selected)
	for _, binding := range selected {
		if !containsRecordValueV1(schema.Options, binding) {
			return nil, fmt.Errorf("binding %q is not declared by the release contract", binding)
		}
		if !targetAdvertisesBindingV1(target, binding) {
			return nil, fmt.Errorf("binding %q is not advertised by the target", binding)
		}
	}
	return selected, nil
}

func resolveRequestedSelectionsV1(schema SelectionSchemaV1, target *TargetRecordV1,
	requested map[string][]string) (map[string][]string, error) {
	selected := make(map[string][]string, len(requested))
	for dimension, values := range requested {
		selected[dimension] = append([]string{}, values...)
	}
	matches, err := selectionMapMatchesCombinationV1(schema, selected)
	if err != nil {
		return nil, err
	}
	if !matches {
		return nil, fmt.Errorf("selection map does not equal one release-contract combination")
	}
	if !targetSupportsSelectionCombinationV1(target, schema, SelectionCombinationV1(selected)) {
		return nil, fmt.Errorf("selection map is not advertised by the target")
	}
	return selected, nil
}

func resolveCandidateSupportTupleV1(contract *ReleaseContractV1, target *TargetRecordV1,
	group CanonicalRequirementGroupV1, activeBindings []string) (supportTupleV1, error) {
	bindings, err := resolveRequestedBindingsV1(contract.Binding, target, group.Binding, activeBindings)
	if err != nil {
		return supportTupleV1{}, err
	}
	selections, err := resolveRequestedSelectionsV1(contract.Selections, target, group.Selections)
	if err != nil {
		return supportTupleV1{}, err
	}
	tuple := supportTupleV1{Context: group.Context, Bindings: bindings, Selections: selections}
	wanted, err := supportTupleKeyV1(tuple)
	if err != nil {
		return supportTupleV1{}, err
	}
	matched := false
	for _, supportCase := range target.SupportCases {
		key, err := supportTupleKeyV1(supportCase)
		if err != nil {
			return supportTupleV1{}, err
		}
		if key != wanted {
			continue
		}
		if matched {
			return supportTupleV1{}, invalidDefinitionV1{err: fmt.Errorf("target repeats one support case")}
		}
		matched = true
	}
	if !matched {
		return supportTupleV1{}, fmt.Errorf("context, binding set, and selection map do not equal one target support case")
	}
	return tuple, nil
}

// candidateContributionsV1 builds the union this candidate's exact support
// case selects. Validation profiles remain separate from selected identity.
func candidateContributionsV1(view map[string]loadedRecordV1, contract *ReleaseContractV1,
	target *TargetRecordV1, tuple supportTupleV1) ([]RecordReferenceV1, []ToolExportV1, error) {
	packages, payloads, artifacts, _, exports, err := selectedContributionReferencesV1(target, tuple)
	if err != nil {
		return nil, nil, err
	}
	references := append([]RecordReferenceV1{}, packages...)
	references = append(references, payloads...)
	references = append(references, artifacts...)
	exports = append(append([]ToolExportV1{}, contract.Exports...), exports...)
	for _, name := range tuple.Bindings {
		binding, found := targetBindingEntryV1(target, name)
		if !found {
			return nil, nil, fmt.Errorf("target does not provide binding %q", name)
		}
		references = append(references, binding.Contract)
		record, err := resolvedRecordV1(view, binding.Contract)
		if err != nil {
			return nil, nil, err
		}
		bindingContract, ok := record.Value.(*BindingContractV1)
		if !ok || bindingContract.Name != name {
			return nil, nil, fmt.Errorf("binding %q contract resolves to an incompatible record", name)
		}
		exports = append(exports, bindingContract.CLI)
	}
	for _, reference := range references {
		if _, err := resolvedRecordV1(view, reference); err != nil {
			return nil, nil, err
		}
	}
	canonicalExports, err := canonicalCandidateExportsV1(exports)
	if err != nil {
		return nil, nil, err
	}
	return canonicalReferenceUnionV1(references), canonicalExports, nil
}

func canonicalCandidateExportsV1(exports []ToolExportV1) ([]ToolExportV1, error) {
	paths := make(map[string]string, len(exports))
	for _, exported := range exports {
		if previous, exists := paths[exported.Name]; exists && previous != exported.Path {
			return nil, fmt.Errorf("selected contributions conflict on export %q: %q and %q",
				exported.Name, previous, exported.Path)
		}
		paths[exported.Name] = exported.Path
	}
	names := make([]string, 0, len(paths))
	for name := range paths {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]ToolExportV1, 0, len(names))
	for _, name := range names {
		result = append(result, ToolExportV1{Name: name, Path: paths[name]})
	}
	return result, nil
}

// candidateValidationRecordsV1 finds the fixture and profiles proving this
// exact tuple. Validation data authorizes support but is outside selected
// identity.
func candidateValidationRecordsV1(view map[string]loadedRecordV1, target *TargetRecordV1,
	tuple supportTupleV1) (*IntegrationFixtureRecordV1, []*ValidationProfileRecordV1, error) {
	wanted, err := supportTupleKeyV1(tuple)
	if err != nil {
		return nil, nil, err
	}
	var matched *IntegrationFixtureRecordV1
	for _, reference := range target.IntegrationFixtures {
		record, err := resolvedRecordV1(view, reference)
		if err != nil {
			return nil, nil, err
		}
		fixture, ok := record.Value.(*IntegrationFixtureRecordV1)
		if !ok {
			return nil, nil, fmt.Errorf("integration fixture %q is not a fixture record", reference.ID)
		}
		key, err := supportTupleKeyV1(normalizedFixtureTupleV1(fixture))
		if err != nil {
			return nil, nil, err
		}
		if key == wanted {
			if matched != nil {
				return nil, nil, invalidDefinitionV1{err: fmt.Errorf("multiple fixtures cover one support tuple")}
			}
			matched = fixture
		}
	}
	if matched == nil {
		return nil, nil, fmt.Errorf("no integration fixture covers the requested support tuple")
	}
	profileReferences, err := selectedProfileReferencesV1(target, tuple)
	if err != nil {
		return nil, nil, err
	}
	if !equalReferenceListsV1(profileReferences, matched.ValidationProfiles) {
		return nil, nil, invalidDefinitionV1{err: fmt.Errorf("fixture profiles do not match selected contributions")}
	}
	profiles := make([]*ValidationProfileRecordV1, 0, len(profileReferences))
	for _, reference := range profileReferences {
		record, err := resolvedRecordV1(view, reference)
		if err != nil {
			return nil, nil, err
		}
		profile, ok := record.Value.(*ValidationProfileRecordV1)
		if !ok {
			return nil, nil, fmt.Errorf("validation profile %q is not a profile record", reference.ID)
		}
		profiles = append(profiles, profile)
	}
	return matched, profiles, nil
}

// targetBindingEntryV1 finds the target's contribution mapping for a binding.
func targetBindingEntryV1(target *TargetRecordV1, name string) (TargetBindingV1, bool) {
	for _, binding := range target.Bindings {
		if binding.Name == name {
			return binding, true
		}
	}
	return TargetBindingV1{}, false
}

// canonicalReferenceUnionV1 deduplicates references and returns them in
// canonical order, so a candidate's contribution union does not depend on the
// order its parts were gathered.
func canonicalReferenceUnionV1(references []RecordReferenceV1) []RecordReferenceV1 {
	seen := make(map[recordKeyV1]struct{}, len(references))
	union := make([]RecordReferenceV1, 0, len(references))
	for _, reference := range references {
		key := recordKeyV1{ID: reference.ID, Digest: reference.Digest}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		union = append(union, reference)
	}
	sort.SliceStable(union, func(left int, right int) bool {
		if union[left].ID != union[right].ID {
			return union[left].ID < union[right].ID
		}
		return union[left].Digest < union[right].Digest
	})
	return union
}
