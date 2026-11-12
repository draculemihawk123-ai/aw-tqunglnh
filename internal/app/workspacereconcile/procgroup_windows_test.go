package workspacereconcile_test

import "syscall"

// ownProcessGroup has no Windows equivalent here: its only caller skips on Windows, and this keeps the package compiling.
func ownProcessGroup() *syscall.SysProcAttr { return nil }
