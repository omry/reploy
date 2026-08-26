// Package toolrequest parses and canonically merges portable-tool requests
// without consulting catalog data or a tool-specific version scheme.
package toolrequest

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

// CanonicalBindingDemandV1 preserves the cumulative binding demand retained
// by same-scope request merging. An omitted binding remains an inference demand
// even when it is merged with explicit bindings.
type CanonicalBindingDemandV1 struct {
	All      bool
	Infer    bool
	Explicit []string
}

// CanonicalRequirementGroupV1 is the catalog-independent merged demand for one
// qualified tool in one resolution scope.
type CanonicalRequirementGroupV1 struct {
	Scope              string
	Tool               string
	VersionConstraints []string
	DefinitionRevision string
	Context            string
	Binding            CanonicalBindingDemandV1
	Selections         map[string][]string
}

// SetSyntaxV1 retains one scalar-or-list request field until interpolation and
// canonical normalization have completed.
type SetSyntaxV1 struct {
	Values []string
}

// SyntaxV1 retains either a compact scalar or a structured request mapping.
// The exported representation lets blueprint interpolation visit every string.
type SyntaxV1 struct {
	Compact               string
	Structured            bool
	Tool                  string
	Version               string
	HasVersion            bool
	DefinitionRevision    string
	HasDefinitionRevision bool
	Binding               SetSyntaxV1
	HasBinding            bool
	Selections            map[string]SetSyntaxV1
}

// CanonicalSetV1 separates identity-bearing groups from diagnostic source
// locations. Sources is keyed by qualified tool and must not be serialized into
// request, lock, or cache identity.
type CanonicalSetV1 struct {
	Groups  []CanonicalRequirementGroupV1
	Sources map[string][]string `json:"-"`
}

// UnmarshalYAML accepts the two public request forms while retaining version
// tokens for catalog-owned scheme validation.
func (request *SyntaxV1) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		if node.Tag != "!!str" {
			return fmt.Errorf("portable tool request must be a string or mapping")
		}
		request.Compact = node.Value
		request.Selections = map[string]SetSyntaxV1{}
		return nil
	}
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("portable tool request must be a string or mapping")
	}
	request.Structured = true
	request.Selections = map[string]SetSyntaxV1{}
	present := map[string]bool{}
	for index := 0; index < len(node.Content); index += 2 {
		key := node.Content[index]
		value := node.Content[index+1]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
			return fmt.Errorf("portable tool request field name must be a string")
		}
		if present[key.Value] {
			return fmt.Errorf("portable tool request contains duplicate field %q", key.Value)
		}
		present[key.Value] = true
		switch key.Value {
		case "tool":
			text, err := requestScalarV1(value, "tool")
			if err != nil {
				return err
			}
			request.Tool = text
		case "version":
			text, err := requestScalarV1(value, "version")
			if err != nil {
				return err
			}
			request.Version = text
			request.HasVersion = true
		case "definition_revision":
			if value.Kind != yaml.ScalarNode || value.Tag != "!!str" && value.Tag != "!!int" {
				return fmt.Errorf("portable tool request definition_revision must be a string or integer")
			}
			request.DefinitionRevision = value.Value
			request.HasDefinitionRevision = true
		case "binding":
			set, err := requestSetSyntaxV1(value, "binding")
			if err != nil {
				return err
			}
			request.Binding = set
			request.HasBinding = true
		case "select":
			selections, err := requestSelectionsSyntaxV1(value)
			if err != nil {
				return err
			}
			request.Selections = selections
		default:
			return fmt.Errorf("portable tool request contains unknown field %q", key.Value)
		}
	}
	return nil
}

func requestScalarV1(node *yaml.Node, field string) (string, error) {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return "", fmt.Errorf("portable tool request %s must be a string", field)
	}
	return node.Value, nil
}

func requestSetSyntaxV1(node *yaml.Node, field string) (SetSyntaxV1, error) {
	if node.Kind == yaml.ScalarNode {
		if node.Tag != "!!str" {
			return SetSyntaxV1{}, fmt.Errorf("portable tool request %s must be a string or list", field)
		}
		return SetSyntaxV1{Values: []string{node.Value}}, nil
	}
	if node.Kind != yaml.SequenceNode {
		return SetSyntaxV1{}, fmt.Errorf("portable tool request %s must be a string or list", field)
	}
	values := make([]string, 0, len(node.Content))
	for _, item := range node.Content {
		if item.Kind != yaml.ScalarNode || item.Tag != "!!str" {
			return SetSyntaxV1{}, fmt.Errorf("portable tool request %s list values must be strings", field)
		}
		values = append(values, item.Value)
	}
	return SetSyntaxV1{Values: values}, nil
}

