package providers

import (
	"fmt"
	"sort"

	"github.com/omry/reploy/internal/blueprint"
)

// ComponentOption is the provider-neutral projection used by the retained
// bundle options/add/remove UX.
type ComponentOption struct {
	Name        string
	Component   string
	Type        blueprint.ComponentType
	Description string
}

func ComponentOptions(document blueprint.Document) []ComponentOption {
	result := []ComponentOption{}
	for componentName, component := range document.Environment.Components {
		for name, option := range component.Options {
			result = append(result, ComponentOption{
				Name: name, Component: componentName, Type: component.Type,
				Description: option.Description,
			})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name != result[j].Name {
			return result[i].Name < result[j].Name
		}
		return result[i].Component < result[j].Component
	})
	return result
}

// SelectComponents validates a complete selection update and returns stable
// state. It does not mutate the input when any requested name is invalid.
func SelectComponents(document blueprint.Document, selected []string, add []string, remove []string) ([]string, error) {
	options := map[string]int{}
	for _, option := range ComponentOptions(document) {
		options[option.Name]++
	}
	next := map[string]bool{}
	for _, name := range selected {
		if options[name] == 1 {
			next[name] = true
		}
	}
	for _, name := range add {
		if options[name] == 0 {
			return nil, fmt.Errorf("unknown component option %q", name)
		}
		if options[name] > 1 {
			return nil, fmt.Errorf("component option %q is ambiguous across components", name)
		}
		next[name] = true
	}
	for _, name := range remove {
		if options[name] == 0 {
			return nil, fmt.Errorf("unknown component option %q", name)
		}
		if options[name] > 1 {
			return nil, fmt.Errorf("component option %q is ambiguous across components", name)
		}
		if !next[name] {
			return nil, fmt.Errorf("component option is not selected: %s", name)
		}
		delete(next, name)
	}
	result := make([]string, 0, len(next))
	for name := range next {
		result = append(result, name)
	}
	sort.Strings(result)
	return result, nil
}
