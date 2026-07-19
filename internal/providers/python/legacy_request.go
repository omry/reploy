package python

import "sort"

// LegacyResolveRequest is the aggregate input used by the retained Python
// wheelhouse path while deployment construction moves to provider graph nodes.
// New provider implementations must use providers.ResolveInput instead.
type LegacyResolveRequest struct {
	Platform     string
	BaseImage    string
	Components   []LegacyComponent
	Translations []LegacyTranslation
	DirectRoots  []string
	Executables  []LegacyExecutableRequest
}

type LegacyComponent struct {
	Name         string
	Requirements []string
}

type LegacyTranslation struct {
	Name     string
	Root     string
	Mappings map[string]string
}

type LegacyExecutableRequest struct {
	Name      string
	Component string
	Binary    string
}

func normalizeLegacyResolveRequest(request LegacyResolveRequest) LegacyResolveRequest {
	sort.Slice(request.Components, func(i, j int) bool { return request.Components[i].Name < request.Components[j].Name })
	sort.Slice(request.Translations, func(i, j int) bool { return request.Translations[i].Name < request.Translations[j].Name })
	sort.Strings(request.DirectRoots)
	sort.Slice(request.Executables, func(i, j int) bool { return request.Executables[i].Name < request.Executables[j].Name })
	return request
}
