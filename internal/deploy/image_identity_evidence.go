package deploy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strconv"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/runtimeidentity"
)

const ImageIdentityEvidenceSchemaV1 = "image-identity-evidence-v1"
const imageIdentityFileLimitV1 = 1 << 20
const imageIdentityLineLimitV1 = 64 << 10

var imageIdentityPathsV1 = [...]string{"etc/passwd", "etc/group", "etc/nsswitch.conf"}

// ImageIdentityEvidenceV1 retains verified relationships, never account secrets
// or source bytes. D3.2 supplies authenticated file bytes; D4 binds the lock.
type ImageIdentityEvidenceV1 struct {
	Schema       string                         `json:"schema"`
	Image        providers.RealizedImageV1      `json:"image"`
	Platform     blueprint.Platform             `json:"platform"`
	PolicyDigest canonical.Digest               `json:"policy_digest"`
	Files        map[string]ImageIdentityFileV1 `json:"files"`
	Users        []runtimeidentity.IdentityV1   `json:"users"`
	Groups       map[string]string              `json:"groups"`
}

type ImageIdentityFileV1 struct {
	Digest canonical.Digest `json:"digest"`
	Size   string           `json:"size"`
}

func imageIdentityErrorV1(reason string) error {
	return fmt.Errorf("image identity evidence: %s", reason)
}

func imageIdentityInputsV1(policy *blueprint.IdentityPolicyV1, image providers.RealizedImageV1, platform blueprint.Platform) (canonical.Digest, error) {
	if policy == nil || policy.Validate() != nil {
		return "", imageIdentityErrorV1("invalid policy")
	}
	if image.Validate() != nil || platform.Validate() != nil || platform.OS != "linux" {
		return "", imageIdentityErrorV1("invalid image or Linux platform")
	}
	return canonical.Sum("identity-policy", blueprint.IdentityPolicySchemaV1, policy)
}

// VerifyImageIdentityFilesV1 verifies supplied immutable bytes without host NSS,
// image execution or filesystem access. Its caller must prove image provenance.
func VerifyImageIdentityFilesV1(policy *blueprint.IdentityPolicyV1, image providers.RealizedImageV1, platform blueprint.Platform, files map[string][]byte) (ImageIdentityEvidenceV1, error) {
	digest, err := imageIdentityInputsV1(policy, image, platform)
	if err != nil {
		return ImageIdentityEvidenceV1{}, err
	}
	fail := func() (ImageIdentityEvidenceV1, error) {
		return ImageIdentityEvidenceV1{}, imageIdentityErrorV1("invalid account sources or policy relationships")
	}
	if len(files) != len(imageIdentityPathsV1) {
		return fail()
	}
	evidence := ImageIdentityEvidenceV1{Schema: ImageIdentityEvidenceSchemaV1, Image: image, Platform: platform, PolicyDigest: digest, Files: map[string]ImageIdentityFileV1{}, Users: []runtimeidentity.IdentityV1{}, Groups: map[string]string{}}
	for _, path := range imageIdentityPathsV1 {
		content := files[path]
		if len(content) == 0 || len(content) > imageIdentityFileLimitV1 || bytes.IndexByte(content, 0) >= 0 {
			return fail()
		}
		sum := sha256.Sum256(content)
		evidence.Files[path] = ImageIdentityFileV1{Digest: canonical.Digest("sha256:" + hex.EncodeToString(sum[:])), Size: strconv.Itoa(len(content))}
	}
	users, groups, err := parseImageAccountsV1(files)
	if err != nil {
		return fail()
	}
	for name := range policy.Groups {
		group, ok := groups[name]
		if !ok {
			return fail()
		}
		evidence.Groups[name] = group.gid
	}
	for name := range policy.Users {
		user, ok := users[name]
		if !ok {
			return fail()
		}
		identity := runtimeidentity.IdentityV1{Username: name, UID: user.uid, GID: user.gid, SupplementaryGIDs: []string{}}
		for _, group := range groups {
			if group.members[name] && group.gid != user.gid {
				identity.SupplementaryGIDs = append(identity.SupplementaryGIDs, group.gid)
			}
		}
		slices.SortFunc(identity.SupplementaryGIDs, compareImageIDsV1)
		evidence.Users = append(evidence.Users, identity)
	}
	slices.SortFunc(evidence.Users, func(a, b runtimeidentity.IdentityV1) int { return compareImageIDsV1(a.UID, b.UID) })
	if err := ValidateImageIdentityEvidenceV1(evidence, policy, image, platform); err != nil {
		return fail()
	}
	return evidence, nil
}

