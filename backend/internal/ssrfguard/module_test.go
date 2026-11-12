// Consolidated source; original file boundaries are marked below.
package ssrfguard

import (
	testing "testing"
)

// ---- Source: backend/internal/ssrfguard/ssrfguard_test.go ----
func TestValidateOutboundURLBlocksPrivate(t *testing.T) {
	cases := []string{
		"http://127.0.0.1/",
		"http://localhost/admin",
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.1/",
	}
	for _, u := range cases {
		if err := ValidateOutboundURL(u); err == nil {
			t.Fatalf("expected blocked url: %s", u)
		}
	}
}
func TestValidateOutboundURLAllowsPublic(t *testing.T) {
	if err := ValidateOutboundURL("https://example.com/path"); err != nil {
		t.Fatalf("expected public url allowed: %v", err)
	}
}
func TestValidateDialHostBlocksPrivate(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "localhost", "169.254.169.254", "10.0.0.1"} {
		if err := ValidateDialHost(host); err == nil {
			t.Fatalf("expected blocked dial host: %s", host)
		}
	}
}
func TestValidateDialHostAllowsPublic(t *testing.T) {
	if err := ValidateDialHost("example.com"); err != nil {
		t.Fatalf("expected public host allowed: %v", err)
	}
}
