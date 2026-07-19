package emos

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bdragon300/tusgo"
	"golang.org/x/sync/errgroup"
)

type Uploader struct {
	Client *Client
	HTTP   HTTPDoer
	Output io.Writer
}

type UploadOptions struct {
	Root            string
	JobUUID         string
	Manifest        map[string]any
	StepTimes       []StepTime
	Concurrency     int
	RetryMax        int
	ChunkSizeBytes  int64
	DeleteArtifacts bool
}

type uploadState struct {
	Media map[string]uploadMediaState `json:"media"`
}

type uploadMediaState struct {
	StorageType   string                     `json:"storage_type,omitempty"`
	Completed     bool                       `json:"completed"`
	Attempts      int                        `json:"attempts,omitempty"`
	SizeBytes     int64                      `json:"size_bytes,omitempty"`
	UploadedBytes int64                      `json:"uploaded_bytes,omitempty"`
	LastError     string                     `json:"last_error,omitempty"`
	UpdatedAt     string                     `json:"updated_at,omitempty"`
	Files         map[string]uploadFileState `json:"files,omitempty"`
}

type uploadFileState struct {
	StorageType string `json:"storage_type,omitempty"`
	UploadURL   string `json:"-"`
	Completed   bool   `json:"completed"`
	Attempts    int    `json:"attempts,omitempty"`
	SizeBytes   int64  `json:"size_bytes,omitempty"`
	LastError   string `json:"last_error,omitempty"`
	UpdatedAt   string `json:"updated_at,omitempty"`
}

type uploadTask struct {
	StorageType string
	MediaID     string
	Path        string
	URL         string
	Metadata    map[string]any
	SizeBytes   int64
}

type permanentUploadError struct {
	err error
}

func (e permanentUploadError) Error() string { return e.err.Error() }

func (e permanentUploadError) Unwrap() error { return e.err }

