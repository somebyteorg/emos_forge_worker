package download

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestDownloadFreshFile(t *testing.T) {
	root := t.TempDir()
	client := &http.Client{Transport: downloadRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.Method {
		case http.MethodHead:
			return downloadResponse(http.StatusOK, nil, http.Header{"Content-Length": {"11"}, "Accept-Ranges": {"bytes"}, "ETag": {`"v1"`}}), nil
		case http.MethodGet:
			if r.Header.Get("Range") != "" {
				t.Fatalf("unexpected range request: %s", r.Header.Get("Range"))
			}
			return downloadResponse(http.StatusOK, []byte("hello world"), http.Header{"Content-Length": {"11"}, "Accept-Ranges": {"bytes"}, "ETag": {`"v1"`}}), nil
		default:
			t.Fatalf("unexpected method %s", r.Method)
			return nil, nil
		}
	})}

	result, err := (Downloader{HTTP: client}).Download(context.Background(), Request{
		URI:         "https://example.test/movie.mkv",
		PartialPath: filepath.Join(root, "input.mkv.partial"),
		FinalPath:   filepath.Join(root, "input.mkv"),
	})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if result.Bytes != 11 || result.Resumed {
		t.Fatalf("unexpected result: %+v", result)
	}
	assertFile(t, filepath.Join(root, "input.mkv"), "hello world")
	assertFileExists(t, filepath.Join(root, "input.mkv.metadata.json"))
}

func TestDownloadResumesPartialFile(t *testing.T) {
	root := t.TempDir()
	partial := filepath.Join(root, "input.mkv.partial")
	if err := os.WriteFile(partial, []byte("hello "), 0o600); err != nil {
		t.Fatalf("write partial: %v", err)
	}
	if err := writeMetadata(metadataPath(partial), Metadata{ContentLength: 11, ETag: `"v1"`, AcceptRanges: true}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	client := &http.Client{Transport: downloadRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.Method {
		case http.MethodHead:
			return downloadResponse(http.StatusOK, nil, http.Header{"Content-Length": {"11"}, "Accept-Ranges": {"bytes"}, "ETag": {`"v1"`}}), nil
		case http.MethodGet:
			if r.Header.Get("Range") != "bytes=6-" {
				t.Fatalf("Range = %q", r.Header.Get("Range"))
			}
			return downloadResponse(http.StatusPartialContent, []byte("world"), http.Header{"Content-Range": {"bytes 6-10/11"}, "Content-Length": {"5"}, "Accept-Ranges": {"bytes"}, "ETag": {`"v1"`}}), nil
		default:
			t.Fatalf("unexpected method %s", r.Method)
			return nil, nil
		}
	})}

	result, err := (Downloader{HTTP: client}).Download(context.Background(), Request{
		URI:         "https://example.test/movie.mkv",
		PartialPath: partial,
		FinalPath:   filepath.Join(root, "input.mkv"),
	})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if !result.Resumed || result.Bytes != 11 {
		t.Fatalf("unexpected result: %+v", result)
	}
	assertFile(t, filepath.Join(root, "input.mkv"), "hello world")
}

func TestDownloadMarksChangedPartialStale(t *testing.T) {
	root := t.TempDir()
	partial := filepath.Join(root, "input.mkv.partial")
	if err := os.WriteFile(partial, []byte("old"), 0o600); err != nil {
		t.Fatalf("write partial: %v", err)
	}
	if err := writeMetadata(metadataPath(partial), Metadata{ContentLength: 6, ETag: `"old"`, AcceptRanges: true}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	client := &http.Client{Transport: downloadRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.Method {
		case http.MethodHead:
			return downloadResponse(http.StatusOK, nil, http.Header{"Content-Length": {"3"}, "Accept-Ranges": {"bytes"}, "ETag": {`"new"`}}), nil
		case http.MethodGet:
			if r.Header.Get("Range") != "" {
				t.Fatalf("unexpected range request after stale partial: %s", r.Header.Get("Range"))
			}
			return downloadResponse(http.StatusOK, []byte("new"), http.Header{"Content-Length": {"3"}, "Accept-Ranges": {"bytes"}, "ETag": {`"new"`}}), nil
		default:
			t.Fatalf("unexpected method %s", r.Method)
			return nil, nil
		}
	})}

	result, err := (Downloader{HTTP: client}).Download(context.Background(), Request{
		URI:         "https://example.test/movie.mkv",
		PartialPath: partial,
		FinalPath:   filepath.Join(root, "input.mkv"),
	})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if result.StalePartial == "" || result.Resumed {
		t.Fatalf("unexpected result: %+v", result)
	}
	assertFile(t, filepath.Join(root, "input.mkv"), "new")
	assertFile(t, result.StalePartial, "old")
}

type downloadRoundTripFunc func(*http.Request) (*http.Response, error)

func (f downloadRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func downloadResponse(status int, body []byte, header http.Header) *http.Response {
	if body == nil {
		body = []byte{}
	}
	return &http.Response{
		StatusCode:    status,
		Header:        header,
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
	}
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(data) != want {
		t.Fatalf("%s = %q, want %q", path, string(data), want)
	}
}

func assertFileExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
}
