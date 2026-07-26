package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestBasicAuthMiddleware_AcceptsCorrectPassword confirms the middleware
// passes a request through when the supplied password matches.
func TestBasicAuthMiddleware_AcceptsCorrectPassword(t *testing.T) {
	const pw = "s3cret"
	called := false
	h := BasicAuthMiddleware(pw)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth("agentpkg", pw)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if !called {
		t.Error("handler not invoked on correct password")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rr.Code)
	}
}

// TestBasicAuthMiddleware_RejectsWrongOrMissing covers every auth-failure
// path: no header, wrong password, user set but empty password. All must
// return 401 with WWW-Authenticate so curl/agents know to retry with creds.
func TestBasicAuthMiddleware_RejectsWrongOrMissing(t *testing.T) {
	cases := []struct {
		name       string
		user, pass string
		header     bool
	}{
		{"no-header", "", "", false},
		{"wrong-password", "agentpkg", "wrong", true},
		{"empty-password", "agentpkg", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := BasicAuthMiddleware("real-password")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("handler should not be reached")
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.header {
				req.SetBasicAuth(tc.user, tc.pass)
			}
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != http.StatusUnauthorized {
				t.Errorf("status: got %d, want 401", rr.Code)
			}
			if rr.Header().Get("WWW-Authenticate") == "" {
				t.Error("missing WWW-Authenticate header")
			}
			// Body should be a parseable apitypes.Error JSON.
			var body struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
				t.Errorf("error body not JSON: %v", err)
			}
			if body.Error.Code == "" {
				t.Error("error.code is empty")
			}
		})
	}
}

// TestWriteError_HasStructuredShape confirms that writeError produces
// the standard {error: {code, message}} envelope — the CLI asserts on
// this shape when surfacing API failures to humans.
func TestWriteError_HasStructuredShape(t *testing.T) {
	rr := httptest.NewRecorder()
	writeError(rr, http.StatusBadRequest, "bad_request", "missing field x")
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("content-type: %q", got)
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error.Code != "bad_request" || body.Error.Message != "missing field x" {
		t.Errorf("body: %+v", body.Error)
	}
}
