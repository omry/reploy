package blueprint

import (
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
// Variables are mapping-local type bindings from the original blueprint.
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
			if err == nil && !math.IsInf(n, 0) && !math.IsNaN(n) {
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
		for j, start := range []MappingScalarV1{value.ContainerStart, value.HostStart} {
			n, known := start.integer()
			if j == 1 && known && n == 0 {
				return fmt.Errorf("%s[%d].host_start cannot include host root", path, i)
			}
			if !known || !countKnown {
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

func prepareIdentityMappingV1(mapping *IdentityMappingV1, variables map[string]any) (*IdentityMappingV1, error) {
	if err := mapping.validate(); err != nil || mapping == nil {
		return mapping, err
	}
	mapping.Variables = nil
	fields := []MappingScalarV1{mapping.Profile}
	for _, values := range [][]MappingIntervalV1{mapping.UIDs, mapping.GIDs} {
		for _, value := range values {
			fields = append(fields, value.ContainerStart, value.HostStart, value.Count)
		}
	}
	for _, value := range fields {
		if value.Kind != "expression" {
			continue
		}
		for _, match := range interpolationPattern.FindAllStringSubmatch(value.Value, -1) {
			name := match[1]
			item, found := variables[name]
			if !found || strings.Contains(name, ".") {
				continue
			}
			if mapping.Variables == nil {
				mapping.Variables = map[string]MappingScalarV1{}
			}
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
			mapping.Variables[name] = typed
		}
	}
	return mapping, nil
}