func (u Uploader) UploadManifestAssets(ctx context.Context, opt UploadOptions) error {
	if u.Client == nil {
		return fmt.Errorf("emos client is required")
	}
	if u.HTTP == nil {
		u.HTTP = &http.Client{Timeout: 0}
	}
	if opt.Concurrency <= 0 {
		opt.Concurrency = 10
	}
	if opt.RetryMax <= 0 {
		opt.RetryMax = 3
	}
	if opt.ChunkSizeBytes <= 0 || opt.ChunkSizeBytes > 100<<20 {
		opt.ChunkSizeBytes = 100 << 20
	}
	if opt.Root == "" || opt.JobUUID == "" {
		return fmt.Errorf("upload root and job UUID are required")
	}
	state, err := loadUploadState(opt.Root)
	if err != nil {
		return err
	}
	mediaIDs := manifestMediaIDs(opt.Manifest)
	if len(mediaIDs) == 0 {
		u.logf("no manifest media assets require upload\n")
		if err := u.Client.Completed(ctx, opt.JobUUID, opt.StepTimes); err != nil {
			return err
		}
		u.logf("job %s completed endpoint accepted\n", opt.JobUUID)
		return nil
	}
	tasks, err := u.uploadTasks(ctx, opt.Root, opt.JobUUID, mediaIDs)
	if err != nil {
		return err
	}
	if len(tasks) == 0 {
		u.logf("upload token responses contained no files\n")
		if err := u.Client.Completed(ctx, opt.JobUUID, opt.StepTimes); err != nil {
			return err
		}
		u.logf("job %s completed endpoint accepted\n", opt.JobUUID)
		return nil
	}
	initializeUploadStateForTasks(&state, tasks)
	summary := summarizeUploadTasks(tasks)
	u.logf("upload plan job=%s media=%d files=%d size=%s concurrency=%d retry_max=%d\n", opt.JobUUID, summary.MediaCount, summary.FileCount, humanBytes(summary.SizeBytes), opt.Concurrency, opt.RetryMax)
	for _, media := range summary.Media {
		u.logf("upload media_id=%s storage=%s files=%d size=%s\n", media.MediaID, media.StorageType, media.FileCount, humanBytes(media.SizeBytes))
	}
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(opt.Concurrency)
	var mu sync.Mutex
	for _, task := range tasks {
		task := task
		group.Go(func() error {
			mu.Lock()
			mediaState := state.Media[task.MediaID]
			fileState := mediaState.Files[task.Path]
			if fileState.Completed && fileState.SizeBytes == task.SizeBytes {
				mu.Unlock()
				u.logf("upload skip media_id=%s file=%s already completed\n", task.MediaID, task.Path)
				return nil
			}
			mu.Unlock()
			err := u.uploadWithRetry(groupCtx, opt.Root, opt.RetryMax, opt.ChunkSizeBytes, task, &fileState)
			mu.Lock()
			mediaState = state.Media[task.MediaID]
			if mediaState.Files == nil {
				mediaState.Files = map[string]uploadFileState{}
			}
			if err == nil {
				fileState.StorageType = task.StorageType
				fileState.Completed = true
				fileState.SizeBytes = task.SizeBytes
				fileState.LastError = ""
				fileState.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			} else {
				fileState.StorageType = task.StorageType
				fileState.Completed = false
				fileState.SizeBytes = task.SizeBytes
				fileState.LastError = uploadErrorMessage(err)
				fileState.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			}
			mediaState.Files[task.Path] = fileState
			recomputeMediaState(&mediaState)
			state.Media[task.MediaID] = mediaState
			if err == nil {
				err = saveUploadState(opt.Root, state)
			} else {
				_ = saveUploadState(opt.Root, state)
			}
			mu.Unlock()
			return err
		})
	}
	if err := group.Wait(); err != nil {
		return err
	}
	u.logf("upload assets completed job=%s files=%d size=%s\n", opt.JobUUID, summary.FileCount, humanBytes(summary.SizeBytes))
	if err := u.Client.Completed(ctx, opt.JobUUID, opt.StepTimes); err != nil {
		return err
	}
	u.logf("job %s completed endpoint accepted\n", opt.JobUUID)
	if opt.DeleteArtifacts {
		u.logf("job %s cleanup uploaded artifacts root=%s\n", opt.JobUUID, opt.Root)
		if err := cleanupUploadedArtifacts(opt.Root); err != nil {
			return err
		}
		u.logf("job %s cleanup completed\n", opt.JobUUID)
	}
	return nil
}

func (u Uploader) uploadTasks(ctx context.Context, root, jobUUID string, mediaIDs []string) ([]uploadTask, error) {
	var tasks []uploadTask
	tokens, err := u.Client.UploadTokens(ctx, jobUUID, mediaIDs)
	if err != nil {
		return nil, fmt.Errorf("get upload tokens: %w", err)
	}
	for _, mediaID := range mediaIDs {
		token, ok := tokens[mediaID]
		if !ok {
			return nil, fmt.Errorf("upload token response missing media_id %s", mediaID)
		}
		switch strings.ToLower(strings.TrimSpace(token.StorageType)) {
		case "tusd":
			data, err := token.TUSDData()
			if err != nil {
				return nil, err
			}
			u.logf("upload token media_id=%s storage=tusd files=%d\n", mediaID, len(data.Files))
			for _, item := range data.Files {
				task, err := newUploadTask(root, "tusd", mediaID, item, data.UploadURL, data.Metadata)
				if err != nil {
					return nil, err
				}
				tasks = append(tasks, task)
			}
		case "r2":
			data, err := token.R2Data()
			if err != nil {
				return nil, err
			}
			u.logf("upload token media_id=%s storage=r2 files=%d\n", mediaID, len(data.Tokens))
			paths := make([]string, 0, len(data.Tokens))
			for path := range data.Tokens {
				paths = append(paths, path)
			}
			sort.Strings(paths)
			for _, path := range paths {
				task, err := newUploadTask(root, "r2", mediaID, path, data.Tokens[path], nil)
				if err != nil {
					return nil, err
				}
				tasks = append(tasks, task)
			}
		default:
			return nil, fmt.Errorf("unsupported upload storage_type %q for %s", token.StorageType, mediaID)
		}
	}
	return tasks, nil
}

