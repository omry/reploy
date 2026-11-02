package blueprint

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/runtimeidentity"
)

const mountPolicy = `{schema: 1, users: {root: 0, mail: {uid: 200, primary_group: staff}, postfix: 202}, groups: {root: 0, staff: 201, postfix: 202, shared: 203}, roles: {app: {user: mail}}}`

func mountBlueprint(t *testing.T, fields string) Document {
	t.Helper()
	raw := strings.Replace(policyBlueprintV1(mountPolicy), "      target: /data\n", "      target: /data\n"+fields, 1)
	source, err := Decode([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	document, err := Resolve(source)
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func mailMountContext() runtimeidentity.IdentityV1 {
	return runtimeidentity.IdentityV1{Username: "mail", UID: "200", GID: "201", SupplementaryGIDs: []string{}}
}

func TestManagedMountMetadataYAMLAndCanonicalIdentity(t *testing.T) {
	document := mountBlueprint(t, `      owner: mail
      permissions: "0750"
      directories:
        queue/active: {owner: postfix, permissions: "0700"}
        queue: {}
      files:
        delivery.log: {group: shared, permissions: "0600"}
`)
	mount := document.Environment.Mounts["data"]
	if mount.Owner != "mail" || mount.Group != "" || mount.Permissions != "0750" || len(mount.Directories) != 2 || len(mount.Files) != 1 {
		t.Fatalf("mount declarations changed: %#v", mount)
	}
	if !reflect.DeepEqual(mount, document.Docker.Mounts["data"].Contract) {
		t.Fatal("Docker binding lost the environment metadata")
	}
	payload, err := EncodeResolvedDocumentV1(document)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeResolvedDocumentV1(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded.Environment.Mounts, document.Environment.Mounts) {
		t.Fatal("canonical round trip lost metadata")
	}
	first, err := DocumentDigestV1(document)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*EnvironmentMount){
		func(m *EnvironmentMount) { m.Owner = "postfix" },
		func(m *EnvironmentMount) { m.Group = "shared" },
		func(m *EnvironmentMount) { m.Permissions = "0700" },
		func(m *EnvironmentMount) { m.Directories["queue"] = MountMetadata{Owner: "postfix"} },
		func(m *EnvironmentMount) { m.Files["second.log"] = MountMetadata{} },
	} {
		candidate, err := DecodeResolvedDocumentV1(payload)
		if err != nil {
			t.Fatal(err)
		}
		m := candidate.Environment.Mounts["data"]
		mutate(&m)
		candidate.Environment.Mounts["data"] = m
		binding := candidate.Docker.Mounts["data"]
		binding.Contract = m
		candidate.Docker.Mounts["data"] = binding
		digest, err := DocumentDigestV1(candidate)
		if err != nil || digest == first {
			t.Fatalf("metadata change did not change locked identity: %v", err)
		}
	}
}

func TestManagedMountContextAndOwnerPrimaryGroup(t *testing.T) {
	policy := mountBlueprint(t, "").Environment.Runtime.IdentityPolicy
	for _, test := range []struct {
		name     string
		metadata MountMetadata
		uid, gid uint32
		mode     *uint32
	}{
		{name: "context", uid: 200, gid: 201},
		{name: "owner primary differs from context", metadata: MountMetadata{Owner: "postfix"}, uid: 202, gid: 202},
		{name: "explicit group", metadata: MountMetadata{Owner: "postfix", Group: "shared"}, uid: 202, gid: 203},
		{name: "root is a user", metadata: MountMetadata{Owner: "root"}, uid: 0, gid: 0},
		{name: "mode zero is present", metadata: MountMetadata{Permissions: "0000"}, uid: 200, gid: 201, mode: uint32Pointer(0)},
		{name: "Linux special mode bits", metadata: MountMetadata{Permissions: "2750"}, uid: 200, gid: 201, mode: uint32Pointer(0o2750)},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := ResolveMountMetadata(test.metadata, policy, mailMountContext())
			if err != nil {
				t.Fatal(err)
			}
			if got.UID != test.uid || got.GID != test.gid || !reflect.DeepEqual(got.Permissions, test.mode) {
				t.Fatalf("resolved metadata = %#v", got)
			}
		})
	}
	for _, context := range []runtimeidentity.IdentityV1{
		{Username: "root", UID: "0", GID: "0", SupplementaryGIDs: []string{}},
		{Username: "arbiter", UID: "1000", GID: "42", SupplementaryGIDs: []string{}},
	} {
		got, err := ResolveMountMetadata(MountMetadata{}, nil, context)
		if err != nil || got.Permissions != nil {
			t.Fatalf("ordinary contextual metadata: %#v, %v", got, err)
		}
		if context.UID == "0" && (got.UID != 0 || got.GID != 0) || context.UID == "1000" && (got.UID != 1000 || got.GID != 42) {
			t.Fatalf("ordinary runtime owner was changed: %#v", got)
		}
	}
}