// ValidateImageIdentityEvidenceV1 checks structure and exact policy/image binding.
// File hashes require the verified producer and enclosing immutable build lock.
func ValidateImageIdentityEvidenceV1(evidence ImageIdentityEvidenceV1, policy *blueprint.IdentityPolicyV1, image providers.RealizedImageV1, platform blueprint.Platform) error {
	digest, err := imageIdentityInputsV1(policy, image, platform)
	if err != nil {
		return err
	}
	fail := func() error { return imageIdentityErrorV1("invalid structure or binding") }
	if evidence.Schema != ImageIdentityEvidenceSchemaV1 || evidence.Image != image || evidence.Platform != platform || evidence.PolicyDigest != digest || len(evidence.Files) != 3 || len(evidence.Users) != len(policy.Users) || !reflect.DeepEqual(evidence.Groups, policy.Groups) {
		return fail()
	}
	for _, path := range imageIdentityPathsV1 {
		file, ok := evidence.Files[path]
		size, err := strconv.ParseUint(file.Size, 10, 32)
		if !ok || file.Digest.Validate() != nil || err != nil || size == 0 || size > imageIdentityFileLimitV1 || strconv.FormatUint(size, 10) != file.Size {
			return fail()
		}
	}
	for index, user := range evidence.Users {
		declared, ok := policy.Users[user.Username]
		if !ok || runtimeidentity.ValidateIdentityV1(user) != nil || user.UID != declared.UID || user.GID != policy.Groups[declared.PrimaryGroup] || index > 0 && compareImageIDsV1(evidence.Users[index-1].UID, user.UID) >= 0 {
			return fail()
		}
		extra := []string{}
		for _, name := range declared.SupplementaryGroups {
			extra = append(extra, policy.Groups[name])
		}
		slices.SortFunc(extra, compareImageIDsV1)
		if !slices.Equal(user.SupplementaryGIDs, extra) {
			return fail()
		}
	}
	return nil
}

func EncodeImageIdentityEvidenceV1(evidence ImageIdentityEvidenceV1, policy *blueprint.IdentityPolicyV1, image providers.RealizedImageV1, platform blueprint.Platform) ([]byte, error) {
	if err := ValidateImageIdentityEvidenceV1(evidence, policy, image, platform); err != nil {
		return nil, err
	}
	return canonical.Marshal(evidence)
}

func DecodeImageIdentityEvidenceV1(content []byte, policy *blueprint.IdentityPolicyV1, image providers.RealizedImageV1, platform blueprint.Platform) (ImageIdentityEvidenceV1, error) {
	fail := func() (ImageIdentityEvidenceV1, error) {
		return ImageIdentityEvidenceV1{}, imageIdentityErrorV1("invalid canonical JSON")
	}
	// The complete policy is bounded to 1,024 declarations. Evidence contains
	// only that bounded inventory and three fixed file summaries.
	if len(content) > imageIdentityFileLimitV1 {
		return fail()
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var evidence ImageIdentityEvidenceV1
	if decoder.Decode(&evidence) != nil {
		return fail()
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return fail()
	}
	encoded, err := EncodeImageIdentityEvidenceV1(evidence, policy, image, platform)
	if err != nil || !bytes.Equal(encoded, content) {
		return fail()
	}
	return evidence, nil
}

func compareImageIDsV1(a, b string) int {
	x, _ := strconv.ParseUint(a, 10, 32)
	y, _ := strconv.ParseUint(b, 10, 32)
	if x < y {
		return -1
	}
	if x > y {
		return 1
	}
	return 0
}
