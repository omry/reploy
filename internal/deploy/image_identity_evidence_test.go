package deploy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/runtimeidentity"
)

func imageIdentityFixtureV1(t *testing.T) (*blueprint.IdentityPolicyV1, providers.RealizedImageV1, blueprint.Platform, map[string][]byte) {
	t.Helper()
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	policy := &blueprint.IdentityPolicyV1{
		Schema: blueprint.IdentityPolicySchemaV1,
		Groups: map[string]string{"worker": "200", "zeta": "20", "shared": "300", "low": "40", "unused": "450"},
		Users: map[string]blueprint.IdentityPolicyUserV1{
			"worker": {UID: "200", PrimaryGroup: "worker", SupplementaryGroups: []string{"low", "shared"}},
			"zeta":   {UID: "20", PrimaryGroup: "zeta", SupplementaryGroups: []string{}},
		},
		Roles: map[string]blueprint.IdentityPolicyRoleV1{"app": {User: "worker", Capabilities: []string{}}},
	}
	files := map[string][]byte{
		"etc/passwd":        []byte("# accounts\nroot:SECRET:0:0:private:/root:/bin/sh\nworker:SECRET:200:200:private:/home/worker:/bin/sh\nzeta:x:20:20::/:/bin/false\nBuild.User:x:900:900::/:/bin/false\n"),
		"etc/group":         []byte("\nroot:SECRET:0:\nworker:x:200:worker\nzeta:x:20:\nshared:SECRET:300:worker\nlow:x:40:worker\nunused:x:450:\nBuild.Group:x:900:Build.User\n"),
		"etc/nsswitch.conf": []byte("# identity databases\npasswd: files # local only\ngroup:\tfiles\nhosts: files dns [NOTFOUND=return]\n"),
	}
	image := providers.RealizedImageV1{Digest: buildLockTestDigest("1"), ConfigDigest: buildLockTestDigest("2"), RootFSSubject: buildLockTestDigest("3")}
	return policy, image, platform, files
}

func verifiedImageIdentityFixtureV1(t *testing.T) (ImageIdentityEvidenceV1, *blueprint.IdentityPolicyV1, providers.RealizedImageV1, blueprint.Platform) {
	t.Helper()
	policy, image, platform, files := imageIdentityFixtureV1(t)
	evidence, err := VerifyImageIdentityFilesV1(policy, image, platform, files)
	if err != nil {
		t.Fatal(err)
	}
	return evidence, policy, image, platform
}

func TestImageIdentityEvidenceV1ObservedRelationshipsAndCanonicalRoundTrip(t *testing.T) {
	policy, image, platform, files := imageIdentityFixtureV1(t)
	evidence, err := VerifyImageIdentityFilesV1(policy, image, platform, files)
	if err != nil {
		t.Fatal(err)
	}
	wantUsers := []runtimeidentity.IdentityV1{
		{Username: "zeta", UID: "20", GID: "20", SupplementaryGIDs: []string{}},
		{Username: "worker", UID: "200", GID: "200", SupplementaryGIDs: []string{"40", "300"}},
	}
	if !reflect.DeepEqual(evidence.Users, wantUsers) || !reflect.DeepEqual(evidence.Groups, policy.Groups) {
		t.Fatalf("wrong observed inventory: %#v", evidence)
	}
	if len(evidence.Groups) != 5 || evidence.Groups["unused"] != "450" {
		t.Fatal("unused declaration disappeared")
	}
	wantDigest, err := canonical.Sum("identity-policy", blueprint.IdentityPolicySchemaV1, policy)
	if err != nil || evidence.PolicyDigest != wantDigest || evidence.Image != image || evidence.Platform != platform {
		t.Fatalf("binding changed: %v", err)
	}
	for path, content := range files {
		sum := sha256.Sum256(content)
		if evidence.Files[path].Digest != canonical.Digest("sha256:"+hex.EncodeToString(sum[:])) {
			t.Fatal("file digest does not cover original bytes")
		}
	}
	encoded, err := EncodeImageIdentityEvidenceV1(evidence, policy, image, platform)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"SECRET", "private", "/bin/sh", "Build.User", "hosts", "full_name", "capabilities"} {
		if bytes.Contains(encoded, []byte(private)) {
			t.Fatalf("source data leaked: %q", private)
		}
	}
	decoded, err := DecodeImageIdentityEvidenceV1(encoded, policy, image, platform)
	if err != nil || !reflect.DeepEqual(decoded, evidence) {
		t.Fatalf("roundtrip changed: %v", err)
	}
	// Subsequent mutation of the caller's inputs cannot mutate returned evidence.
	policy.Groups["unused"] = "451"
	files["etc/group"][0] = 'x'
	if evidence.Groups["unused"] != "450" {
		t.Fatal("evidence aliases the policy")
	}
}