func requestSelectionsSyntaxV1(node *yaml.Node) (map[string]SetSyntaxV1, error) {
	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("portable tool request select must be a mapping")
	}
	if len(node.Content) == 0 {
		return nil, fmt.Errorf("portable tool request select must not be empty")
	}
	result := make(map[string]SetSyntaxV1, len(node.Content)/2)
	for index := 0; index < len(node.Content); index += 2 {
		key := node.Content[index]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
			return nil, fmt.Errorf("portable tool request selection dimension must be a string")
		}
		if _, exists := result[key.Value]; exists {
			return nil, fmt.Errorf("portable tool request contains duplicate selection dimension %q", key.Value)
		}
		value, err := requestSetSyntaxV1(node.Content[index+1], "selection "+strconv.Quote(key.Value))
		if err != nil {
			return nil, err
		}
		result[key.Value] = value
	}
	return result, nil
}

// NormalizeAndMergeV1 normalizes one container's requests and merges demands
// only within its caller-supplied canonical resolution scope.
func NormalizeAndMergeV1(requests []SyntaxV1, scope string, context string, sourcePrefix string) (CanonicalSetV1, error) {
	if !validTokenV1(scope) {
		return CanonicalSetV1{}, fmt.Errorf("portable tool resolution scope is invalid")
	}
	if !validIdentifierV1(context) {
		return CanonicalSetV1{}, fmt.Errorf("portable tool context %q is invalid", context)
	}
	groups := map[string]CanonicalRequirementGroupV1{}
	sources := map[string][]string{}
	for index, syntax := range requests {
		group, err := normalizeV1(syntax, scope, context)
		if err != nil {
			return CanonicalSetV1{}, fmt.Errorf("%s[%d]: %w", sourcePrefix, index, err)
		}
		if previous, exists := groups[group.Tool]; exists {
			group, err = MergeV1(previous, group)
			if err != nil {
				return CanonicalSetV1{}, fmt.Errorf("%s[%d]: %w", sourcePrefix, index, err)
			}
		}
		groups[group.Tool] = group
		sources[group.Tool] = append(sources[group.Tool], fmt.Sprintf("%s[%d]", sourcePrefix, index))
	}
	tools := make([]string, 0, len(groups))
	for tool := range groups {
		tools = append(tools, tool)
	}
	sort.Strings(tools)
	result := CanonicalSetV1{Groups: make([]CanonicalRequirementGroupV1, 0, len(tools)), Sources: sources}
	for _, tool := range tools {
		result.Groups = append(result.Groups, groups[tool])
	}
	return result, nil
}

func normalizeV1(syntax SyntaxV1, scope string, context string) (CanonicalRequirementGroupV1, error) {
	if !syntax.Structured {
		tool, version, revision, err := parseCompactV1(syntax.Compact)
		if err != nil {
			return CanonicalRequirementGroupV1{}, err
		}
		return canonicalGroupV1(scope, context, tool, version, revision, false, SetSyntaxV1{}, nil)
	}
	if syntax.Tool == "" {
		return CanonicalRequirementGroupV1{}, fmt.Errorf("portable tool request tool is required")
	}
	tool := syntax.Tool
	if syntax.HasVersion && syntax.Version == "" {
		return CanonicalRequirementGroupV1{}, fmt.Errorf("portable tool request version must not be empty")
	}
	revision := ""
	if syntax.HasDefinitionRevision {
		revision = syntax.DefinitionRevision
		if err := validateRevisionV1(revision); err != nil {
			return CanonicalRequirementGroupV1{}, err
		}
	}
	return canonicalGroupV1(scope, context, tool, syntax.Version, revision, syntax.HasBinding, syntax.Binding, syntax.Selections)
}

func canonicalGroupV1(scope string, context string, tool string, version string, revision string,
	hasBinding bool, bindingSyntax SetSyntaxV1, selectionSyntax map[string]SetSyntaxV1) (CanonicalRequirementGroupV1, error) {
	if !validIdentifierV1(tool) {
		return CanonicalRequirementGroupV1{}, fmt.Errorf("portable tool name %q is invalid", tool)
	}
	constraints := []string{}
	if version != "" {
		if !validTokenV1(version) {
			return CanonicalRequirementGroupV1{}, fmt.Errorf("portable tool version constraint must be a nonempty structural token")
		}
		constraints = []string{version}
	}
	binding := CanonicalBindingDemandV1{Infer: true, Explicit: []string{}}
	if hasBinding {
		values, all, err := normalizeSetV1("binding", bindingSyntax.Values, true)
		if err != nil {
			return CanonicalRequirementGroupV1{}, err
		}
		binding = CanonicalBindingDemandV1{All: all, Explicit: values}
	}
	selections := map[string][]string{}
	for dimension, raw := range selectionSyntax {
		if !validIdentifierV1(dimension) {
			return CanonicalRequirementGroupV1{}, fmt.Errorf("portable tool selection dimension %q is invalid", dimension)
		}
		values, _, err := normalizeSetV1("selection "+strconv.Quote(dimension), raw.Values, false)
		if err != nil {
			return CanonicalRequirementGroupV1{}, err
		}
		selections[dimension] = values
	}
	return CanonicalRequirementGroupV1{
		Scope: scope, Tool: tool, VersionConstraints: constraints, DefinitionRevision: revision,
		Context: context, Binding: binding, Selections: selections,
	}, nil
}

