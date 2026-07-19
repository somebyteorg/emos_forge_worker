package emos

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	maxErrorBody        = 64 << 10
	defaultHTTPTimeout  = 15 * time.Second
	controlAPIAttempts  = 3
	controlRetryInitial = 500 * time.Millisecond
	controlRetryMax     = 3 * time.Second
)

type Client struct {
	baseURL  string
	token    string
	workerID string
	http     HTTPDoer
}

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type Error struct {
	Method     string
	Path       string
	StatusCode int
	Body       string
}

type transportError struct {
	err error
}

func (e transportError) Error() string { return e.err.Error() }

func (e transportError) Unwrap() error { return e.err }

func (e *Error) Error() string {
	if strings.TrimSpace(e.Body) == "" {
		return fmt.Sprintf("emos %s %s returned HTTP %d", e.Method, e.Path, e.StatusCode)
	}
	return fmt.Sprintf("emos %s %s returned HTTP %d: %s", e.Method, e.Path, e.StatusCode, e.Body)
}

func New(baseURL, token, workerID string, timeout time.Duration) (*Client, error) {
	if timeout <= 0 {
		timeout = defaultHTTPTimeout
	}
	return NewWithHTTPDoer(baseURL, token, workerID, &http.Client{Timeout: timeout})
}

func NewWithHTTPDoer(baseURL, token, workerID string, doer HTTPDoer) (*Client, error) {
	baseURL = strings.TrimSpace(baseURL)
	token = strings.TrimSpace(token)
	workerID = strings.TrimSpace(workerID)
	if doer == nil {
		return nil, fmt.Errorf("emos HTTP client is required")
	}
	if baseURL == "" {
		return nil, fmt.Errorf("EMOS_URL is required")
	}
	if token == "" {
		return nil, fmt.Errorf("EMOS_TOKEN is required")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("EMOS_URL must be an absolute http or https URL")
	}
	return &Client{
		baseURL:  strings.TrimRight(parsed.String(), "/"),
		token:    token,
		workerID: workerID,
		http:     doer,
	}, nil
}

func (c *Client) WorkerID() string {
	return c.workerID
}

func (c *Client) Heartbeat(ctx context.Context) error {
	if strings.TrimSpace(c.workerID) == "" {
		return fmt.Errorf("EMOS_FORGE_WORKER_ID is required")
	}
	path := fmt.Sprintf("/api/forge/worker/%s/heartbeat", url.PathEscape(c.workerID))
	return c.do(ctx, http.MethodPut, path, nil, nil, http.StatusNoContent)
}

type JobRef struct {
	JobUUID *string `json:"job_uuid"`
}

func (c *Client) WorkerJob(ctx context.Context, kind string) (*string, error) {
	if strings.TrimSpace(c.workerID) == "" {
		return nil, fmt.Errorf("EMOS_FORGE_WORKER_ID is required")
	}
	if kind != "current" && kind != "next" {
		return nil, fmt.Errorf("job type must be current or next")
	}
	path := fmt.Sprintf("/api/forge/worker/%s/job?type=%s", url.PathEscape(c.workerID), url.QueryEscape(kind))
	var response JobRef
	if err := c.do(ctx, http.MethodGet, path, nil, &response, http.StatusOK); err != nil {
		return nil, err
	}
	if response.JobUUID == nil || strings.TrimSpace(*response.JobUUID) == "" {
		return nil, nil
	}
	jobUUID := strings.TrimSpace(*response.JobUUID)
	return &jobUUID, nil
}

type ClaimResponse struct {
	IsSuccess bool `json:"is_success"`
}

func (c *Client) Claim(ctx context.Context, jobUUID string) (ClaimResponse, error) {
	path := fmt.Sprintf("/api/forge/job/%s/claim", url.PathEscape(jobUUID))
	var response ClaimResponse
	err := c.do(ctx, http.MethodPost, path, nil, &response, http.StatusOK)
	return response, err
}

type JobInfo struct {
	JobID     int       `json:"job_id"`
	JobStatus string    `json:"job_status"`
	JobSteps  []JobStep `json:"job_steps"`
	FileURL   *string   `json:"file_url"`
	FilePath  *string   `json:"file_path"`
}

type JobStep string

const (
	JobStepVideo720P       JobStep = "video_720p"
	JobStepVideo1080P      JobStep = "video_1080p"
	JobStepVideoPackage    JobStep = "video_package"
	JobStepAudioPackage    JobStep = "audio_package"
	JobStepAudioAAC        JobStep = "audio_aac"
	JobStepSubtitlePackage JobStep = "subtitle_package"
	JobStepSprite320       JobStep = "sprite_320"
	JobStepSprite640       JobStep = "sprite_640"
	JobStepSprite720       JobStep = "sprite_720"
)

type StepTime struct {
	Name     string  `json:"name"`
	Duration float64 `json:"duration"`
}

type completedRequest struct {
	StepTimes []StepTime `json:"step_times"`
}

func (c *Client) JobInfo(ctx context.Context, jobUUID string) (JobInfo, error) {
	return c.jobInfo(ctx, jobUUID, false)
}

func (c *Client) JobInfoWithFile(ctx context.Context, jobUUID string) (JobInfo, error) {
	return c.jobInfo(ctx, jobUUID, true)
}