func TestImageIdentityEvidenceV1RejectsAccountSources(t *testing.T) {
	type sourceCase struct{ name, path, old, replacement string }
	cases := []sourceCase{
		{"passwd short", "etc/passwd", "worker:SECRET:200:200:private:/home/worker:/bin/sh", "worker:x:200:200"},
		{"passwd long", "etc/passwd", "worker:SECRET:200:200:private:/home/worker:/bin/sh", "worker:x:200:200::/:/bin/sh:extra"},
		{"empty username", "etc/passwd", "worker:SECRET:", ":SECRET:"},
		{"spaced username", "etc/passwd", "Build.User:x:", "Build User:x:"},
		{"control username", "etc/passwd", "Build.User:x:", "Build\vUser:x:"},
		{"DEL username", "etc/passwd", "Build.User:x:", "Build\x7fUser:x:"},
		{"compat user plus", "etc/passwd", "Build.User:x:", "+Build.User:x:"},
		{"compat user minus", "etc/passwd", "Build.User:x:", "-Build.User:x:"},
		{"duplicate user", "etc/passwd", "Build.User:x:900:900::/:/bin/false", "worker:x:900:900::/:/bin/false"},
		{"uid alias", "etc/passwd", "Build.User:x:900:", "Build.User:x:200:"},
		{"sentinel uid", "etc/passwd", "Build.User:x:900:", "Build.User:x:4294967295:"},
		{"overflow uid", "etc/passwd", "Build.User:x:900:", "Build.User:x:4294967296:"},
		{"negative uid", "etc/passwd", "Build.User:x:900:", "Build.User:x:-1:"},
		{"signed uid", "etc/passwd", "Build.User:x:900:", "Build.User:x:+900:"},
		{"zero padded uid", "etc/passwd", "Build.User:x:900:", "Build.User:x:0900:"},
		{"hex uid", "etc/passwd", "Build.User:x:900:", "Build.User:x:0x384:"},
		{"empty uid", "etc/passwd", "Build.User:x:900:", "Build.User:x::"},
		{"missing primary", "etc/passwd", "Build.User:x:900:900:", "Build.User:x:900:901:"},
		{"sentinel primary", "etc/passwd", "Build.User:x:900:900:", "Build.User:x:900:4294967295:"},
		{"wrong declared uid", "etc/passwd", "worker:SECRET:200:", "worker:SECRET:201:"},
		{"wrong declared primary", "etc/passwd", "worker:SECRET:200:200:", "worker:SECRET:200:20:"},
		{"missing declared user", "etc/passwd", "zeta:x:20:20::/:/bin/false\n", ""},
		{"group short", "etc/group", "unused:x:450:", "unused:x:450"},
		{"group long", "etc/group", "unused:x:450:", "unused:x:450::extra"},
		{"empty group name", "etc/group", "unused:x:450:", ":x:450:"},
		{"group compat", "etc/group", "unused:x:450:", "+unused:x:450:"},
		{"group compat minus", "etc/group", "unused:x:450:", "-unused:x:450:"},
		{"duplicate group name", "etc/group", "unused:x:450:", "worker:x:450:"},
		{"gid alias", "etc/group", "unused:x:450:", "unused:x:200:"},
		{"sentinel group", "etc/group", "unused:x:450:", "unused:x:4294967295:"},
		{"noncanonical group", "etc/group", "unused:x:450:", "unused:x:0450:"},
		{"missing inactive primary", "etc/group", "Build.Group:x:900:Build.User\n", ""},
		{"unknown member", "etc/group", "unused:x:450:", "unused:x:450:absent"},
		{"duplicate member", "etc/group", "shared:SECRET:300:worker", "shared:SECRET:300:worker,worker"},
		{"empty member", "etc/group", "shared:SECRET:300:worker", "shared:SECRET:300:worker,"},
		{"missing membership", "etc/group", "shared:SECRET:300:worker", "shared:SECRET:300:"},
		{"extra membership", "etc/group", "unused:x:450:", "unused:x:450:worker"},
		{"wrong unused group", "etc/group", "unused:x:450:", "unused:x:451:"},
		{"missing unused group", "etc/group", "unused:x:450:\n", ""},
		{"NUL", "etc/passwd", "private", "private\x00SECRET"},
		{"CR", "etc/passwd", "private", "private\rSECRET"},
		{"invalid name encoding", "etc/group", "unused:x:450:", "\xff:x:450:"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			policy, image, platform, files := imageIdentityFixtureV1(t)
			files[test.path] = []byte(strings.Replace(string(files[test.path]), test.old, test.replacement, 1))
			assertImageIdentitySourceRejectsV1(t, policy, image, platform, files)
		})
	}
}