func newUploadTask(root, storageType, mediaID, relativePath, uploadURL string, metadata map[string]any) (uploadTask, error) {
	relativePath = filepath.ToSlash(filepath.Clean(filepath.FromSlash(relativePath)))
	if relativePath == "." || strings.HasPrefix(relativePath, "../") || filepath.IsAbs(relativePath) {
		return uploadTask{}, fmt.Errorf("upload file path %q escapes task root", relativePath)
	}
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(relativePath)))
	if err != nil {
		return uploadTask{}, fmt.Errorf("stat upload file %s: %w", relativePath, err)
	}
	if !info.Mode().IsRegular() {
		return uploadTask{}, fmt.Errorf("upload file %s is not a regular file", relativePath)
	}
	return uploadTask{StorageType: storageType, MediaID: mediaID, Path: relativePath, URL: uploadURL, Metadata: metadata, SizeBytes: info.Size()}, nil
}

func (u Uploader) uploadWithRetry(ctx context.Context, root string, retryMax int, chunkSize int64, task uploadTask, state *uploadFileState) error {
	var last error
	for attempt := 1; attempt <= retryMax; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		state.Attempts++
		u.logf("upload %s attempt %d/%d via %s\n", task.Path, attempt, retryMax, task.StorageType)
		switch task.StorageType {
		case "tusd":
			last = u.uploadTUSD(ctx, root, chunkSize, task, state)
		case "r2":
			last = u.uploadR2(ctx, root, task)
		default:
			last = fmt.Errorf("unsupported upload storage_type %q", task.StorageType)
		}
		if last == nil {
			return nil
		}
		if isPermanentUploadError(last) {
			return last
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if attempt < retryMax {
			sleep := time.Duration(attempt*attempt) * time.Second
			u.logf("upload %s failed: %v; retrying in %s\n", task.Path, last, sleep)
			timer := time.NewTimer(sleep)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	return fmt.Errorf("upload %s failed after %d attempts: %w", task.Path, retryMax, last)
}

func initializeUploadStateForTasks(state *uploadState, tasks []uploadTask) {
	if state.Media == nil {
		state.Media = map[string]uploadMediaState{}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, task := range tasks {
		mediaState := state.Media[task.MediaID]
		if mediaState.Files == nil {
			mediaState.Files = map[string]uploadFileState{}
		}
		fileState := mediaState.Files[task.Path]
		if fileState.SizeBytes != task.SizeBytes {
			fileState.Completed = false
			fileState.Attempts = 0
			fileState.LastError = ""
		}
		fileState.StorageType = task.StorageType
		fileState.SizeBytes = task.SizeBytes
		if fileState.UpdatedAt == "" {
			fileState.UpdatedAt = now
		}
		mediaState.Files[task.Path] = fileState
		mediaState.StorageType = task.StorageType
		recomputeMediaState(&mediaState)
		state.Media[task.MediaID] = mediaState
	}
}

func recomputeMediaState(media *uploadMediaState) {
	media.Completed = len(media.Files) > 0
	media.Attempts = 0
	media.SizeBytes = 0
	media.UploadedBytes = 0
	media.LastError = ""
	media.UpdatedAt = ""
	for _, file := range media.Files {
		media.Attempts += file.Attempts
		media.SizeBytes += file.SizeBytes
		if file.Completed {
			media.UploadedBytes += file.SizeBytes
		} else {
			media.Completed = false
			if media.LastError == "" {
				media.LastError = file.LastError
			}
		}
		if file.UpdatedAt > media.UpdatedAt {
			media.UpdatedAt = file.UpdatedAt
		}
	}
}

type uploadPlanSummary struct {
	MediaCount int
	FileCount  int
	SizeBytes  int64
	Media      []uploadMediaPlan
}

type uploadMediaPlan struct {
	MediaID     string
	StorageType string
	FileCount   int
	SizeBytes   int64
}

func summarizeUploadTasks(tasks []uploadTask) uploadPlanSummary {
	byMedia := map[string]*uploadMediaPlan{}
	var summary uploadPlanSummary
	for _, task := range tasks {
		summary.FileCount++
		summary.SizeBytes += task.SizeBytes
		media := byMedia[task.MediaID]
		if media == nil {
			media = &uploadMediaPlan{MediaID: task.MediaID, StorageType: task.StorageType}
			byMedia[task.MediaID] = media
		}
		if media.StorageType == "" {
			media.StorageType = task.StorageType
		}
		media.FileCount++
		media.SizeBytes += task.SizeBytes
	}
	ids := make([]string, 0, len(byMedia))
	for mediaID := range byMedia {
		ids = append(ids, mediaID)
	}
	sort.Strings(ids)
	summary.MediaCount = len(ids)
	for _, mediaID := range ids {
		summary.Media = append(summary.Media, *byMedia[mediaID])
	}
	return summary
}

func (u Uploader) uploadR2(ctx context.Context, root string, task uploadTask) error {
	path := filepath.Join(root, filepath.FromSlash(task.Path))
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	progress := newProgressReader(file, task.Path, task.SizeBytes, 0, u.Output)
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, task.URL, progress)
	if err != nil {
		return err
	}
	request.ContentLength = task.SizeBytes
	response, err := u.HTTP.Do(request)
	if err != nil {
		return fmt.Errorf("r2 upload %s request failed: %s", task.Path, redactUploadURLs(err.Error()))
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, maxErrorBody))
		return fmt.Errorf("r2 upload returned HTTP %d: %s", response.StatusCode, readableResponseBody(body))
	}
	progress.finish()
	return nil
}