func uint32Pointer(value uint32) *uint32 { return &value }

func TestManagedMountMetadataRefusesUnprovedPrincipalsAndContexts(t *testing.T) {
	policy := mountBlueprint(t, "").Environment.Runtime.IdentityPolicy
	for _, metadata := range []MountMetadata{
		{Owner: "app"}, {Owner: "unknown"}, {Group: "unknown"}, {Group: "app"},
		{Owner: "../mail"}, {Owner: "200"}, {Permissions: "750"}, {Permissions: "+123"},
		{Permissions: "0888"}, {Permissions: "-100"}, {Permissions: " 700"}, {Permissions: "10000"},
	} {
		if _, err := ResolveMountMetadata(metadata, policy, mailMountContext()); err == nil {
			t.Fatalf("invalid metadata accepted: %#v", metadata)
		}
	}
	for _, mutate := range []func(*runtimeidentity.IdentityV1){
		func(c *runtimeidentity.IdentityV1) { c.Username = "unknown" },
		func(c *runtimeidentity.IdentityV1) { c.UID = "202" },
		func(c *runtimeidentity.IdentityV1) { c.GID = "203" },
		func(c *runtimeidentity.IdentityV1) { c.UID = "0200" },
		func(c *runtimeidentity.IdentityV1) { c.UID = "4294967295" },
	} {
		context := mailMountContext()
		mutate(&context)
		if _, err := ResolveMountMetadata(MountMetadata{}, policy, context); err == nil {
			t.Fatalf("unproved default context accepted: %#v", context)
		}
	}
	for _, metadata := range []MountMetadata{{Owner: "postfix"}, {Group: "staff"}} {
		if _, err := ResolveMountMetadata(metadata, nil, mailMountContext()); err == nil {
			t.Fatalf("undeclared host lookup implicitly accepted: %#v", metadata)
		}
	}
	got, err := ResolveMountMetadata(MountMetadata{Owner: "mail"}, nil, mailMountContext())
	if err != nil || got.UID != 200 || got.GID != 201 {
		t.Fatalf("exact ordinary context was not reused: %#v, %v", got, err)
	}
}

func TestManagedMountObjectsHaveDeterministicPreparationOrder(t *testing.T) {
	document := mountBlueprint(t, `      owner: postfix
      directories:
        z/a: {permissions: "0700"}
        z: {}
        a: {}
      files:
        z/first: {owner: postfix}
        aa: {}
`)
	objects, err := ResolveManagedMountObjects(document.Environment.Mounts["data"], document.Environment.Runtime.IdentityPolicy, mailMountContext())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".", "a", "z", "z/a", "aa", "z/first"}
	if len(objects) != len(want) {
		t.Fatalf("objects = %#v", objects)
	}
	for i, object := range objects {
		if object.Path != want[i] || object.Directory != (i < 4) {
			t.Fatalf("preparation order = %#v", objects)
		}
	}
	if objects[0].Identity.UID != 202 || objects[1].Identity.UID != 200 || objects[1].Identity.GID != 201 || objects[5].Identity.UID != 202 {
		t.Fatal("an omitted child owner must use its runtime context, independently of an explicit mount owner")
	}
	if objects[3].Identity.Permissions == nil || *objects[3].Identity.Permissions != 0o700 || objects[1].Identity.Permissions != nil {
		t.Fatal("omitted creation permissions were not preserved")
	}
}

func TestManagedMountPathsAndKindsRejectAmbiguousTrees(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../file", "a/../file", "a/./file", "/file", "a//b", "a/", `a\b`, "a\x00b", "a\nb"} {
		for _, directory := range []bool{false, true} {
			mount := EnvironmentMount{Target: "/data"}
			if directory {
				mount.Directories = map[string]MountMetadata{name: {}}
			} else {
				mount.Files = map[string]MountMetadata{name: {}}
			}
			if _, err := ResolveManagedMountObjects(mount, nil, mailMountContext()); err == nil {
				t.Fatalf("unsafe child path accepted: %q", name)
			}
		}
	}
	for _, mount := range []EnvironmentMount{
		{Target: "/data", Directories: map[string]MountMetadata{"queue": {}}, Files: map[string]MountMetadata{"queue": {}}},
		{Target: "/data", Directories: map[string]MountMetadata{"queue/active": {}}, Files: map[string]MountMetadata{"queue": {}}},
		{Target: "/data", Files: map[string]MountMetadata{"queue": {}, "queue/file": {}}},
	} {
		if _, err := ResolveManagedMountObjects(mount, nil, mailMountContext()); err == nil {
			t.Fatalf("conflicting object tree accepted: %#v", mount)
		}
	}
}