func assertImageIdentitySourceRejectsV1(t *testing.T, policy *blueprint.IdentityPolicyV1, image providers.RealizedImageV1, platform blueprint.Platform, files map[string][]byte) {
	t.Helper()
	evidence, err := VerifyImageIdentityFilesV1(policy, image, platform, files)
	if err == nil || !reflect.DeepEqual(evidence, ImageIdentityEvidenceV1{}) {
		t.Fatalf("accepted invalid source or returned partial evidence: %v", err)
	}
	for _, private := range []string{"SECRET", "private", "Build.User", "absent", "worker", "\xff"} {
		if strings.Contains(err.Error(), private) {
			t.Fatalf("source data in diagnostic: %v", err)
		}
	}
}

func TestImageIdentityEvidenceV1NSSAndBounds(t *testing.T) {
	for _, source := range []string{
		"", "passwd: files\n", "group: files\n",
		"passwd: compat\ngroup: files\n", "passwd: files sss\ngroup: files\n",
		"passwd: files [NOTFOUND=return]\ngroup: files\n",
		"passwd: files\ngroup: files systemd\n",
		"passwd: files\npasswd: files\ngroup: files\n",
		"passwd: files\ngroup: files\ngroup: files\n",
		"passwd: files\\\ngroup: files\n",
		"passwd: files\nhosts: files \\\ngroup: files\n",
		"passwd:\n files\ngroup: files\n",
		"passwd: files\ngroup: files\x00SECRET\n",
	} {
		t.Run(source, func(t *testing.T) {
			policy, image, platform, files := imageIdentityFixtureV1(t)
			files["etc/nsswitch.conf"] = []byte(source)
			assertImageIdentitySourceRejectsV1(t, policy, image, platform, files)
		})
	}
	for _, path := range imageIdentityPathsV1 {
		for _, size := range []string{"missing", "empty", "file", "line", "scanner"} {
			t.Run(path+size, func(t *testing.T) {
				policy, image, platform, files := imageIdentityFixtureV1(t)
				switch size {
				case "missing":
					delete(files, path)
				case "empty":
					files[path] = nil
				case "file":
					files[path] = bytes.Repeat([]byte("#"), imageIdentityFileLimitV1+1)
				case "line":
					files[path] = append(bytes.Repeat([]byte("#"), imageIdentityLineLimitV1+1), '\n')
				case "scanner":
					files[path] = bytes.Repeat([]byte("#"), imageIdentityLineLimitV1+3)
				}
				assertImageIdentitySourceRejectsV1(t, policy, image, platform, files)
			})
		}
	}
	policy, image, platform, files := imageIdentityFixtureV1(t)
	files["etc/shadow"] = []byte("SECRET")
	assertImageIdentitySourceRejectsV1(t, policy, image, platform, files)
	delete(files, "etc/shadow")
	// An unknown key cannot replace one of the three fixed files.
	files["passwd"] = files["etc/passwd"]
	delete(files, "etc/passwd")
	assertImageIdentitySourceRejectsV1(t, policy, image, platform, files)
}

