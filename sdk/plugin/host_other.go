//go:build !wasip1

package plugin

// HostFunc stands in for Enplace outside the sandbox, so logic can be
// tested with plain go test. It gets and returns the host call's JSON.
var HostFunc func(req []byte) []byte

func hostCall(req []byte) ([]byte, error) {
	if HostFunc == nil {
		return nil, ErrNoHost
	}
	return HostFunc(req), nil
}