func TestManagedMountObjectResolverRejectsProtectedOrUnresolvedTargets(t *testing.T) {
	for _, target := range []string{"", ".", "data", "/", "/data/../etc", "/data//x", `/data\x`, "/etc/passwd", "/proc", "/opt/reploy", "/mnt/reploy-home"} {
		mount := EnvironmentMount{Target: target, Files: map[string]MountMetadata{"file": {}}}
		if _, err := ResolveManagedMountObjects(mount, nil, mailMountContext()); err == nil {
			t.Fatalf("resolver accepted a non-managed or protected target %q", target)
		}
	}
}

func TestManagedMountMetadataStrictYAML(t *testing.T) {
	for _, fields := range []string{
		"      objects: {}\n", "      owner: {name: mail}\n", "      files: []\n",
		"      directories: {queue: {kind: directory}}\n", "      files: {log: {content: secret}}\n",
		"      files: {log: {}, log: {owner: mail}}\n",
	} {
		raw := strings.Replace(policyBlueprintV1(mountPolicy), "      target: /data\n", "      target: /data\n"+fields, 1)
		if _, err := Decode([]byte(raw)); err == nil {
			t.Fatalf("unexpected public shape accepted: %s", fields)
		}
	}
}

func TestIntrinsicRootPolicyIsNotAnExternalMount(t *testing.T) {
	for _, writable := range []string{"false", "true"} {
		raw := strings.Replace(minimalBlueprint, "  mounts:\n    data:", "  mounts:\n    root: {target: /, writable: "+writable+"}\n    data:", 1)
		source, err := Decode([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		document, err := Resolve(source)
		if err != nil {
			t.Fatal(err)
		}
		if document.Environment.ImageRootWritable() != (writable == "true") || len(document.Docker.Mounts) != 1 {
			t.Fatal("intrinsic root changed external mount inventory or policy")
		}
		payload, err := EncodeResolvedDocumentV1(document)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := DecodeResolvedDocumentV1(payload)
		if err != nil || decoded.Environment.ImageRootWritable() != (writable == "true") {
			t.Fatalf("root policy round trip: %v", err)
		}
	}
	for _, root := range []string{
		"root: {target: /, owner: root}", "root: {target: /, permissions: '0755'}",
		"root: {target: /, update_policy: preserve}", "other: {target: /}",
		"root: {target: /, directories: {var: {}}}", "root: {target: /, files: {x: {}}}",
	} {
		raw := strings.Replace(minimalBlueprint, "  mounts:\n    data:", "  mounts:\n    "+root+"\n    data:", 1)
		source, err := Decode([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Resolve(source); err == nil {
			t.Fatalf("root acquired managed storage semantics: %s", root)
		}
	}
}

func TestManagedMountCanonicalInputCannotBypassDeclarationChecks(t *testing.T) {
	base := mountBlueprint(t, "      owner: mail\n")
	payload, err := EncodeResolvedDocumentV1(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Document){
		func(d *Document) {
			m := d.Environment.Mounts["data"]
			m.Owner = "unknown"
			d.Environment.Mounts["data"] = m
		},
		func(d *Document) {
			m := d.Docker.Mounts["data"]
			m.Contract.Owner = "postfix"
			d.Docker.Mounts["data"] = m
		},
		func(d *Document) { m := d.Docker.Mounts["data"]; m.Mode = MountBind; d.Docker.Mounts["data"] = m },
		func(d *Document) {
			m := d.Environment.Mounts["data"]
			m.Target = "/data/../etc"
			d.Environment.Mounts["data"] = m
		},
		func(d *Document) { delete(d.Docker.Mounts, "data") },
		func(d *Document) {
			m := d.Docker.Mounts["data"]
			m.Contract.Files = map[string]MountMetadata{"../outside": {}}
			d.Docker.Mounts["data"] = m
		},
	} {
		document, err := DecodeResolvedDocumentV1(payload)
		if err != nil {
			t.Fatal(err)
		}
		mutate(&document)
		if _, err := EncodeResolvedDocumentV1(document); err == nil {
			t.Fatal("inconsistent direct document accepted")
		}
		// This bypasses the encoder, as an on-disk or bundle attacker could.
		raw, err := json.Marshal(resolvedDocumentEnvelopeV1{Schema: ResolvedDocumentSchemaV1, Document: document})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeResolvedDocumentV1(ResolvedDocumentV1(raw)); err == nil {
			t.Fatal("tampered resolved input bypassed metadata validation")
		}
	}
}

func TestManagedMountCanonicalContractRequiresExactReference(t *testing.T) {
	for _, reference := range []string{"data", "", "environment.mounts.missing", "environment.mounts.data"} {
		t.Run(reference, func(t *testing.T) {
			document := mountBlueprint(t, "")
			binding := document.Docker.Mounts["data"]
			binding.Extends = reference
			binding.Contract.Permissions = "0700"
			document.Docker.Mounts["data"] = binding
			if _, err := EncodeResolvedDocumentV1(document); err == nil {
				t.Fatal("unmatched Docker-only metadata was accepted by the encoder")
			}
			raw, err := json.Marshal(resolvedDocumentEnvelopeV1{Schema: ResolvedDocumentSchemaV1, Document: document})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeResolvedDocumentV1(ResolvedDocumentV1(raw)); err == nil {
				t.Fatal("unmatched Docker-only metadata bypassed canonical decoding")
			}
		})
	}
}

func TestOrdinaryMountOmissionPreservesCanonicalWire(t *testing.T) {
	source, err := Decode([]byte(minimalBlueprint))
	if err != nil {
		t.Fatal(err)
	}
	document, err := Resolve(source)
	if err != nil {
		t.Fatal(err)
	}
	mount := document.Environment.Mounts["data"]
	encoded, err := json.Marshal(mount)
	if err != nil || string(encoded) != `{"Target":"/data","Writable":true,"UpdatePolicy":"preserve"}` {
		t.Fatalf("ordinary mount wire changed: %s, %v", encoded, err)
	}
	if document.Environment.ImageRootWritable() {
		t.Fatal("root policy omission became writable")
	}
	objects, err := ResolveManagedMountObjects(mount, nil, runtimeidentity.IdentityV1{})
	if err != nil || objects != nil {
		t.Fatalf("ordinary omitted declarations acquired preparation or context requirements: %#v, %v", objects, err)
	}
}

func TestManagedMountMetadataUsesExistingGlobalVariables(t *testing.T) {
	raw := strings.Replace(policyBlueprintV1(mountPolicy), "  id: demo\n", "  id: demo\n  vars: {mount_owner: mail, mount_mode: '0750'}\n", 1)
	raw = strings.Replace(raw, "      target: /data\n", "      target: /data\n      owner: '{{ mount_owner }}'\n      files: {log: {permissions: '{{ mount_mode }}'}}\n", 1)
	source, err := Decode([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	document, err := Resolve(source)
	if err != nil {
		t.Fatal(err)
	}
	mount := document.Environment.Mounts["data"]
	if mount.Owner != "mail" || mount.Files["log"].Permissions != "0750" {
		t.Fatalf("existing global interpolation was not used: %#v", mount)
	}
}

func TestIntrinsicRootRejectsBindingsAndCommandOverrideInBothInputs(t *testing.T) {
	raw := strings.Replace(minimalBlueprint, "  mounts:\n    data:", "  mounts:\n    root: {target: /}\n    data:", 1)
	for _, candidate := range []string{
		strings.Replace(raw, "      executable: application.server\n", "      executable: application.server\n      mounts: {root: {writable: true}}\n", 1),
		strings.Replace(raw, "docker:\n  mounts:\n", "docker:\n  mounts:\n    root: {extends: environment.mounts.root, mode: bind, source: /tmp}\n", 1),
	} {
		source, err := Decode([]byte(candidate))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Resolve(source); err == nil {
			t.Fatal("root bind/command override accepted from YAML")
		}
	}
	source, err := Decode([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	document, err := Resolve(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, binding := range []bool{false, true} {
		copy := document
		if binding {
			copy.Docker.Mounts = map[string]DockerMount{"root": {Extends: "environment.mounts.root", Mode: MountManagedBind, Source: "root", Contract: EnvironmentMount{Target: "/"}}}
		} else {
			copy.Environment.Commands = map[string]Command{"serve": {Mounts: map[string]CommandMountOverride{"root": {Writable: true}}}}
		}
		encoded, err := json.Marshal(resolvedDocumentEnvelopeV1{Schema: ResolvedDocumentSchemaV1, Document: copy})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeResolvedDocumentV1(ResolvedDocumentV1(encoded)); err == nil {
			t.Fatal("resolved document bypassed intrinsic root restrictions")
		}
	}
}
