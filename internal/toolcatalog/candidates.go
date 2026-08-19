package toolcatalog

import (
	"errors"
	"fmt"

	pep440 "github.com/aquasecurity/go-pep440-version"
	"github.com/aquasecurity/go-version/pkg/semver"
	"sort"
	"strconv"
	"strings"
)

// Candidate selection for portable tool requests.
//
// Selection is per requirement: it turns one request into the ordered list of
// release candidates that requirement could be satisfied by, each carrying the
// contribution union its own references produce. Choosing among candidates so
// that separate requirements do not conflict in a shared provider domain is
// joint solving, which belongs to PTD-09B.

// ToolRequestV1 is one normalized request for a portable tool.
type ToolRequestV1 struct {
	Name       string
	Version    string
	Revision   string
	Context    string
	Binding    string
	Selections []string
	Parameters []ParameterValueV1
}

// ClientCapabilitiesV1 describes the running client a candidate must satisfy.
type ClientCapabilitiesV1 struct {
	ReployVersion      string
	ResolverPrimitives []string
}

// ReleaseCandidateV1 is one release that could satisfy a request on a target,
// with the contributions its own references select.
type ReleaseCandidateV1 struct {
	Manifest      ReleaseManifestV1
	Contract      ReleaseContractV1
	Target        TargetRecordV1
	Fixture       IntegrationFixtureRecordV1
	Profile       ValidationProfileRecordV1
	Binding       string
	Selections    []string
	Parameters    []ParameterValueV1
	Contributions []RecordReferenceV1
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
func (catalog *CatalogV1) SelectReleaseCandidatesV1(request ToolRequestV1, observed TargetIdentityV1,
	client ClientCapabilitiesV1) ([]ReleaseCandidateV1, error) {
	if !validRecordIdentifierV1(request.Name) {
		return nil, fmt.Errorf("tool name %q is invalid", request.Name)
	}
	if request.Version != "" && !validRecordTokenV1(request.Version) {
		return nil, fmt.Errorf("tool version constraint %q is invalid", request.Version)
	}
	if request.Revision != "" {
		if err := validateCanonicalDecimalV1("tool definition revision", request.Revision, true); err != nil {
			return nil, err
		}
	}
	if err := validateTargetIdentityV1(observed); err != nil {
		return nil, fmt.Errorf("observed target: %w", err)
	}
	toolKey, exists := catalog.tools[request.Name]
	if !exists {
		return nil, fmt.Errorf("portable tool %q is not defined", request.Name)
	}
	tool, ok := catalog.records[toolKey].Value.(*ToolRecordV1)
	if !ok {
		return nil, fmt.Errorf("portable tool %q does not resolve to a tool record", request.Name)
	}

	normalized, err := normalizeRequestedVersionV1(tool, request.Version)
	if err != nil {
		return nil, err
	}
	ordered, err := catalog.enumerateReleaseCandidatesV1(tool, normalized, request.Revision)
	if err != nil {
		return nil, err
	}
	if len(ordered) == 0 {
		return nil, fmt.Errorf("portable tool %q has no release matching version %q revision %q",
			request.Name, normalized, request.Revision)
	}

	candidates := make([]ReleaseCandidateV1, 0, len(ordered))
	rejected := make([]candidateRejectionV1, 0, len(ordered))
	for _, manifestKey := range ordered {
		candidate, err := catalog.reduceCandidateV1(manifestKey, request, observed, client)
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
		return nil, fmt.Errorf("portable tool %q has no candidate satisfying the request: %s",
			request.Name, strings.Join(reasons, "; "))
	}
	return candidates, nil
}

// normalizeRequestedVersionV1 gives an opaque request an exact coordinate before
// any candidate is enumerated. A versionless opaque request means the tool
// record's default_version; enumeration never sees an absent coordinate.
func normalizeRequestedVersionV1(tool *ToolRecordV1, requested string) (string, error) {
	if tool.VersionScheme != "opaque" {
		return requested, nil
	}
	if requested != "" {
		return requested, nil
	}
	if tool.DefaultVersion == "" {
		return "", fmt.Errorf("opaque tool %q declares no default version", tool.Name)
	}
	return tool.DefaultVersion, nil
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
	// Token is the constraint with any equality operator removed, empty only
	// for an unconstrained request.
	Token string
	// Exact reports that Token names a version or alias the tool advertises.
	Exact bool
	// Equality reports that the request was written as an equality constraint,
	// which names a coordinate and so never reaches the ordering path.
	Equality bool
}

// classifyRequestedVersionV1 resolves a request's version token against every
// coordinate and alias the tool advertises before any operator is interpreted.
func classifyRequestedVersionV1(releases []releaseEntryV1, constraint string) requestedVersionV1 {
	if constraint == "" {
		return requestedVersionV1{}
	}
	requested := requestedVersionV1{Token: constraint, Equality: strings.HasPrefix(constraint, "==")}
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

// enumerateReleaseCandidatesV1 lists the releases a request authorizes, newest
// first. An exact definition-revision pin restricts enumeration rather than
// being overridden by newest-first ordering.
func (catalog *CatalogV1) enumerateReleaseCandidatesV1(tool *ToolRecordV1, version string,
	revision string) ([]recordKeyV1, error) {
	releases, err := catalog.toolReleasesV1(tool)
	if err != nil {
		return nil, err
	}
	requested := classifyRequestedVersionV1(releases, version)
	// A revision corrects one exact upstream version. Accepting a pin without
	// one would apply that revision to whichever version happened to sort
	// first, which is not the release the pin names.
	if revision != "" && !requested.Exact {
		return nil, fmt.Errorf("definition revision %q requires an exact upstream version, but %q is not one the tool advertises",
			revision, version)
	}
	matched := make([]releaseEntryV1, 0, len(releases))
	for _, release := range releases {
		satisfies, err := releaseSatisfiesConstraintV1(tool.VersionScheme, release.manifest.Version,
			release.manifest.Aliases, requested)
		if err != nil {
			return nil, err
		}
		if !satisfies {
			continue
		}
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
			return compareToolVersionsV1(tool.VersionScheme,
				matched[left].manifest.Version, matched[right].manifest.Version) > 0
		}
		return matched[left].revision > matched[right].revision
	})
	keys := make([]recordKeyV1, 0, len(matched))
	for _, item := range matched {
		keys = append(keys, item.key)
	}
	return keys, nil
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

// integerConstraintSatisfiedV1 evaluates a comma-separated comparison
// expression against an integer coordinate. The integer scheme is ordered but
// is not SemVer: a bare decimal such as `21` is not a semantic version, so a
// SemVer constraint evaluator rejects every release the expression should have
// matched rather than comparing it.
func integerConstraintSatisfiedV1(upstream string, constraint string) (bool, error) {
	value, err := strconv.ParseUint(upstream, 10, 63)
	if err != nil {
		return false, nil
	}
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
			return false, fmt.Errorf("version constraint %q is invalid under the integer scheme", constraint)
		}
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
	if requested.Token == "" {
		prerelease, err := isPrereleaseVersionV1(scheme, upstream)
		if err != nil {
			return false, err
		}
		return !prerelease, nil
	}
	if requested.Exact {
		return matchesRequestedVersionV1(upstream, aliases, requested.Token), nil
	}
	if requested.Equality {
		// The tool advertises no such coordinate, so equality matches nothing
		// rather than falling through to an ordering comparison.
		return false, nil
	}
	switch scheme {
	case "semver":
		constraints, err := semver.NewConstraints(requested.Token)
		if err != nil {
			return false, fmt.Errorf("version constraint %q is invalid under %s", requested.Token, scheme)
		}
		version, err := semver.Parse(upstream)
		if err != nil {
			return false, nil
		}
		return constraints.Check(version), nil
	case "integer":
		return integerConstraintSatisfiedV1(upstream, requested.Token)
	case "pep440":
		specifiers, err := pep440.NewSpecifiers(requested.Token)
		if err != nil {
			return false, fmt.Errorf("version constraint %q is invalid under PEP 440", requested.Token)
		}
		version, err := pep440.Parse(upstream)
		if err != nil {
			return false, nil
		}
		return specifiers.Check(version), nil
	}
	return false, fmt.Errorf("version scheme %q has no ordering, so constraint %q cannot be evaluated",
		scheme, requested.Token)
}

