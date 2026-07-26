package cli

import "testing"

// TestIsTarballSHAPlaceholder covers the placeholders that resolveTarball
// must treat as "fall back to the .sha256 sidecar":
//   - empty string
//   - literal "sha256:TBD"
//   - anything ending in ":TBD" (defensive — e.g. with extra prefix)
func TestIsTarballSHAPlaceholder(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", true},
		{"sha256:TBD", true},
		{"sha256:abc:TBD", true},
		{"sha256:deadbeef", false},
		{"sha256:", false},
	}
	for _, tc := range cases {
		got := isTarballSHAPlaceholder(tc.in)
		if got != tc.want {
			t.Errorf("isTarballSHAPlaceholder(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestTrimSHA256Prefix strips the "sha256:" prefix when present and
// returns the input unchanged otherwise — used for comparing downloaded
// byte hashes against the manifest's tarball.sha256.
func TestTrimSHA256Prefix(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"sha256:deadbeef", "deadbeef"},
		{"deadbeef", "deadbeef"},
		{"sha256:", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := trimSHA256Prefix(tc.in); got != tc.want {
			t.Errorf("trimSHA256Prefix(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestHexEncode confirms the helper returns the lowercase-hex encoding of
// the input bytes (not a hash — just a hex encoder).
func TestHexEncode(t *testing.T) {
	if got := hexEncode([]byte("")); got != "" {
		t.Errorf("hexEncode(empty) = %q", got)
	}
	// Known fixture: "abc" → "616263"
	got := hexEncode([]byte("abc"))
	const want = "616263"
	if got != want {
		t.Errorf("hexEncode(\"abc\") = %q, want %q", got, want)
	}
}
