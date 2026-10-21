package blueprint

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const identityMappingPathV1 = "environment.runtime.identity_mapping"

// MappingScalarV1 preserves source type through resolved blueprint JSON.
// A float variable whose value is 1 must never become an integer mapping ID.
type MappingScalarV1 struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

type MappingIntervalV1 struct {
	ContainerStart MappingScalarV1 `yaml:"container_start"`
	HostStart      MappingScalarV1 `yaml:"host_start"`
	Count          MappingScalarV1 `yaml:"count"`
}

// IdentityMappingV1 retains portable intent, not allocation or admission.
// Variables retain every original blueprint variable's scalar type.
type IdentityMappingV1 struct {
	Schema    int                        `yaml:"schema"`
	Profile   MappingScalarV1            `yaml:"profile"`
	UIDs      []MappingIntervalV1        `yaml:"uids"`
	GIDs      []MappingIntervalV1        `yaml:"gids"`
	Variables map[string]MappingScalarV1 `yaml:"-" json:"Variables,omitempty"`
}

func (value *MappingScalarV1) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("%s values must be scalars", identityMappingPathV1)
	}
	switch node.Tag {
	case "!!int":
		var n uint64
		if err := node.Decode(&n); err != nil || n >= math.MaxUint32 {
			return fmt.Errorf("%s IDs must be usable unsigned 32-bit integers", identityMappingPathV1)
		}
		*value = MappingScalarV1{"integer", strconv.FormatUint(n, 10)}
	case "!!str":
		*value = mappingStringV1(node.Value)
	default:
		return fmt.Errorf("%s values must be integers, profile strings, required values or expressions", identityMappingPathV1)
	}
	return nil
}

func mappingStringV1(text string) MappingScalarV1 {
	kind := "string"
	if text == "???" {
		kind = "missing"
	} else if interpolationPattern.MatchString(text) {
		kind = "expression"
	}
	return MappingScalarV1{kind, text}
}

func (value MappingScalarV1) integer() (uint64, bool) {
	n, err := strconv.ParseUint(value.Value, 10, 32)
	return n, value.Kind == "integer" && err == nil && n < math.MaxUint32 && strconv.FormatUint(n, 10) == value.Value
}

func (value MappingScalarV1) validate(path string, profile bool) error {
	if value.Kind == "missing" && value.Value == "???" ||
		value.Kind == "expression" && interpolationPattern.MatchString(value.Value) {
		return nil
	}
	if profile && value.Kind == "string" && (value.Value == "exact" || value.Value == "bounded") {
		return nil
	}
	if !profile {
		if _, ok := value.integer(); ok {
			return nil
		}
	}
	return fmt.Errorf("%s has an invalid type or value", path)
}

func (mapping *IdentityMappingV1) UnmarshalYAML(node *yaml.Node) error {
	present, err := validateSyntaxMapping(node, identityMappingPathV1, map[string]bool{
		"schema": true, "profile": true, "uids": true, "gids": true,
	})
	if err != nil {
		return err
	}
	for _, field := range []string{"schema", "profile", "uids", "gids"} {
		if !present[field] {
			return fmt.Errorf("%s.%s is required", identityMappingPathV1, field)
		}
	}
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == "schema" && node.Content[i+1].Tag != "!!int" {
			return fmt.Errorf("%s.schema must be the integer 1", identityMappingPathV1)
		}
	}
	type plain IdentityMappingV1
	if err := node.Decode((*plain)(mapping)); err != nil {
		return err
	}
	return mapping.validate()
}

func (interval *MappingIntervalV1) UnmarshalYAML(node *yaml.Node) error {
	present, err := validateSyntaxMapping(node, identityMappingPathV1+" interval", map[string]bool{
		"container_start": true, "host_start": true, "count": true,
	})
	if err != nil {
		return err
	}
	for _, field := range []string{"container_start", "host_start", "count"} {
		if !present[field] {
			return fmt.Errorf("%s interval.%s is required", identityMappingPathV1, field)
		}
	}
	type plain MappingIntervalV1
	return node.Decode((*plain)(interval))
}

