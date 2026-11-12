//go:build windows

package secretkey

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// platformProvider on Windows: the key file holds a DPAPI (CurrentUser) blob — only this Windows user on this machine can read it.
func platformProvider(root string) ports.SecretKeyProvider {
	p := NewFileProvider(root)
	p.Path += ".dpapi"
	p.kind = "dpapi"
	p.protect, p.unprotect = dpapiProtect, dpapiUnprotect
	return &cached{inner: p}
}

func blob(b []byte) *windows.DataBlob {
	if len(b) == 0 {
		return &windows.DataBlob{}
	}
	return &windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
}

func takeBlob(out *windows.DataBlob) []byte {
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...)
}

func dpapiProtect(plain []byte) ([]byte, error) {
	var out windows.DataBlob
	if err := windows.CryptProtectData(blob(plain), windows.StringToUTF16Ptr(service), nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, fmt.Errorf("CryptProtectData: %w", err)
	}
	return takeBlob(&out), nil
}

func dpapiUnprotect(protected []byte) ([]byte, error) {
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(blob(protected), nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, fmt.Errorf("CryptUnprotectData: %w", err)
	}
	return takeBlob(&out), nil
}