func TestImageIdentityEvidenceV1NSSLexicalOverrides(t *testing.T) {
	const valid = "passwd: files\ngroup: files\n"
	for _, override := range []string{
		"passwd sss\n", "group ldap\n", "passwd\tsss\n", "group\tldap\n",
		" passwd sss\n", "\tgroup ldap\n", "passwd\vsss\n", "group\fldap\n",
		"passwd\n", "group\n", "passwd\t\n", "group \n",
	} {
		for _, source := range []string{valid + override, override + valid} {
			t.Run(source, func(t *testing.T) {
				policy, image, platform, files := imageIdentityFixtureV1(t)
				files["etc/nsswitch.conf"] = []byte(source)
				assertImageIdentitySourceRejectsV1(t, policy, image, platform, files)
			})
		}
	}
	for _, source := range []string{
		"passwd: files\u00a0\ngroup: files\n", "passwd: files\ngroup: \u00a0files\n",
		"passwd\u00a0: files\ngroup: files\n", "passwd: files\ngroup: files\u2003\n",
		"passwd: \u2003files\ngroup: files\n", "passwd: files\ngroup\u2003: files\n",
	} {
		t.Run(source, func(t *testing.T) {
			policy, image, platform, files := imageIdentityFixtureV1(t)
			files["etc/nsswitch.conf"] = []byte(source)
			assertImageIdentitySourceRejectsV1(t, policy, image, platform, files)
		})
	}
	for _, source := range []string{
		valid, " \tpasswd \t: \tfiles \t# comment\n\tgroup: files\n",
		"passwd\v:\ffiles\v\ngroup\f:\vfiles\f\n",
		valid + "hosts dns\nservices: files dns [NOTFOUND=return]\n",
		"# passwd sss\n" + valid + "# group ldap\n",
	} {
		t.Run("valid "+source, func(t *testing.T) {
			policy, image, platform, files := imageIdentityFixtureV1(t)
			files["etc/nsswitch.conf"] = []byte(source)
			if _, err := VerifyImageIdentityFilesV1(policy, image, platform, files); err != nil {
				t.Fatalf("rejected literal files-only control or unrelated database: %v", err)
			}
		})
	}
}

func TestImageIdentityEvidenceV1LiteralCommentClassification(t *testing.T) {
	for _, prefix := range []string{"\u0085", "\u00a0", "\u1680", "\u2000", "\u2003", "\u2028", "\u2029", "\u202f", "\u205f", "\u3000"} {
		for _, path := range []string{"etc/passwd", "etc/group"} {
			for _, kind := range []string{"alias", "relationship", "blank"} {
				t.Run(hex.EncodeToString([]byte(prefix))+path+kind, func(t *testing.T) {
					policy, image, platform, files := imageIdentityFixtureV1(t)
					line := prefix + "#hidden:x:200:200::/:/bin/false\n"
					if kind == "relationship" {
						line = prefix + "#hidden:x:901:999::/:/bin/false\n"
					}
					if path == "etc/group" {
						line = prefix + "#hidden:x:200:\n"
						if kind == "relationship" {
							line = prefix + "#hidden:x:999:worker\n"
						}
					}
					if kind == "blank" {
						line = prefix + "\n"
					}
					files[path] = append(files[path], []byte(line)...)
					assertImageIdentitySourceRejectsV1(t, policy, image, platform, files)
				})
			}
		}
	}
	// ASCII blank/comments keep their meaning. Unrelated Unicode account names
	// remain literal records, rather than being rejected or silently discarded.
	policy, image, platform, files := imageIdentityFixtureV1(t)
	for _, path := range imageIdentityPathsV1 {
		files[path] = append(files[path], []byte(" \t\v\f\n \t\v\f# ignored:malformed:record\n")...)
	}
	files["etc/passwd"] = append(files["etc/passwd"], []byte("\u00a0#inactive:x:901:901::/:/bin/false\n")...)
	files["etc/group"] = append(files["etc/group"], []byte("\u00a0#inactive:x:901:\u00a0#inactive\n")...)
	if _, err := VerifyImageIdentityFilesV1(policy, image, platform, files); err != nil {
		t.Fatalf("rejected ASCII comments or valid literal inactive account: %v", err)
	}
}