func (u Uploader) uploadTUSD(ctx context.Context, root string, chunkSize int64, task uploadTask, state *uploadFileState) error {
	client, err := u.tusClient(ctx, task.URL)
	if err != nil {
		return err
	}
	upload := tusgo.Upload{
		Location:   state.UploadURL,
		RemoteSize: task.SizeBytes,
		Metadata:   tusMetadata(task),
	}
	created := false
	if upload.Location == "" {
		response, err := client.CreateUpload(&upload, task.SizeBytes, false, upload.Metadata)
		if err != nil {
			return tusPermanentError("create", err, response)
		}
		if strings.TrimSpace(upload.Location) == "" {
			return permanentUploadError{err: fmt.Errorf("tusd create response missing Location")}
		}
		location, err := resolveTUSLocation(task.URL, upload.Location)
		if err != nil {
			return err
		}
		upload.Location = location
		state.UploadURL = location
		created = true
	}
	stream := tusgo.NewUploadStream(client, &upload)
	stream.ChunkSize = chunkSize
	if !created {
		response, err := stream.Sync()
		if err != nil {
			return tusPermanentError("resume check", err, response)
		}
	}
	offset := stream.Tell()
	if offset < 0 || offset > task.SizeBytes {
		return permanentUploadError{err: fmt.Errorf("tusd remote offset %d is outside file size %d for %s", offset, task.SizeBytes, task.Path)}
	}
	path := filepath.Join(root, filepath.FromSlash(task.Path))
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	if offset > 0 {
		if _, err := file.Seek(offset, io.SeekStart); err != nil {
			return err
		}
	}
	progress := newProgressReader(file, task.Path, task.SizeBytes, offset, u.Output)
	if _, err := io.Copy(stream, progress); err != nil {
		return tusPermanentError("patch", err, stream.LastResponse)
	}
	if stream.Tell() != task.SizeBytes {
		return permanentUploadError{err: fmt.Errorf("tusd uploaded %d bytes for %s, expected %d", stream.Tell(), task.Path, task.SizeBytes)}
	}
	progress.finish()
	return nil
}