func (c *Client) jobInfo(ctx context.Context, jobUUID string, withFile bool) (JobInfo, error) {
	path := fmt.Sprintf("/api/forge/job/%s/info", url.PathEscape(jobUUID))
	if withFile {
		path += "?with_file=1"
	}
	var response JobInfo
	err := c.do(ctx, http.MethodGet, path, nil, &response, http.StatusOK)
	return response, err
}

func (c *Client) Manifest(ctx context.Context, jobUUID string, manifest map[string]any) error {
	path := fmt.Sprintf("/api/forge/job/%s/manifest", url.PathEscape(jobUUID))
	return c.doJSON(ctx, http.MethodPost, path, map[string]any{"manifest": manifest}, nil, http.StatusNoContent)
}

func (c *Client) Completed(ctx context.Context, jobUUID string, stepTimes []StepTime) error {
	if stepTimes == nil {
		stepTimes = []StepTime{}
	}
	path := fmt.Sprintf("/api/forge/job/%s/completed", url.PathEscape(jobUUID))
	return c.doJSON(ctx, http.MethodPost, path, completedRequest{StepTimes: stepTimes}, nil, http.StatusNoContent)
}

func (c *Client) Failed(ctx context.Context, jobUUID, message string) error {
	path := fmt.Sprintf("/api/forge/job/%s/failed", url.PathEscape(jobUUID))
	return c.doJSON(ctx, http.MethodPost, path, map[string]string{"message_error": message}, nil, http.StatusNoContent)
}

type UploadTokensRequest struct {
	Names map[string]string `json:"names"`
}

type UploadTokenResponse struct {
	StorageType string          `json:"storage_type"`
	Data        json.RawMessage `json:"data"`
}

type TUSDTokenData struct {
	UploadURL string         `json:"upload_url"`
	Metadata  map[string]any `json:"metadata"`
	Files     []string       `json:"files"`
}

type R2TokenData struct {
	Tokens map[string]string `json:"tokens"`
}

func (c *Client) UploadTokens(ctx context.Context, jobUUID string, names []string) (map[string]UploadTokenResponse, error) {
	requestNames := make(map[string]string, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" {
			requestNames[name] = name
		}
	}
	if len(requestNames) == 0 {
		return map[string]UploadTokenResponse{}, nil
	}
	path := fmt.Sprintf("/api/forge/job/%s/getUploadToken", url.PathEscape(jobUUID))
	var response map[string]UploadTokenResponse
	err := c.doJSON(ctx, http.MethodPost, path, UploadTokensRequest{Names: requestNames}, &response, http.StatusOK)
	if response == nil {
		response = map[string]UploadTokenResponse{}
	}
	return response, err
}

func (r UploadTokenResponse) TUSDData() (TUSDTokenData, error) {
	var data TUSDTokenData
	if err := json.Unmarshal(r.Data, &data); err != nil {
		return TUSDTokenData{}, fmt.Errorf("decode tusd upload token: %w", err)
	}
	if strings.TrimSpace(data.UploadURL) == "" {
		return TUSDTokenData{}, fmt.Errorf("tusd upload_url is required")
	}
	return data, nil
}

func (r UploadTokenResponse) R2Data() (R2TokenData, error) {
	var data R2TokenData
	if err := json.Unmarshal(r.Data, &data); err != nil {
		return R2TokenData{}, fmt.Errorf("decode r2 upload token: %w", err)
	}
	if len(data.Tokens) == 0 {
		return R2TokenData{}, fmt.Errorf("r2 tokens are required")
	}
	return data, nil
}

func (c *Client) doJSON(ctx context.Context, method, path string, input any, output any, expected ...int) error {
	var body []byte
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("encode emos request: %w", err)
		}
		body = data
	}
	return c.do(ctx, method, path, body, output, expected...)
}

func (c *Client) do(ctx context.Context, method, path string, body []byte, output any, expected ...int) error {
	var last error
	for attempt := 1; attempt <= controlAPIAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := c.doOnce(ctx, method, path, body, output, expected...)
		if err == nil {
			return nil
		}
		last = err
		if attempt == controlAPIAttempts || !retryableControlError(ctx, err) {
			return err
		}
		if err := sleepRetry(ctx, controlRetryDelay(attempt)); err != nil {
			return err
		}
	}
	return last
}

func (c *Client) doOnce(ctx context.Context, method, path string, body []byte, output any, expected ...int) error {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return transportError{err: err}
	}
	defer response.Body.Close()
	if !statusAllowed(response.StatusCode, expected) {
		return c.httpError(method, path, response)
	}
	if output == nil || response.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(response.Body).Decode(output); err != nil {
		if err == io.EOF {
			return nil
		}
		return fmt.Errorf("decode emos response: %w", err)
	}
	return nil
}

func retryableControlError(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	var apiErr *Error
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusTooManyRequests || apiErr.StatusCode >= 500
	}
	var transport transportError
	return errors.As(err, &transport)
}

func controlRetryDelay(attempt int) time.Duration {
	delay := controlRetryInitial
	for i := 1; i < attempt; i++ {
		delay *= 2
	}
	if delay > controlRetryMax {
		return controlRetryMax
	}
	return delay
}

func sleepRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c *Client) httpError(method, path string, response *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(response.Body, maxErrorBody))
	return &Error{Method: method, Path: path, StatusCode: response.StatusCode, Body: readableResponseBody(body)}
}

func statusAllowed(status int, expected []int) bool {
	for _, value := range expected {
		if status == value {
			return true
		}
	}
	return false
}