func TestImageIdentityEvidenceV1AcceptsExactBoundsAndRoot(t *testing.T) {
	policy, image, platform, files := imageIdentityFixtureV1(t)
	policy.Users["root"] = blueprint.IdentityPolicyUserV1{UID: "0", PrimaryGroup: "root", SupplementaryGroups: []string{}}
	policy.Groups["root"] = "0"
	// An inactive name need not use portable policy grammar. IDs may reach the
	// last non-sentinel uint32 value, and a missing final LF is supported.
	files["etc/passwd"] = []byte(strings.Replace(string(files["etc/passwd"]), "Build.User:x:900:", "Build.User:x:4294967294:", 1))
	files["etc/group"] = bytes.TrimSuffix(files["etc/group"], []byte("\n"))
	files["etc/nsswitch.conf"] = append(files["etc/nsswitch.conf"], append(bytes.Repeat([]byte("#"), imageIdentityLineLimitV1), '\n')...)
	// Pad with bounded comment lines to exactly the selected-file limit.
	for len(files["etc/nsswitch.conf"]) < imageIdentityFileLimitV1 {
		remaining := imageIdentityFileLimitV1 - len(files["etc/nsswitch.conf"])
		n := min(remaining, imageIdentityLineLimitV1)
		line := bytes.Repeat([]byte("#"), n)
		line[n-1] = '\n'
		files["etc/nsswitch.conf"] = append(files["etc/nsswitch.conf"], line...)
	}
	evidence, err := VerifyImageIdentityFilesV1(policy, image, platform, files)
	if err != nil || len(evidence.Users) != 3 || evidence.Users[0].Username != "root" || evidence.Files["etc/nsswitch.conf"].Size != "1048576" {
		t.Fatalf("valid edge rejected: %v", err)
	}
}

