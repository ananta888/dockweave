// Package mounthelper implements the narrow, fd-based protocol between the
// unprivileged dockweaved and the privileged dockweave-mountd helper.
//
// The client never sends paths for privileged resolution: it opens directory
// file descriptors (after its own allowlist and openat2 checks) and passes
// them with SCM_RIGHTS. The helper only performs the syscall on
// /proc/self/fd/<n> and refuses targets outside the state root.
package mounthelper

import (
	"encoding/json"
	"fmt"
)

// Request is one privileged mount operation.
type Request struct {
	Op        string  `json:"op"`
	Flags     uintptr `json:"flags,omitempty"`
	Recursive bool    `json:"recursive,omitempty"`
	RO        bool    `json:"ro,omitempty"`
}

// Response is the helper's answer.
type Response struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// Operations.
const (
	OpPing        = "ping"
	OpBind        = "bind"
	OpRemount     = "remount"
	OpMove        = "move"
	OpUnmount     = "unmount"
	OpPropagation = "propagation"
	OpSetReadonly = "set_readonly"
)

func encode(req Request) ([]byte, error) {
	b, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func decodeResponse(raw []byte) error {
	var resp Response
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("mountd: invalid response: %w", err)
	}
	if !resp.OK {
		if resp.Error == "" {
			resp.Error = "unknown error"
		}
		return fmt.Errorf("mountd: %s", resp.Error)
	}
	return nil
}