func (u Uploader) tusClient(ctx context.Context, uploadURL string) (*tusgo.Client, error) {
	baseURL, err := url.Parse(uploadURL)
	if err != nil {
		return nil, err
	}
	client := tusgo.NewClient(tusHTTPClient(u.HTTP), baseURL).WithContext(ctx)
	client.Capabilities = &tusgo.ServerCapabilities{Extensions: []string{"creation"}}
	return client, nil
}

func tusHTTPClient(doer HTTPDoer) *http.Client {
	if client, ok := doer.(*http.Client); ok {
		return client
	}
	return &http.Client{Transport: doerRoundTripper{doer: doer}}
}

type doerRoundTripper struct {
	doer HTTPDoer
}

func (t doerRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return t.doer.Do(request)
}

func resolveTUSLocation(baseURL, location string) (string, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	ref, err := url.Parse(location)
	if err != nil {
		return "", err
	}
	return base.ResolveReference(ref).String(), nil
}

func tusPermanentError(operation string, err error, response *http.Response) error {
	if err == nil {
		return nil
	}
	if response != nil || tusErrorIsPermanent(err) {
		return permanentUploadError{err: fmt.Errorf("tusd %s failed: %s", operation, tusErrorMessage(err, response))}
	}
	return err
}

func tusErrorIsPermanent(err error) bool {
	return errors.Is(err, tusgo.ErrUnexpectedResponse) ||
		errors.Is(err, tusgo.ErrUnsupportedFeature) ||
		errors.Is(err, tusgo.ErrUploadTooLarge) ||
		errors.Is(err, tusgo.ErrUploadDoesNotExist) ||
		errors.Is(err, tusgo.ErrOffsetsNotSynced) ||
		errors.Is(err, tusgo.ErrProtocol) ||
		errors.Is(err, tusgo.ErrCannotUpload)
}

func tusErrorMessage(err error, response *http.Response) string {
	if response == nil {
		return err.Error()
	}
	body, _ := io.ReadAll(io.LimitReader(response.Body, maxErrorBody))
	bodyText := readableResponseBody(body)
	if bodyText != "" {
		return fmt.Sprintf("HTTP %d: %s", response.StatusCode, bodyText)
	}
	return fmt.Sprintf("HTTP %d: %s", response.StatusCode, err)
}

func uploadErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	return redactUploadURLs(err.Error())
}

var uploadURLPattern = regexp.MustCompile(`https?://[^\s"']+`)

func redactUploadURLs(message string) string {
	return uploadURLPattern.ReplaceAllStringFunc(message, func(raw string) string {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.RawQuery == "" {
			return raw
		}
		parsed.RawQuery = "redacted"
		return parsed.String()
	})
}

func isPermanentUploadError(err error) bool {
	var permanent permanentUploadError
	return errors.As(err, &permanent)
}

func tusMetadata(task uploadTask) map[string]string {
	values := make(map[string]string, len(task.Metadata)+1)
	for key, value := range task.Metadata {
		values[key] = fmt.Sprint(value)
	}
	values["file_path"] = task.Path
	return values
}

type progressReader struct {
	reader     io.Reader
	name       string
	total      int64
	done       int64
	start      time.Time
	lastReport time.Time
	output     io.Writer
}

func newProgressReader(reader io.Reader, name string, total, offset int64, output io.Writer) *progressReader {
	now := time.Now()
	return &progressReader{reader: reader, name: name, total: total, done: offset, start: now, lastReport: now, output: output}
}

func (r *progressReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.done += int64(n)
		r.report(false)
	}
	return n, err
}

func (r *progressReader) finish() {
	r.done = r.total
	r.report(true)
}

