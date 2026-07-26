package server

import (
	"encoding/json"
	"io"
	"net/http"
)

// writeJSON writes v as JSON with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes a standard error response.
func writeError(w http.ResponseWriter, status int, code, msg string) {
	type errBody struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	body := errBody{}
	body.Error.Code = code
	body.Error.Message = msg
	writeJSON(w, status, body)
}

// jsonDecoder returns a decoder that rejects unknown fields.
func jsonDecoder(r io.Reader) *json.Decoder {
	return json.NewDecoder(r)
}
