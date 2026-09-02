package providerstore

import (
	"fmt"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

// CanonicalSourceURLV1 returns the single catalog-and-lock spelling for a
// credential-free HTTPS source URL. Catalog loading and locked replay share
// this implementation so their accepted source evidence cannot diverge.
func CanonicalSourceURLV1(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Opaque != "" || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.ForceQuery || parsed.RawQuery != "" || parsed.Fragment != "" || strings.Contains(raw, "#") || parsed.Host != strings.ToLower(parsed.Host) || strings.HasSuffix(parsed.Hostname(), ".") || parsed.Port() == "443" || !canonicalSourceASCIIHostV1(parsed.Hostname()) || !canonicalSourcePercentEscapesV1(parsed.EscapedPath()) || canonicalSourceHasDotSegmentV1(parsed.Path) {
		return "", fmt.Errorf("source URL must be a canonical credential-free HTTPS URL without query or fragment")
	}
	port := parsed.Port()
	if strings.HasSuffix(parsed.Host, ":") || port != "" && !canonicalSourcePositivePortV1(port) {
		return "", fmt.Errorf("source URL must use a canonical authority")
	}
	if port != "" {
		parsedPort, err := strconv.ParseUint(port, 10, 16)
		if err != nil || parsedPort == 0 {
			return "", fmt.Errorf("source URL must use a canonical authority")
		}
	}
	host := parsed.Hostname()
	if address, err := netip.ParseAddr(host); err == nil {
		if address.Zone() != "" {
			return "", fmt.Errorf("source URL must use a canonical authority")
		}
		host = address.String()
		if address.Is6() {
			host = "[" + host + "]"
		}
	} else if strings.Contains(host, ":") || canonicalSourceNumericHostV1(host) {
		return "", fmt.Errorf("source URL must use a canonical authority")
	}
	if port != "" {
		host += ":" + port
	}
	escapedPath := parsed.EscapedPath()
	if escapedPath == "" {
		escapedPath = "/"
	}
	return "https://" + host + canonicalSourcePathV1(escapedPath), nil
}

func canonicalSourcePositivePortV1(value string) bool {
	if value == "" || value == "0" || len(value) > 1 && value[0] == '0' {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func canonicalSourceNumericHostV1(host string) bool {
	if host == "" {
		return false
	}
	for _, component := range strings.Split(host, ".") {
		if component == "" {
			return false
		}
		decimal := true
		for _, character := range component {
			if character < '0' || character > '9' {
				decimal = false
				break
			}
		}
		if decimal {
			continue
		}
		if len(component) <= 2 || !strings.HasPrefix(component, "0x") {
			return false
		}
		for _, character := range component[2:] {
			if character < '0' || character > '9' && (character < 'a' || character > 'f') {
				return false
			}
		}
	}
	return true
}

func canonicalSourcePathV1(escapedPath string) string {
	var normalized strings.Builder
	normalized.Grow(len(escapedPath))
	for index := 0; index < len(escapedPath); index++ {
		if escapedPath[index] != '%' {
			normalized.WriteByte(escapedPath[index])
			continue
		}
		value, err := strconv.ParseUint(escapedPath[index+1:index+3], 16, 8)
		if err != nil {
			normalized.WriteString(escapedPath[index:])
			break
		}
		character := byte(value)
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || strings.ContainsRune("-._~", rune(character)) {
			normalized.WriteByte(character)
		} else {
			normalized.WriteString(escapedPath[index : index+3])
		}
		index += 2
	}
	return normalized.String()
}

func canonicalSourceASCIIHostV1(host string) bool {
	for _, character := range host {
		if character > unicode.MaxASCII || unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func canonicalSourcePercentEscapesV1(value string) bool {
	for index := 0; index < len(value); index++ {
		if value[index] != '%' {
			continue
		}
		if index+2 >= len(value) || !canonicalSourceUppercaseHexV1(value[index+1]) || !canonicalSourceUppercaseHexV1(value[index+2]) {
			return false
		}
		index += 2
	}
	return true
}

func canonicalSourceUppercaseHexV1(value byte) bool {
	return value >= '0' && value <= '9' || value >= 'A' && value <= 'F'
}

func canonicalSourceHasDotSegmentV1(value string) bool {
	for _, segment := range strings.Split(value, "/") {
		if segment == "." || segment == ".." {
			return true
		}
	}
	return false
}
