//go:build !windows

package dockerdeploy

func stagedRemovalMustUnlockBeforeRename() bool { return false }
