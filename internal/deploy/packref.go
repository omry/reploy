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
	if scheme == "pypi" && strings.HasPrefix(rest, "//") {
		return parsePyPIURLPackRef(raw)
	}

	body, rawQuery, _ := strings.Cut(rest, "?")
	if body == "" {
		return PackRef{}, fmt.Errorf("blueprint reference source must not be empty")
	}

	source := body
	subdir := ""
	if scheme == "pypi" {
		if strings.Contains(body, "//") {
			return PackRef{}, fmt.Errorf("%s blueprint paths use #PATH, not //PATH", scheme)
		}
	}
	if scheme == "pypi" || scheme == "source" || scheme == "git" {
		if splitSource, path, hasPath := strings.Cut(body, "#"); hasPath {
			source = splitSource
			subdir = strings.TrimPrefix(path, "/")
			if subdir == "" {
				return PackRef{}, fmt.Errorf("%s blueprint path must not be empty", scheme)
			}
		}
	}
	if source == "" {
		return PackRef{}, fmt.Errorf("blueprint reference source must not be empty")
	}
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return PackRef{}, fmt.Errorf("invalid blueprint reference query: %w", err)
	}
	if scheme == "pypi" {
		if _, _, err := parsePyPISource(source); err != nil {
			return PackRef{}, err
		}
		if subdir == "" {
			return PackRef{}, fmt.Errorf("pypi blueprint refs must include an explicit blueprint file path")
		}
		if !isBlueprintManifestPath(subdir) {
			return PackRef{}, fmt.Errorf("pypi blueprint path must point to a %s file: %s", BlueprintManifestGlob, subdir)
		}
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

func parsePyPIURLPackRef(raw string) (PackRef, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return PackRef{}, fmt.Errorf("invalid pypi blueprint reference: %w", err)
	}
	if parsed.Host == "" {
		return PackRef{}, fmt.Errorf("pypi blueprint reference package must not be empty")
	}
	blueprintPath := strings.TrimPrefix(parsed.Path, "/")
	if blueprintPath == "" {
		return PackRef{}, fmt.Errorf("pypi blueprint refs must include an explicit blueprint file path")
	}
	if !isBlueprintManifestPath(blueprintPath) {
		return PackRef{}, fmt.Errorf("pypi blueprint path must point to a %s file: %s", BlueprintManifestGlob, blueprintPath)
	}
	query := parsed.Query()
	versions := query["version"]
	if len(versions) > 1 {
		return PackRef{}, fmt.Errorf("pypi blueprint version query must be specified at most once")
	}
	source := parsed.Host
	if len(versions) == 1 && strings.TrimSpace(versions[0]) == "" {
		return PackRef{}, fmt.Errorf("invalid pypi package version: %s", parsed.Host+"==")
	}
	if len(versions) == 1 && versions[0] != "latest" {
		source += "==" + versions[0]
	}
	if _, _, err := parsePyPISource(source); err != nil {
		return PackRef{}, err
	}
	query.Del("version")
	return PackRef{
		Raw:      raw,
		Scheme:   "pypi",
		Source:   source,
		Subdir:   blueprintPath,
		Query:    query,
		IsPinned: packRefIsPinned("pypi", source, query),
	}, nil
}

func defaultSourceBlueprintSubdir(projectName string) string {
	moduleName := strings.ReplaceAll(normalizePackageName(projectName), "-", "_")
	return moduleName + "/reploy"
}

func isSupportedPackScheme(scheme string) bool {
	switch scheme {
	case "file", "pypi", "source", "git":
		return true
	default:
		return false
	}
}

func packRefIsPinned(scheme string, source string, query url.Values) bool {
	switch scheme {
	case "file", "source":
		return false
	case "pypi":
		return strings.Contains(source, "==")
	case "git":
		return isFullGitHash(query.Get("ref"))
	default:
		return false
	}
}