func TestImageIdentityEvidenceV1RevalidatesEvidence(t *testing.T) {
	cases := map[string]func(*ImageIdentityEvidenceV1){
		"schema":        func(e *ImageIdentityEvidenceV1) { e.Schema = "future" },
		"image":         func(e *ImageIdentityEvidenceV1) { e.Image.Digest = buildLockTestDigest("9") },
		"config":        func(e *ImageIdentityEvidenceV1) { e.Image.ConfigDigest = buildLockTestDigest("9") },
		"rootfs":        func(e *ImageIdentityEvidenceV1) { e.Image.RootFSSubject = buildLockTestDigest("9") },
		"platform":      func(e *ImageIdentityEvidenceV1) { e.Platform.Architecture = "arm64" },
		"policy digest": func(e *ImageIdentityEvidenceV1) { e.PolicyDigest = buildLockTestDigest("9") },
		"missing file":  func(e *ImageIdentityEvidenceV1) { delete(e.Files, "etc/group") },
		"unknown file":  func(e *ImageIdentityEvidenceV1) { e.Files["etc/shadow"] = e.Files["etc/passwd"] },
		"replaced file": func(e *ImageIdentityEvidenceV1) {
			e.Files["passwd"] = e.Files["etc/passwd"]
			delete(e.Files, "etc/passwd")
		},
		"nil users":            func(e *ImageIdentityEvidenceV1) { e.Users = nil },
		"missing user":         func(e *ImageIdentityEvidenceV1) { e.Users = e.Users[:1] },
		"duplicate user":       func(e *ImageIdentityEvidenceV1) { e.Users[1] = e.Users[0] },
		"wrong order":          func(e *ImageIdentityEvidenceV1) { e.Users[0], e.Users[1] = e.Users[1], e.Users[0] },
		"unknown user":         func(e *ImageIdentityEvidenceV1) { e.Users[0].Username = "absent" },
		"wrong uid":            func(e *ImageIdentityEvidenceV1) { e.Users[0].UID = "21" },
		"invalid uid":          func(e *ImageIdentityEvidenceV1) { e.Users[0].UID = "020" },
		"wrong gid":            func(e *ImageIdentityEvidenceV1) { e.Users[0].GID = "40" },
		"nil extras":           func(e *ImageIdentityEvidenceV1) { e.Users[0].SupplementaryGIDs = nil },
		"missing extras":       func(e *ImageIdentityEvidenceV1) { e.Users[1].SupplementaryGIDs = []string{} },
		"unordered extras":     func(e *ImageIdentityEvidenceV1) { e.Users[1].SupplementaryGIDs = []string{"300", "40"} },
		"primary as extra":     func(e *ImageIdentityEvidenceV1) { e.Users[1].SupplementaryGIDs = []string{"200"} },
		"missing unused group": func(e *ImageIdentityEvidenceV1) { delete(e.Groups, "unused") },
		"wrong unused group":   func(e *ImageIdentityEvidenceV1) { e.Groups["unused"] = "451" },
		"extra group":          func(e *ImageIdentityEvidenceV1) { e.Groups["extra"] = "999" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			evidence, policy, image, platform := verifiedImageIdentityFixtureV1(t)
			mutate(&evidence)
			if ValidateImageIdentityEvidenceV1(evidence, policy, image, platform) == nil {
				t.Fatal("accepted forged structure or binding")
			}
			if _, err := EncodeImageIdentityEvidenceV1(evidence, policy, image, platform); err == nil {
				t.Fatal("encoder accepted forged evidence")
			}
			content, err := canonical.Marshal(evidence)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeImageIdentityEvidenceV1(content, policy, image, platform); err == nil {
				t.Fatal("reader accepted forged evidence")
			}
		})
	}
	for _, size := range []string{"", "0", "-1", "+1", "01", "1.0", "1048577", "4294967296"} {
		t.Run("size "+size, func(t *testing.T) {
			evidence, policy, image, platform := verifiedImageIdentityFixtureV1(t)
			file := evidence.Files["etc/passwd"]
			file.Size = size
			evidence.Files["etc/passwd"] = file
			if ValidateImageIdentityEvidenceV1(evidence, policy, image, platform) == nil {
				t.Fatal("accepted invalid file size")
			}
		})
	}
	evidence, policy, image, platform := verifiedImageIdentityFixtureV1(t)
	file := evidence.Files["etc/passwd"]
	file.Digest = "sha256:BAD"
	evidence.Files["etc/passwd"] = file
	if ValidateImageIdentityEvidenceV1(evidence, policy, image, platform) == nil {
		t.Fatal("accepted invalid digest")
	}
}

func TestImageIdentityEvidenceV1ClosedCanonicalJSON(t *testing.T) {
	evidence, policy, image, platform := verifiedImageIdentityFixtureV1(t)
	content, err := EncodeImageIdentityEvidenceV1(evidence, policy, image, platform)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for name, invalid := range map[string]string{
		"unknown":        strings.Replace(text, "{", `{"extra":"SECRET",`, 1),
		"duplicate":      strings.Replace(text, `"schema":`, `"schema":"wrong","schema":`, 1),
		"case alias":     strings.Replace(text, `"schema":`, `"Schema":"wrong","schema":`, 1),
		"nested alias":   strings.Replace(text, `"uid":"20"`, `"UID":"20","uid":"20"`, 1),
		"nested unknown": strings.Replace(text, `"uid":"20"`, `"secret":"SECRET","uid":"20"`, 1),
		"file alias":     strings.Replace(text, `"size":`, `"Size":"1","size":`, 1),
		"missing schema": strings.Replace(text, `"schema":"image-identity-evidence-v1",`, "", 1),
		"null users":     strings.Replace(text, `"users":[`, `"users":null,"old":[`, 1),
		"wrong type":     strings.Replace(text, `"uid":"20"`, `"uid":20`, 1),
		"whitespace":     " " + text,
		"trailer":        text + "{}",
		"bad trailer":    text + "x",
		"malformed":      "{",
		"oversized":      strings.Repeat(" ", imageIdentityFileLimitV1+1),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := DecodeImageIdentityEvidenceV1([]byte(invalid), policy, image, platform)
			if err == nil || !reflect.DeepEqual(got, ImageIdentityEvidenceV1{}) || strings.Contains(err.Error(), "SECRET") {
				t.Fatalf("accepted or leaked invalid JSON: %v", err)
			}
		})
	}
	// JSON reordering also rejects; this is an existing canonical wire contract.
	var pretty bytes.Buffer
	if json.Indent(&pretty, content, "", " ") != nil {
		t.Fatal("fixture is not JSON")
	}
	if _, err := DecodeImageIdentityEvidenceV1(pretty.Bytes(), policy, image, platform); err == nil {
		t.Fatal("accepted noncanonical JSON")
	}
}

