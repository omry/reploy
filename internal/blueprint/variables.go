package blueprint

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var variableNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var interpolationPattern = regexp.MustCompile(`\{\{\s*([A-Za-z_][A-Za-z0-9_.]*)\s*\}\}`)
var wholeInterpolationPattern = regexp.MustCompile(`^\s*\{\{\s*([A-Za-z_][A-Za-z0-9_.]*)\s*\}\}\s*$`)

var reservedVariableRoots = map[string]bool{
	"blueprint":   true,
	"environment": true,
	"docker":      true,
	"reploy":      true,
	"user":        true,
	"system":      true,
}

type variableResolver struct {
	source   map[string]any
	resolved map[string]any
	active   map[string]bool
	stack    []string
}

func resolveVariables(source map[string]any) (map[string]any, error) {
	resolver := variableResolver{
		source:   source,
		resolved: map[string]any{},
		active:   map[string]bool{},
	}
	names := make([]string, 0, len(source))
	for name := range source {
		if !variableNamePattern.MatchString(name) {
			return nil, fmt.Errorf("environment.vars.%s must be a valid identifier", name)
		}
		if reservedVariableRoots[name] {
			return nil, fmt.Errorf("environment.vars.%s shadows reserved root %q", name, name)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, err := resolver.resolve(name); err != nil {
			return nil, err
		}
	}
	return resolver.resolved, nil
}

func (resolver *variableResolver) resolve(name string) (any, error) {
	if value, ok := resolver.resolved[name]; ok {
		return value, nil
	}
	value, ok := resolver.source[name]
	if !ok {
		return nil, fmt.Errorf("unknown blueprint variable %q", name)
	}
	if resolver.active[name] {
		cycle := append(append([]string{}, resolver.stack...), name)
		return nil, fmt.Errorf("blueprint variable cycle: %s", strings.Join(cycle, " -> "))
	}
	resolver.active[name] = true
	resolver.stack = append(resolver.stack, name)
	resolved, err := resolver.resolveValue(value)
	resolver.stack = resolver.stack[:len(resolver.stack)-1]
	delete(resolver.active, name)
	if err != nil {
		return nil, fmt.Errorf("environment.vars.%s: %w", name, err)
	}
	resolver.resolved[name] = resolved
	return resolved, nil
}

func (resolver *variableResolver) resolveValue(value any) (any, error) {
	switch typed := value.(type) {
	case string:
		return resolver.resolveString(typed)
	case []any:
		result := make([]any, len(typed))
		for index, element := range typed {
			resolved, err := resolver.resolveValue(element)
			if err != nil {
				return nil, fmt.Errorf("item %d: %w", index, err)
			}
			result[index] = resolved
		}
		return result, nil
	case map[string]any:
		result := make(map[string]any, len(typed))
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			resolved, err := resolver.resolveValue(typed[key])
			if err != nil {
				return nil, fmt.Errorf("field %s: %w", key, err)
			}
			result[key] = resolved
		}
		return result, nil
	default:
		return value, nil
	}
}

func (resolver *variableResolver) resolveString(value string) (any, error) {
	if match := wholeInterpolationPattern.FindStringSubmatch(value); match != nil && !strings.Contains(match[1], ".") {
		return resolver.resolve(match[1])
	}

	var interpolationErr error
	resolved := interpolationPattern.ReplaceAllStringFunc(value, func(token string) string {
		if interpolationErr != nil {
			return token
		}
		match := interpolationPattern.FindStringSubmatch(token)
		name := match[1]
		if strings.Contains(name, ".") {
			return token
		}
		replacement, err := resolver.resolve(name)
		if err != nil {
			interpolationErr = err
			return token
		}
		switch replacement.(type) {
		case []any, map[string]any:
			interpolationErr = fmt.Errorf("variable %q is not scalar and cannot be embedded in a string", name)
			return token
		default:
			return fmt.Sprint(replacement)
		}
	})
	if interpolationErr != nil {
		return nil, interpolationErr
	}
	return resolved, nil
}
