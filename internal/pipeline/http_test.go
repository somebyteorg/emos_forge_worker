package pipeline

import (
	"net/http"
	"testing"
	"time"
)

func TestNewHTTPClientDoesNotApplyWholeDownloadTimeout(t *testing.T) {
	client := NewHTTPClient(12*time.Second, 7*time.Second)
	if client.Timeout != 0 {
		t.Fatalf("client timeout = %s, want no whole-response timeout", client.Timeout)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T", client.Transport)
	}
	if transport.ResponseHeaderTimeout != 12*time.Second {
		t.Fatalf("response header timeout = %s", transport.ResponseHeaderTimeout)
	}
}
