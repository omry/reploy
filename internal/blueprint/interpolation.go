package blueprint

import (
	"fmt"
	"sort"
	"strings"
)

type InterpolationContext struct {
	Variables map[string]any
	Roots     map[string]any
}

// ResolveOperationStrings performs the final lazy interpolation for values
// consumed by a concrete staged or installed operation.
func ResolveOperationStrings(values []string, variables map[string]any, phase Phase, scope *InstallScope, roots map[string]any) ([]string, error) {
	context := newInterpolationContext(variables, phase, scope)
	for name, value := range roots {
		if name == "reploy" {
			continue
		}
		context = context.WithRoot(name, value)
	}
	if workload, ok := roots["reploy.workload"]; ok {
		context = context.WithReployValue("workload", workload)
	}
	result := make([]string, len(values))
	for index, value := range values {
		resolved, err := interpolateString(value, context, 0)
		if err != nil {
			return nil, fmt.Errorf("item %d: %w", index, err)
		}
		switch resolved.(type) {
		case []any, []string, map[string]any:
			return nil, fmt.Errorf("item %d resolved to %T, expected scalar", index, resolved)
		default:
			result[index] = fmt.Sprint(resolved)
		}
	}
	return result, nil
}

func newInterpolationContext(variables map[string]any, phase Phase, scope *InstallScope) InterpolationContext {
	reploy := map[string]any{"phase": string(phase)}
	if scope != nil {
		reploy["scope"] = string(*scope)
	}
	return InterpolationContext{
		Variables: variables,
		Roots:     map[string]any{"reploy": reploy},
	}
}

func (context InterpolationContext) WithRoot(name string, value any) InterpolationContext {
	roots := make(map[string]any, len(context.Roots)+1)
	for key, root := range context.Roots {
		roots[key] = root
	}
	roots[name] = value
	context.Roots = roots
	return context
}

func (context InterpolationContext) WithReployValue(name string, value any) InterpolationContext {
	roots := make(map[string]any, len(context.Roots))
	for key, root := range context.Roots {
		roots[key] = root
	}
	reploy := map[string]any{}
	if current, ok := roots["reploy"].(map[string]any); ok {
		for key, item := range current {
			reploy[key] = item
		}
	}
	reploy[name] = value
	roots["reploy"] = reploy
	context.Roots = roots
	return context
}

func interpolate(value any, context InterpolationContext) (any, error) {
	return interpolateDepth(value, context, 0)
}

func interpolateDepth(value any, context InterpolationContext, depth int) (any, error) {
	if depth > 100 {
		return nil, fmt.Errorf("interpolation exceeded maximum depth")
	}
	switch typed := value.(type) {
	case string:
		return interpolateString(typed, context, depth)
	case []string:
		result := make([]string, len(typed))
		for index, element := range typed {
			resolved, err := interpolateString(element, context, depth+1)
			if err != nil {
				return nil, fmt.Errorf("item %d: %w", index, err)
			}
			text, ok := resolved.(string)
			if !ok {
				return nil, fmt.Errorf("item %d resolved to %T, expected string", index, resolved)
			}
			result[index] = text
		}
		return result, nil
	case []any:
		result := make([]any, len(typed))
		for index, element := range typed {
			resolved, err := interpolateDepth(element, context, depth+1)
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
			resolved, err := interpolateDepth(typed[key], context, depth+1)
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

func interpolateString(value string, context InterpolationContext, depth int) (any, error) {
	if match := wholeInterpolationPattern.FindStringSubmatch(value); match != nil {
		resolved, err := context.lookup(match[1])
		if err != nil {
			return nil, err
		}
		return interpolateDepth(resolved, context, depth+1)
	}

	var interpolationErr error
	resolved := interpolationPattern.ReplaceAllStringFunc(value, func(token string) string {
		if interpolationErr != nil {
			return token
		}
		match := interpolationPattern.FindStringSubmatch(token)
		replacement, err := context.lookup(match[1])
		if err != nil {
			interpolationErr = err
			return token
		}
		replacement, err = interpolateDepth(replacement, context, depth+1)
		if err != nil {
			interpolationErr = err
			return token
		}
		switch replacement.(type) {
		case []any, []string, map[string]any:
			interpolationErr = fmt.Errorf("reference %q is not scalar and cannot be embedded in a string", match[1])
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

func (context InterpolationContext) lookup(reference string) (any, error) {
	parts := strings.Split(reference, ".")
	if len(parts) == 1 {
		value, ok := context.Variables[reference]
		if !ok {
			return nil, fmt.Errorf("unknown blueprint variable %q", reference)
		}
		return value, nil
	}
	value, ok := context.Roots[parts[0]]
	if !ok {
		if reservedVariableRoots[parts[0]] {
			return nil, fmt.Errorf("namespace %q is unavailable in this operation", parts[0])
		}
		return nil, fmt.Errorf("unknown interpolation root %q", parts[0])
	}
	for _, part := range parts[1:] {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("interpolation reference %q cannot select %q from %T", reference, part, value)
		}
		value, ok = object[part]
		if !ok {
			return nil, fmt.Errorf("interpolation reference %q is unavailable", reference)
		}
	}
	return value, nil
}
