//go:build !unix

package fsowner

import "os"

func FromInfo(os.FileInfo) Owner        { return Owner{} }
func sameOwner(os.FileInfo, Owner) bool { return true }
func multipleLinks(os.FileInfo) bool    { return false }
func removedDirectory(os.FileInfo) bool { return false }
