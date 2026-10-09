package platform

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"strings"
	"syscall"
	"testing"
)

type failingUpstreamTransport struct{ cause error }

func (t failingUpstreamTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, t.cause
}

func TestUpstreamErrorDiagnosisWithoutCredentialLeaks(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		cause      error
	}{
		{"canceled", "已取消", context.Canceled},
		{"deadline", "超时", context.DeadlineExceeded},
		{"dns", "域名解析失败", &net.DNSError{Err: "private-error-detail", Name: "private-host", IsNotFound: true}},
		{"dns timeout", "超时", &net.DNSError{Err: "private-error-detail", IsTimeout: true}},
		{"refused", "拒绝连接", &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}},
		{"certificate", "证书校验失败", &tls.CertificateVerificationError{Err: errors.New("private-error-detail")}},
		{"other", "连接失败", errors.New("private-error-detail")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &App{upstream: &http.Client{Transport: failingUpstreamTransport{cause: tc.cause}}}
			req, _ := http.NewRequest("GET", "https://example.test/healthz?api_key=private-query-secret", nil)
			req.Header.Set("Authorization", "Bearer private-header-secret")
			_, err := a.executeUpstream(req)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error did not explain %s: %v", tc.name, err)
			}
			for _, secret := range []string{"private-query-secret", "private-header-secret", "private-error-detail", "private-host", "https://"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("upstream error exposed a request URL, credential or raw error")
				}
			}
		})
	}
}

func TestUpstreamConnectionRefusedDiagnosis(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	a, _, _ := testApp(t)
	req, _ := http.NewRequest("GET", "http://"+address+"/healthz?api_key=private-query-secret", nil)
	_, err = a.executeUpstream(req)
	if err == nil || !strings.Contains(err.Error(), "拒绝连接") || strings.Contains(err.Error(), "private-query-secret") {
		t.Fatalf("real dial failure lost its cause or leaked the request URL: %v", err)
	}
}
