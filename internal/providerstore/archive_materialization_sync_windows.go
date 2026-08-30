//go:build windows

package providerstore

import "os"

func syncArchiveMaterializationDirectory(*os.Root, string) error { return nil }
