package app

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"forge_worker/internal/config"
	"forge_worker/internal/emos"
)

func TestUploadCompletedEMOSJobCleansTmpOnlyAfterCompletedAccepted(t *testing.T) {
	tests := []struct {
		name            string
		completedStatus int
		wantError       bool
		wantTmp         bool
	}{
		{name: "completed accepted", completedStatus: http.StatusNoContent},
		{name: "completed rejected", completedStatus: http.StatusInternalServerError, wantError: true, wantTmp: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			tmpFile := filepath.Join(root, "tmp", "pipeline_state.json")
			if err := os.MkdirAll(filepath.Dir(tmpFile), 0o700); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}
			if err := os.WriteFile(tmpFile, []byte("checkpoint"), 0o600); err != nil {
				t.Fatalf("WriteFile checkpoint: %v", err)
			}
			if err := os.WriteFile(filepath.Join(root, "manifest.json"), []byte(`{"schema_version":1}`), 0o600); err != nil {
				t.Fatalf("WriteFile manifest: %v", err)
			}

			doer := &completedCleanupDoer{t: t, tmpFile: tmpFile, completedStatus: tt.completedStatus}
			client, err := emos.NewWithHTTPDoer("https://emos.test", "token", "worker", doer)
			if err != nil {
				t.Fatalf("emos.New: %v", err)
			}
			var output bytes.Buffer
			err = uploadCompletedEMOSJob(context.Background(), config.Config{}, client, "job-1", root, nil, &output)
			if (err != nil) != tt.wantError {
				t.Fatalf("uploadCompletedEMOSJob error = %v, wantError=%t", err, tt.wantError)
			}
			if !doer.completedCalled {
				t.Fatal("completed endpoint was not called")
			}
			_, statErr := os.Stat(filepath.Join(root, "tmp"))
			if tt.wantTmp && statErr != nil {
				t.Fatalf("tmp should be retained, stat error: %v", statErr)
			}
			if !tt.wantTmp && !os.IsNotExist(statErr) {
				t.Fatalf("tmp should be removed after completed, stat error: %v", statErr)
			}
		})
	}
}

type completedCleanupDoer struct {
	t               *testing.T
	tmpFile         string
	completedStatus int
	completedCalled bool
}

func (d *completedCleanupDoer) Do(request *http.Request) (*http.Response, error) {
	status := http.StatusNoContent
	if filepath.Base(request.URL.Path) == "completed" {
		d.completedCalled = true
		if _, err := os.Stat(d.tmpFile); err != nil {
			d.t.Errorf("tmp was removed before completed response: %v", err)
		}
		status = d.completedStatus
	}
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(nil)),
		Request:    request,
	}, nil
}
