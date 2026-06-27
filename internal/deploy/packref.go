package deploy

import (
	"fmt"
	"net/url"
	"strings"
)

type PackRef struct {
	Raw      string     `json:"raw"`
	Scheme   string     `json:"scheme"`
	Source   string     `json:"source"`
	Subdir   string     `json:"subdir,omitempty"`
	Query    url.Values `json:"query,omitempty"`
	IsPinned bool       `json:"is_pinned"`
}

func ParsePackRef(raw string) (PackRef, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return PackRef{}, fmt.Errorf("blueprint reference must not be empty")
	}

	scheme, rest, ok := strings.Cut(raw, ":")
	if !ok || scheme == "" || rest == "" {
		return PackRef{}, fmt.Errorf("blueprint reference must use scheme:value syntax")
	}
	if !isSupportedPackScheme(scheme) {
		return PackRef{}, fmt.Errorf("unsupported blueprint reference scheme: %s", scheme)
	}

	body, rawQuery, _ := strings.Cut(rest, "?")
	if body == "" {
		return PackRef{}, fmt.Errorf("blueprint reference source must not be empty")
	}

	source := body
	subdir := ""
	if scheme == "pypi" {
		if strings.Contains(body, "//") {
			return PackRef{}, fmt.Errorf("pypi blueprint paths use #PATH, not //PATH")
		}
		if splitSource, path, hasPath := strings.Cut(body, "#"); hasPath {
			source = splitSource
			subdir = strings.TrimPrefix(path, "/")
			if subdir == "" {
				return PackRef{}, fmt.Errorf("pypi blueprint path must not be empty")
			}
		}
	}
	if source == "" {
		return PackRef{}, fmt.Errorf("blueprint reference source must not be empty")
	}
	if scheme == "pypi" && subdir == "" {
		packageName, _, err := parsePyPISource(source)
		if err != nil {
			return PackRef{}, err
		}
		subdir = defaultPyPIBlueprintSubdir(packageName)
	}

	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return PackRef{}, fmt.Errorf("invalid blueprint reference query: %w", err)
	}

	return PackRef{
		Raw:      raw,
		Scheme:   scheme,
		Source:   source,
		Subdir:   subdir,
		Query:    query,
		IsPinned: packRefIsPinned(scheme, source, query),
	}, nil
}

func defaultPyPIBlueprintSubdir(packageName string) string {
	moduleName := strings.ReplaceAll(normalizePackageName(packageName), "-", "_")
	return moduleName + "/reploy"
}

func isSupportedPackScheme(scheme string) bool {
	switch scheme {
	case "file", "pypi":
		return true
	default:
		return false
	}
}

func packRefIsPinned(scheme string, source string, query url.Values) bool {
	switch scheme {
	case "file":
		return false
	case "pypi":
		return strings.Contains(source, "==")
	default:
		return false
	}
}
