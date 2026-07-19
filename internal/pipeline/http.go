package pipeline

import (
	"net"
	"net/http"
	"time"
)

func NewHTTPClient(timeout, connectTimeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	if connectTimeout <= 0 {
		connectTimeout = timeout
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: connectTimeout, KeepAlive: 30 * time.Second}).DialContext
	transport.ResponseHeaderTimeout = timeout
	return &http.Client{Transport: transport}
}