func (r *progressReader) report(force bool) {
	if r.output == nil {
		return
	}
	now := time.Now()
	if !force && now.Sub(r.lastReport) < time.Second {
		return
	}
	r.lastReport = now
	elapsed := now.Sub(r.start).Seconds()
	if elapsed <= 0 {
		elapsed = 0.001
	}
	speed := float64(r.done) / elapsed
	remaining := time.Duration(0)
	if speed > 0 && r.total > r.done {
		remaining = time.Duration(math.Ceil(float64(r.total-r.done)/speed)) * time.Second
	}
	percent := 100.0
	if r.total > 0 {
		percent = float64(r.done) * 100 / float64(r.total)
	}
	fmt.Fprintf(r.output, "upload %s %.1f%% %s/%s %s/s eta %s\n", r.name, percent, humanBytes(r.done), humanBytes(r.total), humanBytes(int64(speed)), remaining.Round(time.Second))
}

func humanBytes(value int64) string {
	const unit = 1024
	if value < unit {
		return fmt.Sprintf("%dB", value)
	}
	div, exp := int64(unit), 0
	for n := value / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(value)/float64(div), "KMGTPE"[exp])
}

func manifestMediaIDs(manifest map[string]any) []string {
	seen := make(map[string]bool)
	var ids []string
	for _, key := range []string{"video_tracks", "audio_tracks", "subtitles", "sprites"} {
		items, _ := manifest[key].([]any)
		for _, item := range items {
			object, _ := item.(map[string]any)
			id, _ := object["media_id"].(string)
			id = strings.TrimSpace(id)
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func ManifestMediaIDs(manifest map[string]any) []string {
	return manifestMediaIDs(manifest)
}

func loadUploadState(root string) (uploadState, error) {
	state := uploadState{Media: map[string]uploadMediaState{}}
	data, err := os.ReadFile(filepath.Join(root, "upload_state.json"))
	if os.IsNotExist(err) {
		return state, nil
	}
	if err != nil {
		return uploadState{}, fmt.Errorf("read upload_state.json: %w", err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return state, nil
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return uploadState{}, fmt.Errorf("decode upload_state.json: %w", err)
	}
	if state.Media == nil {
		state.Media = map[string]uploadMediaState{}
	}
	for mediaID, mediaState := range state.Media {
		if mediaState.Files == nil {
			mediaState.Files = map[string]uploadFileState{}
		}
		recomputeMediaState(&mediaState)
		state.Media[mediaID] = mediaState
	}
	return state, nil
}

func saveUploadState(root string, state uploadState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode upload_state.json: %w", err)
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(root, "upload_state.json"), data, 0o600)
}

func cleanupUploadedArtifacts(root string) error {
	if err := archiveTaskJSONFiles(root); err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
			return fmt.Errorf("remove uploaded artifact %s: %w", entry.Name(), err)
		}
	}
	if err := os.Remove(root); err != nil {
		return fmt.Errorf("remove uploaded task directory %s: %w", root, err)
	}
	return nil
}

func archiveTaskJSONFiles(root string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	archiveDir := filepath.Join(filepath.Dir(root), "_logs", filepath.Base(root))
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("stat %s: %w", entry.Name(), err)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if err := os.MkdirAll(archiveDir, 0o700); err != nil {
			return fmt.Errorf("create json archive directory: %w", err)
		}
		source := filepath.Join(root, entry.Name())
		destination := filepath.Join(archiveDir, entry.Name())
		if err := os.Remove(destination); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("replace archived %s: %w", entry.Name(), err)
		}
		if err := os.Rename(source, destination); err != nil {
			return fmt.Errorf("move %s to %s: %w", entry.Name(), destination, err)
		}
	}
	return nil
}

func (u Uploader) logf(format string, args ...any) {
	if u.Output == nil {
		return
	}
	fmt.Fprintf(u.Output, format, args...)
}
