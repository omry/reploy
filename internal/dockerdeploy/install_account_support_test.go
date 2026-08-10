package dockerdeploy

import (
	"os/user"
	"reflect"
	"strings"
	"testing"
)

func TestResolveInstallOwnerCarriesCanonicalSupplementaryGroups(t *testing.T) {
	originalLookupUser := installLookupUser
	originalLookupGroup := installLookupGroup
	originalLookupGroups := installLookupUserGroupIDs
	t.Cleanup(func() {
		installLookupUser = originalLookupUser
		installLookupGroup = originalLookupGroup
		installLookupUserGroupIDs = originalLookupGroups
	})
	installLookupUser = func(name string) (*user.User, error) {
		return &user.User{Username: name, Uid: "991", Gid: "992"}, nil
	}
	installLookupGroup = func(name string) (*user.Group, error) { return &user.Group{Name: name, Gid: "992"}, nil }
	installLookupUserGroupIDs = func(*user.User) ([]string, error) {
		return []string{"44", "992", "33", "44"}, nil
	}

	owner, err := resolveInstallOwner(map[string]string{reployInstallOwnerEnv: "service:service"})
	if err != nil {
		t.Fatal(err)
	}
	if owner.UID != 991 || owner.GID != 992 || !reflect.DeepEqual(owner.SupplementaryGIDs, []uint32{33, 44}) {
		t.Fatalf("owner = %#v", owner)
	}
}

func TestResolveInstallOwnerRejectsRootSupplementaryGroup(t *testing.T) {
	originalLookupUser := installLookupUser
	originalLookupGroup := installLookupGroup
	originalLookupGroups := installLookupUserGroupIDs
	t.Cleanup(func() {
		installLookupUser = originalLookupUser
		installLookupGroup = originalLookupGroup
		installLookupUserGroupIDs = originalLookupGroups
	})
	installLookupUser = func(name string) (*user.User, error) {
		return &user.User{Username: name, Uid: "991", Gid: "992"}, nil
	}
	installLookupGroup = func(name string) (*user.Group, error) { return &user.Group{Name: name, Gid: "992"}, nil }
	installLookupUserGroupIDs = func(*user.User) ([]string, error) { return []string{"0", "992"}, nil }

	_, err := resolveInstallOwner(map[string]string{reployInstallOwnerEnv: "service:service"})
	if err == nil || !strings.Contains(err.Error(), "root group") {
		t.Fatalf("error = %v", err)
	}
}