func TestImageIdentityEvidenceV1CompletePolicyBindingAndInputs(t *testing.T) {
	evidence, policy, image, platform := verifiedImageIdentityFixtureV1(t)
	for name, change := range map[string]func(*blueprint.IdentityPolicyV1){
		"unused role": func(p *blueprint.IdentityPolicyV1) {
			p.Roles["unused"] = blueprint.IdentityPolicyRoleV1{User: "zeta", Capabilities: []string{"CAP_CHOWN"}}
		},
		"capability": func(p *blueprint.IdentityPolicyV1) {
			p.Roles["app"] = blueprint.IdentityPolicyRoleV1{User: "worker", Capabilities: []string{"CAP_SETUID"}}
		},
		"metadata": func(p *blueprint.IdentityPolicyV1) {
			u := p.Users["worker"]
			full := "Service worker"
			u.FullName = &full
			p.Users["worker"] = u
		},
	} {
		t.Run(name, func(t *testing.T) {
			p, _, _, files := imageIdentityFixtureV1(t)
			change(p)
			if ValidateImageIdentityEvidenceV1(evidence, p, image, platform) == nil {
				t.Fatal("changed complete policy did not invalidate evidence")
			}
			if _, err := VerifyImageIdentityFilesV1(p, image, platform, files); err != nil {
				t.Fatalf("valid descriptive/role policy broke account verification: %v", err)
			}
		})
	}
	for name, change := range map[string]func(*blueprint.IdentityPolicyV1, *providers.RealizedImageV1, *blueprint.Platform){
		"bad policy": func(p *blueprint.IdentityPolicyV1, _ *providers.RealizedImageV1, _ *blueprint.Platform) {
			p.Schema = "bad"
		},
		"bad image": func(_ *blueprint.IdentityPolicyV1, i *providers.RealizedImageV1, _ *blueprint.Platform) {
			i.Digest = "bad"
		},
		"bad config": func(_ *blueprint.IdentityPolicyV1, i *providers.RealizedImageV1, _ *blueprint.Platform) {
			i.ConfigDigest = "bad"
		},
		"bad rootfs": func(_ *blueprint.IdentityPolicyV1, i *providers.RealizedImageV1, _ *blueprint.Platform) {
			i.RootFSSubject = "bad"
		},
		"bad platform": func(_ *blueprint.IdentityPolicyV1, _ *providers.RealizedImageV1, p *blueprint.Platform) {
			p.Canonical = "bad"
		},
		"non Linux": func(_ *blueprint.IdentityPolicyV1, _ *providers.RealizedImageV1, p *blueprint.Platform) {
			*p, _ = blueprint.ParsePlatform("darwin/amd64")
		},
	} {
		t.Run(name, func(t *testing.T) {
			p, i, target, files := imageIdentityFixtureV1(t)
			change(p, &i, &target)
			assertImageIdentitySourceRejectsV1(t, p, i, target, files)
			if ValidateImageIdentityEvidenceV1(evidence, p, i, target) == nil {
				t.Fatal("reader accepted invalid policy/image/platform")
			}
		})
	}
	_, _, _, files := imageIdentityFixtureV1(t)
	assertImageIdentitySourceRejectsV1(t, nil, image, platform, files)
	if ValidateImageIdentityEvidenceV1(evidence, nil, image, platform) == nil {
		t.Fatal("nil policy accepted")
	}
	if _, err := canonical.Sum("identity-policy", blueprint.IdentityPolicySchemaV1, policy); err != nil {
		t.Fatal(err)
	}
}