func parseCompactV1(raw string) (string, string, string, error) {
	if raw == "" || strings.TrimSpace(raw) != raw || strings.IndexFunc(raw, unicode.IsSpace) >= 0 || containsControlV1(raw) {
		return "", "", "", fmt.Errorf("compact portable tool request must be nonempty and contain no whitespace")
	}
	if strings.ContainsAny(raw, "[]{}") {
		return "", "", "", fmt.Errorf("compact portable tool request does not support bracket syntax")
	}
	if !strings.HasPrefix(raw, "tool:") {
		return "", "", "", fmt.Errorf("compact portable tool request must start with tool:")
	}
	rest := strings.TrimPrefix(raw, "tool:")
	boundary := strings.IndexAny(rest, "=<>!~^")
	tool := rest
	version := ""
	if boundary >= 0 {
		tool = rest[:boundary]
		version = rest[boundary:]
	}
	if !validIdentifierV1(tool) {
		return "", "", "", fmt.Errorf("portable tool name %q is invalid", tool)
	}
	if version != "" && !validTokenV1(version) {
		return "", "", "", fmt.Errorf("portable tool version constraint must be a structural token")
	}
	// A trailing ~<decimal> remains part of the structural token here. Only the
	// loaded tool-wide coordinate map can distinguish a compact revision suffix
	// from scheme-native version syntax.
	return tool, version, "", nil
}

func normalizeSetV1(field string, raw []string, allowWildcard bool) ([]string, bool, error) {
	if len(raw) == 0 {
		return nil, false, fmt.Errorf("portable tool request %s list must not be empty", field)
	}
	seen := map[string]bool{}
	values := make([]string, 0, len(raw))
	all := false
	for _, value := range raw {
		if value == "*" && allowWildcard {
			all = true
		} else if !validIdentifierV1(value) {
			return nil, false, fmt.Errorf("portable tool request %s value %q is invalid", field, value)
		}
		if seen[value] {
			return nil, false, fmt.Errorf("portable tool request %s contains duplicate value %q", field, value)
		}
		seen[value] = true
		values = append(values, value)
	}
	if all {
		if len(values) != 1 {
			return nil, false, fmt.Errorf("portable tool request %s cannot mix wildcard and explicit values", field)
		}
		return []string{}, true, nil
	}
	sort.Strings(values)
	return values, false, nil
}

func validateRevisionV1(value string) error {
	if value == "" || value == "0" || value[0] == '0' {
		return fmt.Errorf("portable tool definition_revision must be a positive canonical decimal")
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return fmt.Errorf("portable tool definition_revision must be a positive canonical decimal")
		}
	}
	return nil
}

// MergeV1 applies the public field-by-field same-scope merge rules.
func MergeV1(left CanonicalRequirementGroupV1, right CanonicalRequirementGroupV1) (CanonicalRequirementGroupV1, error) {
	if left.Scope != right.Scope || left.Tool != right.Tool {
		return CanonicalRequirementGroupV1{}, fmt.Errorf("portable tool requirements may merge only for the same scope and tool")
	}
	if left.Context != right.Context {
		return CanonicalRequirementGroupV1{}, fmt.Errorf("portable tool %q has conflicting contexts %q and %q", left.Tool, left.Context, right.Context)
	}
	if left.DefinitionRevision != "" && right.DefinitionRevision != "" && left.DefinitionRevision != right.DefinitionRevision {
		return CanonicalRequirementGroupV1{}, fmt.Errorf("portable tool %q has conflicting definition revisions %s and %s", left.Tool, left.DefinitionRevision, right.DefinitionRevision)
	}
	result := left
	result.VersionConstraints = unionV1(left.VersionConstraints, right.VersionConstraints)
	if result.DefinitionRevision == "" {
		result.DefinitionRevision = right.DefinitionRevision
	}
	if left.Binding.All || right.Binding.All {
		result.Binding = CanonicalBindingDemandV1{All: true, Explicit: []string{}}
	} else {
		result.Binding = CanonicalBindingDemandV1{
			Infer:    left.Binding.Infer || right.Binding.Infer,
			Explicit: unionV1(left.Binding.Explicit, right.Binding.Explicit),
		}
	}
	result.Selections = map[string][]string{}
	for dimension, values := range left.Selections {
		result.Selections[dimension] = append([]string{}, values...)
	}
	for dimension, values := range right.Selections {
		result.Selections[dimension] = unionV1(result.Selections[dimension], values)
	}
	return result, nil
}

func unionV1(left []string, right []string) []string {
	seen := make(map[string]bool, len(left)+len(right))
	result := make([]string, 0, len(left)+len(right))
	for _, value := range append(append([]string{}, left...), right...) {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func validIdentifierV1(value string) bool {
	if value == "" || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, character := range value[1:] {
		if character < 'a' || character > 'z' {
			if character < '0' || character > '9' {
				if character != '-' {
					return false
				}
			}
		}
	}
	return true
}

func validTokenV1(value string) bool {
	return value != "" && strings.TrimSpace(value) == value && !containsControlV1(value)
}

func containsControlV1(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) {
			return true
		}
	}
	return false
}
