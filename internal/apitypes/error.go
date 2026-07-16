package apitypes

// Error is the standard error response shape for all API endpoints.
type Error struct {
	Error ErrorBody `json:"error"`
}

type ErrorBody struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// NewError constructs a standard error response.
func NewError(code, message string) Error {
	return Error{Error: ErrorBody{Code: code, Message: message}}
}