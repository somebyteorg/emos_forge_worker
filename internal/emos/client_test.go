package emos

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
)

func TestWorkerJobUsesGETQueryAndWorkerUserAgent(t *testing.T) {
	doer := roundTripDoer(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", request.Method)
		}
		if request.URL.Path != "/api/forge/worker/worker-1/job" {
			t.Fatalf("path = %q", request.URL.Path)
		}
		if request.URL.Query().Get("type") != "next" {
			t.Fatalf("type query = %q", request.URL.Query().Get("type"))
		}
		if request.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("Authorization = %q", request.Header.Get("Authorization"))
		}
		if request.Header.Get("User-Agent") != "emos-forge-worker" {
			t.Fatalf("User-Agent = %q", request.Header.Get("User-Agent"))
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewBufferString(`{"job_uuid":"job-1"}`)),
		}, nil
	})
	client, err := NewWithHTTPDoer("https://emos.test", "token", "worker-1", doer)
	if err != nil {
		t.Fatalf("NewWithHTTPDoer: %v", err)
	}

	jobUUID, err := client.WorkerJob(context.Background(), "next")
	if err != nil {
		t.Fatalf("WorkerJob: %v", err)
	}
	if jobUUID == nil || *jobUUID != "job-1" {
		t.Fatalf("job UUID = %v", jobUUID)
	}
}

type roundTripDoer func(*http.Request) (*http.Response, error)

func (f roundTripDoer) Do(request *http.Request) (*http.Response, error) {
	return f(request)
}
