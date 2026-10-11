package doctor

// GitShellResultForTest exposes gitShellResult to the external test package.
func GitShellResultForTest(execPath string, err error) CheckResult { return gitShellResult(execPath, err) }
