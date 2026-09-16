package python

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	pep440 "github.com/aquasecurity/go-pep440-version"
)

// wheelRequirementDistributionName validates the complete PEP 508 requirement
// syntax needed in wheel metadata, without evaluating its marker.
func wheelRequirementDistributionName(requirement string) (string, error) {
	for i := 0; i < len(requirement); i++ {
		if requirement[i] >= 0x7f || requirement[i] < 0x20 && requirement[i] != '\t' {
			return "", fmt.Errorf("requirement contains invalid character")
		}
	}
	value := strings.TrimSpace(requirement)
	if value == "" {
		return "", fmt.Errorf("empty requirement")
	}
	body, marker, err := splitWheelRequirementMarker(value)
	if err != nil {
		return "", err
	}
	name := requirementNamePattern.FindString(body)
	if name == "" || !wheelCoreNamePattern.MatchString(name) {
		return "", fmt.Errorf("invalid distribution name")
	}
	remainder := strings.TrimSpace(strings.TrimPrefix(body, name))
	if strings.HasPrefix(remainder, "[") {
		end := strings.IndexByte(remainder, ']')
		if end < 0 {
			return "", fmt.Errorf("unclosed extras")
		}
		if err := validateWheelRequirementExtras(remainder[1:end]); err != nil {
			return "", err
		}
		remainder = strings.TrimSpace(remainder[end+1:])
	}
	if remainder != "" {
		if strings.HasPrefix(remainder, "@") {
			if err := validateWheelRequirementURL(strings.TrimSpace(remainder[1:])); err != nil {
				return "", err
			}
		} else {
			if _, err := normalizeWheelVersionSpecifiers(remainder); err != nil {
				return "", fmt.Errorf("invalid version specifier: %w", err)
			}
		}
	}
	if marker != "" {
		if err := parseWheelMarker(marker); err != nil {
			return "", err
		}
	}
	return NormalizeDistributionName(name), nil
}

var wheelRequirementSpecifierPattern = regexp.MustCompile(`^(~=|==|!=|<=|>=|<|>)[ \t]*([A-Za-z0-9._*+!-]+)$`)
var wheelWildcardReleasePattern = regexp.MustCompile(`^[0-9]+(?:![0-9]+)?(?:\.[0-9]+)*$`)

// Normalize ordinary metadata before using the PEP 440 specifier library,
// whose specifier lexer rejects case variants accepted by its version parser.
// Keep the anchored grammar check: the library also accepts nonstandard forms.
func normalizeWheelVersionSpecifiers(value string) (string, error) {
	value = strings.Trim(value, " \t")
	if strings.HasPrefix(value, "(") && strings.HasSuffix(value, ")") {
		value = value[1 : len(value)-1]
	}
	parts := strings.Split(value, ",")
	normalized := make([]string, 0, len(parts))
	for index, part := range parts {
		part = strings.Trim(part, " \t")
		if part == "" && index > 0 && index == len(parts)-1 {
			continue // PEP 508 permits one trailing comma.
		}
		if arbitrary, found := strings.CutPrefix(part, "==="); found {
			// PyPA's arbitrary-equality grammar permits any ASCII string,
			// including the empty string, except whitespace and delimiters.
			arbitrary = strings.Trim(arbitrary, " \t")
			for i := 0; i < len(arbitrary); i++ {
				if arbitrary[i] <= ' ' || arbitrary[i] >= 0x7f || arbitrary[i] == ';' || arbitrary[i] == ')' {
					return "", fmt.Errorf("invalid arbitrary version comparison")
				}
			}
			// Equality is case-insensitive string matching, so retain release
			// numbers, aliases, and optional v prefixes without PEP 440 parsing.
			normalized = append(normalized, "==="+strings.ToLower(arbitrary))
			continue
		}
		match := wheelRequirementSpecifierPattern.FindStringSubmatch(part)
		if match == nil {
			return "", fmt.Errorf("invalid version comparison")
		}
		operator, versionText := match[1], match[2]
		base, wildcard := strings.CutSuffix(versionText, ".*")
		version, err := pep440.Parse(base)
		if err != nil {
			return "", err
		}
		versionText = version.String()
		if wildcard {
			if (operator != "==" && operator != "!=") || !wheelWildcardReleasePattern.MatchString(versionText) {
				return "", fmt.Errorf("invalid wildcard comparison")
			}
			versionText += ".*"
		}
		if _, err := pep440.NewSpecifiers(operator + versionText); err != nil {
			return "", err
		}
		normalized = append(normalized, operator+versionText)
	}
	return strings.Join(normalized, ","), nil
}

func splitWheelRequirementMarker(value string) (string, string, error) {
	// Reuse the ordinary requirement boundary, including whitespace-sensitive
	// URL termination, then validate the complete suffix rather than discard it.
	body := portableToolPythonRequirementBodyV1(value)
	if body == value {
		return value, "", nil
	}
	suffix := strings.TrimSpace(value[len(body):])
	marker, found := strings.CutPrefix(suffix, ";")
	marker = strings.TrimSpace(marker)
	if body == "" || !found || marker == "" {
		return "", "", fmt.Errorf("invalid environment marker")
	}
	return body, marker, nil
}

func validateWheelRequirementExtras(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	for _, extra := range strings.Split(value, ",") {
		extra = strings.TrimSpace(extra)
		if !wheelCoreNamePattern.MatchString(extra) {
			return fmt.Errorf("invalid extra %q", extra)
		}
	}
	return nil
}

