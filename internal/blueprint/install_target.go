package blueprint

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

type HostOS string

const (
	HostLinux   HostOS = "linux"
	HostMacOS   HostOS = "macos"
	HostWindows HostOS = "windows"
)

type HostPaths struct {
	Home       string
	UserData   string
	LocalData  string
	SystemData string
}

type InstallTargetContext struct {
	Host      HostOS
	Scope     InstallScope
	Override  string
	Paths     HostPaths
	Variables map[string]any
}

var windowsAbsolutePathPattern = regexp.MustCompile(`^(?:[A-Za-z]:[\\/]|\\\\)`)

func resolveInstallTarget(target InstallTargetSyntax, environmentID string, context InstallTargetContext) (string, error) {
	return ResolveInstallTarget(InstallTarget{DefaultPath: target.DefaultPath, DefaultPaths: target.DefaultPaths}, environmentID, context)
}

// ResolveInstallTarget resolves the typed environment install target for a
// concrete host and install scope.
func ResolveInstallTarget(target InstallTarget, environmentID string, context InstallTargetContext) (string, error) {
	if err := validateInstallPlatform(context.Host, context.Scope); err != nil {
		return "", err
	}
	if err := validateInstallTargetKeys(target.DefaultPaths, context); err != nil {
		return "", err
	}

	candidate := strings.TrimSpace(context.Override)
	if candidate == "" {
		candidate = strings.TrimSpace(target.DefaultPaths[string(context.Scope)+"."+string(context.Host)])
	}
	if candidate == "" {
		candidate = strings.TrimSpace(target.DefaultPaths[string(context.Host)])
	}
	if candidate == "" {
		candidate = strings.TrimSpace(target.DefaultPath)
	}
	if candidate == "" {
		candidate = defaultInstallTarget(environmentID, context)
	}

	interpolation := newInterpolationContext(context.Variables, PhaseInstalled, &context.Scope)
	interpolation = interpolation.WithRoot("environment", map[string]any{"id": environmentID})
	interpolation = interpolation.WithRoot("user", map[string]any{
		"home":       context.Paths.Home,
		"data":       context.Paths.UserData,
		"local_data": context.Paths.LocalData,
	})
	interpolation = interpolation.WithRoot("system", map[string]any{"data": context.Paths.SystemData})
	interpolation = interpolation.WithReployValue("install_root", defaultInstallRoot(context))

	resolved, err := interpolate(candidate, interpolation)
	if err != nil {
		return "", fmt.Errorf("resolve install target: %w", err)
	}
	text, ok := resolved.(string)
	if !ok {
		return "", fmt.Errorf("resolve install target: expected string, got %T", resolved)
	}
	if strings.ContainsAny(text, "\r\n\t") {
		return "", fmt.Errorf("install target must not contain tabs or newlines")
	}
	if installTargetContainsTraversal(text) {
		return "", fmt.Errorf("install target must not contain parent-directory traversal: %s", text)
	}
	if !isAbsoluteHostPath(context.Host, text) {
		return "", fmt.Errorf("install target must resolve to an absolute %s path: %s", context.Host, text)
	}
	if context.Host == HostWindows {
		text = strings.ReplaceAll(text, "/", `\`)
	}
	return text, nil
}

func installTargetContainsTraversal(value string) bool {
	for _, part := range strings.FieldsFunc(value, func(char rune) bool { return char == '/' || char == '\\' }) {
		if part == ".." {
			return true
		}
	}
	return false
}

func validateInstallPlatform(host HostOS, scope InstallScope) error {
	switch host {
	case HostLinux:
		if scope == InstallScopeUser || scope == InstallScopeSystem {
			return nil
		}
	case HostMacOS, HostWindows:
		if scope == InstallScopeUser {
			return nil
		}
		if scope == InstallScopeSystem {
			return fmt.Errorf("%s system install scope is not supported", host)
		}
	default:
		return fmt.Errorf("unsupported host OS %q", host)
	}
	return fmt.Errorf("unsupported install scope %q for %s", scope, host)
}

func validateInstallTargetKeys(values map[string]string, context InstallTargetContext) error {
	for key, value := range values {
		parts := strings.Split(key, ".")
		valid := false
		switch len(parts) {
		case 1:
			valid = validHostOS(HostOS(parts[0]))
		case 2:
			valid = (parts[0] == string(InstallScopeUser) || parts[0] == string(InstallScopeSystem)) && validHostOS(HostOS(parts[1]))
		}
		if !valid {
			return fmt.Errorf("environment.install.target.default_paths contains unknown key %q", key)
		}
		if strings.ContainsAny(value, "\r\n\t") {
			return fmt.Errorf("environment.install.target.default_paths.%s must not contain tabs or newlines", key)
		}
		if err := validateKnownInterpolation(value, context.Variables); err != nil {
			return fmt.Errorf("environment.install.target.default_paths.%s: %w", key, err)
		}
	}
	return nil
}

func validateKnownInterpolation(value string, variables map[string]any) error {
	for _, match := range interpolationPattern.FindAllStringSubmatch(value, -1) {
		reference := match[1]
		root, _, dotted := strings.Cut(reference, ".")
		if dotted {
			if !reservedVariableRoots[root] {
				return fmt.Errorf("unknown interpolation root %q", root)
			}
			continue
		}
		if _, ok := variables[reference]; !ok {
			return fmt.Errorf("unknown blueprint variable %q", reference)
		}
	}
	return nil
}

func validHostOS(host HostOS) bool {
	return host == HostLinux || host == HostMacOS || host == HostWindows
}

func defaultInstallTarget(environmentID string, context InstallTargetContext) string {
	return path.Join(defaultInstallRoot(context), environmentID)
}

func defaultInstallRoot(context InstallTargetContext) string {
	if context.Host == HostLinux && context.Scope == InstallScopeSystem {
		return "/opt"
	}
	if context.Host == HostWindows {
		return path.Join(strings.ReplaceAll(context.Paths.LocalData, `\`, "/"), "Reploy", "installs")
	}
	return path.Join(context.Paths.UserData, "Reploy", "installs")
}

func isAbsoluteHostPath(host HostOS, value string) bool {
	if host == HostWindows {
		return windowsAbsolutePathPattern.MatchString(value)
	}
	return strings.HasPrefix(value, "/")
}
