package manifest

import (
	"strings"
	"testing"
)

// TestValidateLogo_Acceptance pins the three documented formats. Anything
// outside this set must be rejected with a clear error.
func TestValidateLogo_Acceptance(t *testing.T) {
	cases := []struct {
		name string
		in   string
		ok   bool
	}{
		{"empty", "", true},
		{"http", "http://example.com/logo.svg", true},
		{"https", "https://example.com/logo.png", true},
		{"https with path and query", "https://cdn.example.com/v1/logo.svg?x=1", true},

		{"data svg", "data:image/svg+xml;base64,PHN2Zz48L3N2Zz4=", true},
		{"data png", "data:image/png;base64,iVBORw0KGgo=", true},
		{"data jpeg", "data:image/jpeg;base64,/9j/4AAQ", true},
		{"data webp", "data:image/webp;base64,UklGRg==", true},
		{"data gif", "data:image/gif;base64,R0lGODdh", true},

		// rejections
		{"plain identifier (was valid under old catalog)", "robot", false},
		{"data without base64", "data:image/svg+xml,plain", false},
		{"data non-image mime", "data:text/plain;base64,Zm9v", false},
		{"data empty payload", "data:image/svg+xml;base64,", false},
		{"ftp url", "ftp://example.com/logo.svg", false},
		{"url without host", "https:///path", false},
		{"junk string", "lucide:code", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateLogo(c.in)
			if c.ok && err != nil {
				t.Errorf("validateLogo(%q) = %v, want nil", c.in, err)
			}
			if !c.ok && err == nil {
				t.Errorf("validateLogo(%q) = nil, want error", c.in)
			}
		})
	}
}

// TestValidateLogo_ErrorMessages checks the error string contains the bad
// value, so authors can find their typo without re-reading the source.
func TestValidateLogo_ErrorMessages(t *testing.T) {
	err := validateLogo("robot")
	if err == nil || !strings.Contains(err.Error(), `"robot"`) {
		t.Fatalf("expected error mentioning the bad value, got %v", err)
	}
	err = validateLogo("data:image/svg+xml,plain")
	if err == nil || !strings.Contains(err.Error(), "data:image/<mime>;base64,<payload>") {
		t.Fatalf("expected error mentioning the data URL format, got %v", err)
	}
}