func validateWheelRequirementURL(value string) error {
	if value == "" {
		return fmt.Errorf("invalid direct URL")
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c == '%' {
			if i+2 >= len(value) || !isHex(value[i+1]) || !isHex(value[i+2]) {
				return fmt.Errorf("invalid direct URL")
			}
			i += 2
			continue
		}
		if c >= 0x80 || !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~:/?#[]@!$&'()*+,;=", rune(c)) {
			return fmt.Errorf("invalid direct URL")
		}
	}
	if _, err := url.Parse(value); err != nil {
		return fmt.Errorf("invalid direct URL")
	}
	return nil
}

func isHex(value byte) bool {
	return value >= '0' && value <= '9' || value >= 'a' && value <= 'f' || value >= 'A' && value <= 'F'
}

// Marker names and precedence follow the PyPA dependency specifier grammar:
// https://packaging.python.org/en/latest/specifications/dependency-specifiers/
var wheelMarkerVariables = map[string]bool{
	"python_version": true, "python_full_version": true, "os_name": true,
	"sys_platform": true, "platform_release": true, "platform_system": true,
	"platform_version": true, "platform_machine": true,
	"platform_python_implementation": true, "implementation_name": true,
	"implementation_version": true, "extra": true,
}

// Keep the exact legacy spellings recognized by PyPA packaging. Other dotted
// names (such as platform.release) are not valid aliases.
var wheelMarkerAliases = map[string]string{
	"os.name":                        "os_name",
	"sys.platform":                   "sys_platform",
	"platform.version":               "platform_version",
	"platform.machine":               "platform_machine",
	"platform.python_implementation": "platform_python_implementation",
	"python_implementation":          "platform_python_implementation",
}

type wheelMarkerParser struct {
	tokens []string
	pos    int
}

func parseWheelMarker(value string) error {
	p := &wheelMarkerParser{tokens: tokenizeWheelMarker(value)}
	if p.tokens == nil || len(p.tokens) > 4096 {
		return fmt.Errorf("invalid environment marker")
	}
	if !p.parseOr(0) || p.pos != len(p.tokens) {
		return fmt.Errorf("invalid environment marker")
	}
	return nil
}

func tokenizeWheelMarker(value string) []string {
	var out []string
	for i := 0; i < len(value); {
		if len(out) >= 4096 {
			return nil
		}
		if value[i] == '\n' || value[i] == '\r' || value[i] < 0x20 && value[i] != ' ' && value[i] != '\t' {
			return nil
		}
		if value[i] == ' ' || value[i] == '\t' {
			i++
			continue
		}
		if strings.ContainsRune("()", rune(value[i])) {
			out = append(out, value[i:i+1])
			i++
			continue
		}
		if value[i] == '\'' || value[i] == '"' {
			quote := value[i]
			start := i
			i++
			for i < len(value) && value[i] != quote {
				if value[i] < 0x20 && value[i] != '\t' || value[i] >= 0x7f || value[i] == '\\' {
					return nil
				}
				i++
			}
			if i >= len(value) {
				return nil
			}
			i++
			out = append(out, value[start:i])
			continue
		}
		if strings.ContainsRune("<>=!~", rune(value[i])) {
			start := i
			i++
			for i < len(value) && strings.ContainsRune("<>=", rune(value[i])) {
				i++
			}
			out = append(out, value[start:i])
			continue
		}
		start := i
		for i < len(value) && value[i] != ' ' && value[i] != '\t' && !strings.ContainsRune("()<>=!~'\"", rune(value[i])) {
			i++
		}
		word := value[start:i]
		if canonical, found := wheelMarkerAliases[word]; found {
			word = canonical
		}
		out = append(out, word)
	}
	return out
}

func (p *wheelMarkerParser) parseOr(depth int) bool {
	if !p.parseAnd(depth) {
		return false
	}
	for p.take("or") {
		if !p.parseAnd(depth) {
			return false
		}
	}
	return true
}
func (p *wheelMarkerParser) parseAnd(depth int) bool {
	if !p.parseFactor(depth) {
		return false
	}
	for p.take("and") {
		if !p.parseFactor(depth) {
			return false
		}
	}
	return true
}
func (p *wheelMarkerParser) parseFactor(depth int) bool {
	if p.take("(") {
		if depth >= 64 {
			return false
		}
		ok := p.parseOr(depth+1) && p.take(")")
		return ok
	}
	if p.pos >= len(p.tokens) || (!wheelMarkerVariables[p.tokens[p.pos]] && !isWheelMarkerString(p.tokens[p.pos])) {
		return false
	}
	p.pos++
	if p.pos >= len(p.tokens) {
		return false
	}
	op := p.tokens[p.pos]
	p.pos++
	if op == "not" {
		if p.pos >= len(p.tokens) || p.tokens[p.pos] != "in" {
			return false
		}
		p.pos++
	} else if op != "in" && op != "==" && op != "!=" && op != "<" && op != "<=" && op != ">" && op != ">=" && op != "~=" && op != "===" {
		return false
	}
	if p.pos >= len(p.tokens) {
		return false
	}
	value := p.tokens[p.pos]
	p.pos++
	return wheelMarkerVariables[value] || isWheelMarkerString(value)
}

func isWheelMarkerString(value string) bool {
	return len(value) >= 2 && (value[0] == '\'' || value[0] == '"') && value[len(value)-1] == value[0]
}
func (p *wheelMarkerParser) take(value string) bool {
	if p.pos < len(p.tokens) && p.tokens[p.pos] == value {
		p.pos++
		return true
	}
	return false
}
