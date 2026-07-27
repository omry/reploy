package providers

import (
	"fmt"
	"sort"

	"github.com/omry/reploy/internal/blueprint"
)

// ApplicationOption is the provider-neutral projection used by the retained
// bundle options/add/remove UX.
type ApplicationOption struct {
	Name        string
	Application string
	Description string
}

func ApplicationOptions(document blueprint.Document) []ApplicationOption {
	result := []ApplicationOption{}
	for applicationName, application := range document.Environment.Applications {
		for name, option := range application.Options {
			result = append(result, ApplicationOption{
				Name: name, Application: applicationName, Description: option.Description,
			})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name != result[j].Name {
			return result[i].Name < result[j].Name
		}
		return result[i].Application < result[j].Application
	})
	return result
}

// SelectApplicationOptions validates a complete selection update and returns stable
// state. It does not mutate the input when any requested name is invalid.
func SelectApplicationOptions(document blueprint.Document, selected []string, add []string, remove []string) ([]string, error) {
	options := map[string]int{}
	for _, option := range ApplicationOptions(document) {
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
			return nil, fmt.Errorf("unknown application option %q", name)
		}
		if options[name] > 1 {
			return nil, fmt.Errorf("application option %q is ambiguous across applications", name)
		}
		next[name] = true
	}
	for _, name := range remove {
		if options[name] == 0 {
			return nil, fmt.Errorf("unknown application option %q", name)
		}
		if options[name] > 1 {
			return nil, fmt.Errorf("application option %q is ambiguous across applications", name)
		}
		if !next[name] {
			return nil, fmt.Errorf("application option is not selected: %s", name)
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
