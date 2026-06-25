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
	if scheme != "file" {
		if index := packSubdirDelimiter(body); index >= 0 {
			source = body[:index]
			subdir = strings.TrimPrefix(body[index+2:], "/")
		}
	}
	if source == "" {
		return PackRef{}, fmt.Errorf("blueprint reference source must not be empty")
	}
	if scheme == "pypi" && subdir == "" {
		return PackRef{}, fmt.Errorf("pypi blueprint references require a package-internal path")
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

func isSupportedPackScheme(scheme string) bool {
	switch scheme {
	case "file", "git", "sl", "pypi":
		return true
	default:
		return false
	}
}

func packSubdirDelimiter(body string) int {
	offset := 0
	for {
		index := strings.Index(body[offset:], "//")
		if index < 0 {
			return -1
		}
		index += offset
		if index == 0 || body[index-1] != ':' {
			return index
		}
		offset = index + 2
	}
}

func packRefIsPinned(scheme string, source string, query url.Values) bool {
	switch scheme {
	case "file":
		return false
	case "pypi":
		return strings.Contains(source, "==")
	case "git":
		return query.Get("ref") != ""
	case "sl":
		return query.Get("rev") != ""
	default:
		return false
	}
}
