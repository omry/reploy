package deploy

import (
	"bufio"
	"bytes"
	"strconv"
	"strings"
	"unicode/utf8"
)

type imageAccountV1 struct{ uid, gid string }
type imageGroupV1 struct {
	gid     string
	members map[string]bool
}

func imageAccountIDV1(value string) bool {
	n, err := strconv.ParseUint(value, 10, 32)
	return err == nil && n < 4294967295 && strconv.FormatUint(n, 10) == value
}

func imageAccountNameV1(name string) bool {
	if name == "" || strings.HasPrefix(name, "+") || strings.HasPrefix(name, "-") || !utf8.ValidString(name) {
		return false
	}
	for _, character := range name {
		if character <= ' ' || character == 127 || character == ':' {
			return false
		}
	}
	return true
}

func imageAccountLinesV1(content []byte, visit func(string) bool) bool {
	scanner := bufio.NewScanner(bytes.NewReader(content))
	// ScanLines removes CR. A Linux account source must not acquire a second
	// interpretation through that convenience, so reject it before scanning.
	if bytes.IndexByte(content, '\r') >= 0 {
		return false
	}
	scanner.Buffer(make([]byte, 4096), imageIdentityLineLimitV1+2)
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) > imageIdentityLineLimitV1 {
			return false
		}
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") && !visit(line) {
			return false
		}
	}
	return scanner.Err() == nil
}

func parseImageAccountsV1(files map[string][]byte) (map[string]imageAccountV1, map[string]imageGroupV1, error) {
	users := map[string]imageAccountV1{}
	groups := map[string]imageGroupV1{}
	uids, gids := map[string]bool{}, map[string]bool{}
	fail := func() (map[string]imageAccountV1, map[string]imageGroupV1, error) {
		return nil, nil, imageIdentityErrorV1("invalid account sources")
	}
	if !imageAccountLinesV1(files["etc/passwd"], func(line string) bool {
		fields := strings.Split(line, ":")
		if len(fields) != 7 || !imageAccountNameV1(fields[0]) || !imageAccountIDV1(fields[2]) || !imageAccountIDV1(fields[3]) || uids[fields[2]] {
			return false
		}
		if _, exists := users[fields[0]]; exists {
			return false
		}
		uids[fields[2]] = true
		users[fields[0]] = imageAccountV1{fields[2], fields[3]}
		return true
	}) {
		return fail()
	}
	if !imageAccountLinesV1(files["etc/group"], func(line string) bool {
		fields := strings.Split(line, ":")
		if len(fields) != 4 || !imageAccountNameV1(fields[0]) || !imageAccountIDV1(fields[2]) || gids[fields[2]] {
			return false
		}
		if _, exists := groups[fields[0]]; exists {
			return false
		}
		members := map[string]bool{}
		if fields[3] != "" {
			for _, name := range strings.Split(fields[3], ",") {
				if !imageAccountNameV1(name) || members[name] {
					return false
				}
				if _, exists := users[name]; !exists {
					return false
				}
				members[name] = true
			}
		}
		gids[fields[2]] = true
		groups[fields[0]] = imageGroupV1{fields[2], members}
		return true
	}) {
		return fail()
	}
	for _, user := range users {
		if !gids[user.gid] {
			return fail()
		}
	}
	seen := map[string]bool{}
	continued := false
	if !imageAccountLinesV1(files["etc/nsswitch.conf"], func(line string) bool {
		// NSS tokens are literal bytes. Unicode trimming would turn a different
		// database or provider name into an accepted account source.
		const separators = " \t\v\f"
		line, _, _ = strings.Cut(line, "#")
		previousContinued := continued
		continued = strings.HasSuffix(strings.TrimRight(line, separators), "\\")
		line = strings.TrimLeft(line, separators)
		end := strings.IndexAny(line, ":"+separators)
		if end < 0 {
			end = len(line)
		}
		key := line[:end]
		if key != "passwd" && key != "group" {
			return true
		}
		// Identify the database before requiring the colon. Otherwise a later
		// colonless "passwd sss" or "group ldap" override could be ignored.
		rest := strings.TrimLeft(line[end:], separators)
		if !strings.HasPrefix(rest, ":") || previousContinued || seen[key] || strings.Trim(rest[1:], separators) != "files" {
			return false
		}
		seen[key] = true
		return true
	}) || !seen["passwd"] || !seen["group"] {
		return fail()
	}
	return users, groups, nil
}
