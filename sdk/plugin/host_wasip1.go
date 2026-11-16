//go:build wasip1

package plugin

import "unsafe"

//go:wasmimport enplace call
func enplaceCall(ptr unsafe.Pointer, n uint32) uint32

//go:wasmimport enplace result
func enplaceResult(ptr unsafe.Pointer)

func hostCall(req []byte) ([]byte, error) {
	if len(req) == 0 {
		req = []byte("{}")
	}
	n := enplaceCall(unsafe.Pointer(&req[0]), uint32(len(req)))
	if n == 0 {
		return nil, ErrNoHost
	}
	out := make([]byte, n)
	enplaceResult(unsafe.Pointer(&out[0]))
	return out, nil
}
