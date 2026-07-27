package dockerdeploy

import "os"

var embeddedRuntimeExecutable = os.Executable

func embeddedRuntimeFileName() string {
	if currentHostPlatform().GOOS == "windows" {
		return ToolBinaryFileName + ".exe"
	}
	return ToolBinaryFileName
}
