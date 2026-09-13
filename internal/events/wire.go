package events

import "encoding/json"

// Protocol error codes.
const (
	CodeInvalidRequest    = "invalid_request"
	CodeInvalidParams     = "invalid_params"
	CodeUnsupportedMethod = "unsupported_method"
	CodeNotFound          = "not_found"
)

// WireError represents a protocol-level error returned by the daemon.
type WireError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *WireError) Error() string {
	return e.Code + ": " + e.Message
}

// Request is an incoming protocol frame over the socket.
type Request struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Response is an outgoing result or error frame over the socket.
type Response struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *WireError      `json:"error,omitempty"`
}
