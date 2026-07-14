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
	Type        blueprint.ComponentType
	Group       string
	Description string
}

func ComponentOptions(document blueprint.Document) []ComponentOption {
	result := []ComponentOption{}
	for name, component := range document.Environment.Components {
		if component.Optional == nil {
			continue
		}
		result = append(result, ComponentOption{
			Name: name, Type: component.Type, Group: component.Optional.Group,
			Description: component.Optional.Description,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

// SelectComponents validates a complete selection update and returns stable
// state. It does not mutate the input when any requested name is invalid.
func SelectComponents(document blueprint.Document, selected []string, add []string, remove []string) ([]string, error) {
	options := map[string]bool{}
	for _, option := range ComponentOptions(document) {
		options[option.Name] = true
	}
	next := map[string]bool{}
	for _, name := range selected {
		if options[name] {
			next[name] = true
		}
	}
	for _, name := range add {
		if !options[name] {
			return nil, fmt.Errorf("unknown optional component %q", name)
		}
		next[name] = true
	}
	for _, name := range remove {
		if !options[name] {
			return nil, fmt.Errorf("unknown optional component %q", name)
		}
		if !next[name] {
			return nil, fmt.Errorf("optional component is not selected: %s", name)
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