func (mapping *IdentityMappingV1) validate() error {
	if mapping == nil {
		return nil
	}
	if mapping.Schema != 1 {
		return fmt.Errorf("%s.schema must be the integer 1", identityMappingPathV1)
	}
	if err := mapping.Profile.validate(identityMappingPathV1+".profile", true); err != nil {
		return err
	}
	for i, values := range [][]MappingIntervalV1{mapping.UIDs, mapping.GIDs} {
		path := identityMappingPathV1 + "." + []string{"uids", "gids"}[i]
		if len(values) == 0 || mapping.Profile == (MappingScalarV1{"string", "bounded"}) && len(values) != 1 {
			return fmt.Errorf("%s must be nonempty and bounded maps require exactly one interval", path)
		}
		if err := validateMappingLiteralsV1(path, values); err != nil {
			return err
		}
	}
	for name, value := range mapping.Variables {
		if name == "" || strings.Contains(name, ".") {
			return fmt.Errorf("%s has an invalid blueprint variable binding", identityMappingPathV1)
		}
		switch value.Kind {
		case "integer":
			signed, e1 := strconv.ParseInt(value.Value, 10, 64)
			unsigned, e2 := strconv.ParseUint(value.Value, 10, 64)
			if e1 == nil && strconv.FormatInt(signed, 10) == value.Value || e2 == nil && strconv.FormatUint(unsigned, 10) == value.Value {
				continue
			}
		case "float":
			n, err := strconv.ParseFloat(value.Value, 64)
			if err == nil && !math.IsInf(n, 0) && !math.IsNaN(n) && strconv.FormatFloat(n, 'g', -1, 64) == value.Value {
				continue
			}
		case "bool":
			if value.Value == "true" || value.Value == "false" {
				continue
			}
		case "null", "invalid":
			if value.Value == "" {
				continue
			}
		case "string":
			continue
		case "missing", "expression":
			if value.validate(identityMappingPathV1, false) == nil {
				continue
			}
		default:
		}
		return fmt.Errorf("%s has an invalid blueprint variable type", identityMappingPathV1)
	}
	return nil
}

func validateMappingLiteralsV1(path string, values []MappingIntervalV1) error {
	type interval struct{ start, end uint64 }
	var container, host []interval
	for i, value := range values {
		fields := []MappingScalarV1{value.ContainerStart, value.HostStart, value.Count}
		for j, field := range []string{"container_start", "host_start", "count"} {
			if err := fields[j].validate(fmt.Sprintf("%s[%d].%s", path, i, field), false); err != nil {
				return err
			}
		}
		count, countKnown := value.Count.integer()
		if countKnown && count == 0 {
			return fmt.Errorf("%s[%d].count must be positive", path, i)
		}
		if !countKnown {
			count = 1 // Every valid resolution must have a positive count.
		}
		for j, start := range []MappingScalarV1{value.ContainerStart, value.HostStart} {
			n, known := start.integer()
			if j == 1 && known && n == 0 {
				return fmt.Errorf("%s[%d].host_start cannot include host root", path, i)
			}
			if !known {
				continue
			}
			if n+count > math.MaxUint32 {
				return fmt.Errorf("%s[%d] exceeds usable unsigned 32-bit IDs", path, i)
			}
			if j == 0 {
				container = append(container, interval{n, n + count})
			} else {
				host = append(host, interval{n, n + count})
			}
		}
	}
	for _, intervals := range [][]interval{container, host} {
		slices.SortFunc(intervals, func(a, b interval) int {
			if a.start < b.start {
				return -1
			}
			if a.start > b.start {
				return 1
			}
			return 0
		})
		for i := 1; i < len(intervals); i++ {
			if intervals[i].start < intervals[i-1].end {
				return fmt.Errorf("%s literal intervals overlap", path)
			}
		}
	}
	return nil
}