// reduceCandidateV1 removes a candidate the request cannot use, before any joint
// solving sees it. Every rejection here is intrinsic to this one requirement.
func (catalog *CatalogV1) reduceCandidateV1(manifestKey recordKeyV1, request ToolRequestV1,
	observed TargetIdentityV1, client ClientCapabilitiesV1) (ReleaseCandidateV1, error) {
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
	if !containsRecordValueV1(contract.Contexts, request.Context) {
		return ReleaseCandidateV1{}, fmt.Errorf("context %q is not supported", request.Context)
	}
	target, err := selectExactTargetV1(view, manifest, observed)
	if err != nil {
		return ReleaseCandidateV1{}, err
	}
	binding, err := resolveRequestedBindingV1(contract.Binding, target, request.Binding)
	if err != nil {
		return ReleaseCandidateV1{}, err
	}
	selections, err := resolveRequestedSelectionsV1(contract.Selections, target, request.Selections)
	if err != nil {
		return ReleaseCandidateV1{}, err
	}
	parameters, err := resolveRequestedParametersV1(contract, target, request.Parameters)
	if err != nil {
		return ReleaseCandidateV1{}, err
	}
	contributions, err := candidateContributionsV1(view, contract, target, binding, selections)
	if err != nil {
		return ReleaseCandidateV1{}, err
	}
	fixtureRecord, profileRecord, err := candidateValidationRecordsV1(view, target, contract,
		supportTupleV1{Context: request.Context, Binding: binding, Selections: selections, Parameters: parameters})
	if err != nil {
		return ReleaseCandidateV1{}, err
	}
	// Every record placed in a candidate is cloned, so a returned candidate
	// cannot alias loaded catalog state.
	return ReleaseCandidateV1{
		Manifest:      cloneReleaseManifestV1(manifest),
		Contract:      cloneReleaseContractV1(contract),
		Target:        cloneTargetRecordV1(target),
		Fixture:       cloneIntegrationFixtureV1(fixtureRecord),
		Profile:       cloneValidationProfileV1(profileRecord),
		Binding:       binding,
		Selections:    append([]string{}, selections...),
		Parameters:    append([]ParameterValueV1{}, parameters...),
		Contributions: contributions,
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

// resolveRequestedBindingV1 applies binding inference: an omitted binding means
// the contract default, and the resolved symbol must be advertised by the target
// rather than merely declared by the contract.
func resolveRequestedBindingV1(request BindingRequestV1, target *TargetRecordV1, requested string) (string, error) {
	if requested == "" {
		requested = request.Default
	}
	if requested == "" && request.Required {
		// A contract may require a binding, declare no default, and leave the
		// choice to the target. When the target advertises exactly one of the
		// declared options there is nothing to choose, so inference names it
		// rather than failing a request that omitted what it could not know.
		inferred := ""
		for _, option := range request.Options {
			if !targetAdvertisesBindingV1(target, option) {
				continue
			}
			if inferred != "" {
				return "", fmt.Errorf("a binding is required and the target advertises more than one")
			}
			inferred = option
		}
		requested = inferred
	}
	if requested == "" {
		if request.Required {
			return "", fmt.Errorf("a binding is required")
		}
		return "", nil
	}
	if !containsRecordValueV1(request.Options, requested) {
		return "", fmt.Errorf("binding %q is not declared by the release contract", requested)
	}
	if !targetAdvertisesBindingV1(target, requested) {
		return "", fmt.Errorf("binding %q is not advertised by the target", requested)
	}
	return requested, nil
}

// resolveRequestedSelectionsV1 normalizes a selection set against the contract
// and the target, enforcing the cardinality and compatibility rules the contract
// declares rather than only membership.
func resolveRequestedSelectionsV1(request SelectionRequestV1, target *TargetRecordV1,
	requested []string) ([]string, error) {
	if requested == nil {
		requested = request.Defaults
	}
	selected := append([]string{}, requested...)
	sort.Strings(selected)
	advertised := make([]string, 0, len(target.Selections))
	for _, selection := range target.Selections {
		advertised = append(advertised, selection.Name)
	}
	for index, value := range selected {
		if index > 0 && selected[index-1] == value {
			return nil, fmt.Errorf("selection %q is duplicated", value)
		}
		if !containsRecordValueV1(request.Options, value) {
			return nil, fmt.Errorf("selection %q is not declared by the release contract", value)
		}
		if !containsRecordValueV1(advertised, value) {
			return nil, fmt.Errorf("selection %q is not advertised by the target", value)
		}
	}
	minimum, _ := strconv.ParseUint(request.Minimum, 10, 63)
	maximum, _ := strconv.ParseUint(request.Maximum, 10, 63)
	if uint64(len(selected)) < minimum {
		return nil, fmt.Errorf("at least %s selections are required", request.Minimum)
	}
	if uint64(len(selected)) > maximum {
		return nil, fmt.Errorf("at most %s selections are permitted", request.Maximum)
	}
	if len(selected) != 0 && !selectionSetCompatibleV1(selected, request.CompatibilityGroups) {
		return nil, fmt.Errorf("selections are not compatible with one another")
	}
	return selected, nil
}

// resolveRequestedParametersV1 normalizes typed parameters against both the
// contract schema and the selected target's narrowing, filling declared defaults
// so a candidate's identity does not depend on what the caller left unsaid.
func resolveRequestedParametersV1(contract *ReleaseContractV1, target *TargetRecordV1,
	requested []ParameterValueV1) ([]ParameterValueV1, error) {
	provided := make(map[string]string, len(requested))
	for _, value := range requested {
		if _, duplicate := provided[value.Name]; duplicate {
			return nil, fmt.Errorf("parameter %q is supplied twice", value.Name)
		}
		provided[value.Name] = value.Value
	}
	declared := make(map[string]struct{}, len(contract.Parameters))
	resolved := make([]ParameterValueV1, 0, len(contract.Parameters))
	for _, parameter := range contract.Parameters {
		declared[parameter.Name] = struct{}{}
		value, supplied := provided[parameter.Name]
		if !supplied {
			if parameter.Default == nil {
				if parameter.Required {
					return nil, fmt.Errorf("parameter %q is required", parameter.Name)
				}
				continue
			}
			value = *parameter.Default
		}
		if !parameterValueInSchemaV1(value, parameter) {
			return nil, fmt.Errorf("parameter %q value %q is outside the contract domain", parameter.Name, value)
		}
		if !targetParameterAllowsV1(target.Parameters, parameter.Name, value) {
			return nil, fmt.Errorf("parameter %q value %q is outside the target domain", parameter.Name, value)
		}
		resolved = append(resolved, ParameterValueV1{Name: parameter.Name, Value: value})
	}
	for name := range provided {
		if _, found := declared[name]; !found {
			return nil, fmt.Errorf("parameter %q is not declared by the release contract", name)
		}
	}
	return resolved, nil
}

// candidateContributionsV1 builds the union this candidate's own references
// select: unconditional target contributions plus only the entries for the
// resolved binding and normalized selections. Unselected contributions never
// enter it.
func candidateContributionsV1(view map[string]loadedRecordV1, contract *ReleaseContractV1,
	target *TargetRecordV1, binding string, selections []string) ([]RecordReferenceV1, error) {
	references := append([]RecordReferenceV1{}, target.PackageSets...)
	references = append(references, target.Payloads...)
	if binding != "" {
		entry, found := targetBindingEntryV1(target, binding)
		if !found {
			return nil, fmt.Errorf("target does not provide binding %q", binding)
		}
		references = append(references, entry.Contract)
		references = append(references, entry.Artifacts...)
		references = append(references, entry.PackageSets...)
	}
	for _, name := range selections {
		entry, found := targetSelectionEntryV1(target, name)
		if !found {
			return nil, fmt.Errorf("target does not provide selection %q", name)
		}
		references = append(references, entry.Payloads...)
		references = append(references, entry.PackageSets...)
	}
	// Every contribution must resolve exactly, so a candidate that survives
	// selection is one whose own references are all present.
	for _, reference := range references {
		if _, err := resolvedRecordV1(view, reference); err != nil {
			return nil, err
		}
	}
	return canonicalReferenceUnionV1(references), nil
}

// candidateValidationRecordsV1 finds the fixture proving this exact tuple and
// the validation profile the target uses. Validation data authorizes a support
// claim; it does not enter selected identity.
func candidateValidationRecordsV1(view map[string]loadedRecordV1, target *TargetRecordV1,
	contract *ReleaseContractV1, tuple supportTupleV1) (*IntegrationFixtureRecordV1, *ValidationProfileRecordV1, error) {
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
		key, err := supportTupleKeyV1(normalizedFixtureTupleV1(contract, fixture))
		if err != nil {
			return nil, nil, err
		}
		if key == wanted {
			matched = fixture
			break
		}
	}
	if matched == nil {
		return nil, nil, fmt.Errorf("no integration fixture covers the requested support tuple")
	}
	profileRecord, err := resolvedRecordV1(view, target.ValidationProfile)
	if err != nil {
		return nil, nil, err
	}
	profile, ok := profileRecord.Value.(*ValidationProfileRecordV1)
	if !ok {
		return nil, nil, fmt.Errorf("validation profile %q is not a profile record", target.ValidationProfile.ID)
	}
	return matched, profile, nil
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

// targetSelectionEntryV1 finds the target's contribution mapping for a selection.
func targetSelectionEntryV1(target *TargetRecordV1, name string) (TargetSelectionV1, bool) {
	for _, selection := range target.Selections {
		if selection.Name == name {
			return selection, true
		}
	}
	return TargetSelectionV1{}, false
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
