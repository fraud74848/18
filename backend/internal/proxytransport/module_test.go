// Consolidated source; original file boundaries are marked below.
package proxytransport

import (
	context "context"
	net "net"
	http "net/http"
	testing "testing"
	time "time"
)

// ---- Source: backend/internal/proxytransport/client_test.go ----
func TestNormalizeProxyURL_NonStandardSOCKS(t *testing.T) {
	got, err := NormalizeProxyURL("sock5://216.238.102.241:1234:admin:admin520")
	if err != nil {
		t.Fatal(err)
	}
	want := "socks5://admin:admin520@216.238.102.241:1234"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
func TestNormalizeProxyURL_StandardSOCKS(t *testing.T) {
	got, err := NormalizeProxyURL("socks5://admin:admin520@216.238.102.241:1234")
	if err != nil {
		t.Fatal(err)
	}
	if got != "socks5://admin:admin520@216.238.102.241:1234" {
		t.Fatalf("unexpected: %s", got)
	}
}
func TestSafeDialContextBlocksLoopback(t *testing.T) {
	dial := SafeDialContext(&net.Dialer{Timeout: 2 * time.Second})
	_, err := dial(context.Background(), "tcp", "127.0.0.1:80")
	if err == nil {
		t.Fatal("expected dial to 127.0.0.1 blocked")
	}
}
func TestSafeDialContextBlocksMetadataIP(t *testing.T) {
	dial := SafeDialContext(&net.Dialer{Timeout: 2 * time.Second})
	_, err := dial(context.Background(), "tcp", "169.254.169.254:80")
	if err == nil {
		t.Fatal("expected dial to metadata IP blocked")
	}
}
func TestSafeCheckRedirectBlocksPrivate(t *testing.T) {
	fn := SafeCheckRedirect(10)
	req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1/", nil)
	if err != nil {
		t.Fatal(err)
	}
	via := []*http.Request{req}
	if err := fn(req, via); err == nil {
		t.Fatal("expected redirect to loopback blocked")
	}
}
func TestNewHTTPClientHasSafeRedirect(t *testing.T) {
	c := NewHTTPClient(2*time.Second, "")
	if c.CheckRedirect == nil {
		t.Fatal("CheckRedirect must be set")
	}
	req, _ := http.NewRequest(http.MethodGet, "http://169.254.169.254/latest/meta-data/", nil)
	if err := c.CheckRedirect(req, []*http.Request{req}); err == nil {
		t.Fatal("client CheckRedirect must block metadata")
	}
}