func mappingContainerKeyV1(value MappingScalarV1, variables map[string]MappingScalarV1) (uint64, bool) {
	if value.Kind == "expression" {
		if match := wholeInterpolationPattern.FindStringSubmatch(value.Value); match != nil {
			value = variables[match[1]]
		}
	}
	return value.integer()
}

// Return canonical validated intervals without changing caller-owned data.
func canonicalIdentityMappingV1(mapping *IdentityMappingV1) *IdentityMappingV1 {
	if mapping == nil {
		return nil
	}
	normalized := *mapping
	normalized.UIDs = slices.Clone(mapping.UIDs)
	normalized.GIDs = slices.Clone(mapping.GIDs)
	for _, values := range [][]MappingIntervalV1{normalized.UIDs, normalized.GIDs} {
		slices.SortFunc(values, func(a, b MappingIntervalV1) int {
			left, leftKnown := mappingContainerKeyV1(a.ContainerStart, mapping.Variables)
			right, rightKnown := mappingContainerKeyV1(b.ContainerStart, mapping.Variables)
			if leftKnown != rightKnown {
				if leftKnown {
					return -1
				}
				return 1
			}
			if leftKnown && left != right {
				return cmp.Compare(left, right)
			}
			// Unknown keys and equal numeric keys keep one deterministic intent.
			other := [3]MappingScalarV1{b.ContainerStart, b.HostStart, b.Count}
			for i, field := range [3]MappingScalarV1{a.ContainerStart, a.HostStart, a.Count} {
				if order := cmp.Compare(field.Kind, other[i].Kind); order != 0 {
					return order
				}
				if order := cmp.Compare(field.Value, other[i].Value); order != 0 {
					return order
				}
			}
			return 0
		})
	}
	return &normalized
}

func prepareIdentityMappingV1(mapping *IdentityMappingV1, variables map[string]any) (*IdentityMappingV1, error) {
	if err := mapping.validate(); err != nil || mapping == nil {
		return mapping, err
	}
	prepared := *mapping
	mapping = &prepared
	mapping.Variables = nil
	if len(variables) != 0 {
		mapping.Variables = make(map[string]MappingScalarV1, len(variables))
	}
	for name, item := range variables {
		mapping.Variables[name] = mappingVariableV1(item)
	}
	return canonicalIdentityMappingV1(mapping), nil
}

func mappingVariableV1(item any) MappingScalarV1 {
	typed := MappingScalarV1{Value: fmt.Sprint(item)}
	switch item.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		typed.Kind = "integer"
	case float32, float64:
		typed.Kind = "float"
	case bool:
		typed.Kind = "bool"
	case nil:
		typed = MappingScalarV1{Kind: "null"}
	case string:
		typed = mappingStringV1(item.(string))
	default:
		typed = MappingScalarV1{Kind: "invalid"}
	}
	return typed
}

func (mapping *IdentityMappingV1) validateVariables(variables map[string]any) error {
	if mapping == nil {
		return nil
	}
	inconsistent := fmt.Errorf("%s variable bindings do not match blueprint variables; restage from the original blueprint", identityMappingPathV1)
	if len(mapping.Variables) != len(variables) {
		return inconsistent
	}
	for name, item := range variables {
		value, found := mapping.Variables[name]
		wanted := mappingVariableV1(item)
		if !found {
			return inconsistent
		}
		if value == wanted {
			continue
		}
		// JSON loses integral numeric kinds; the saved source kind stays authoritative.
		if (value.Kind == "integer" || value.Kind == "float") && (wanted.Kind == "integer" || wanted.Kind == "float") {
			var declared any = json.Number(value.Value)
			if value.Kind == "float" {
				number, err := strconv.ParseFloat(value.Value, 64)
				if err != nil {
					return inconsistent
				}
				declared = number
			}
			saved, e1 := json.Marshal(declared)
			resolved, e2 := json.Marshal(item)
			if e1 == nil && e2 == nil && bytes.Equal(saved, resolved) {
				continue
			}
		}
		return inconsistent
	}
	return nil
}
