package deploy

import "runtime"

func hasPOSIXPermissionBits() bool { return runtime.GOOS != "windows" }
