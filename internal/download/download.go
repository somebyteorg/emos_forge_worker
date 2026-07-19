package download

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"forge_worker/internal/httpx"
)

type Request struct {
	URI         string
	PartialPath string
	FinalPath   string
}

type Metadata struct {
	ContentLength int64  `json:"content_length,omitempty"`
	ETag          string `json:"etag,omitempty"`
	LastModified  string `json:"last_modified,omitempty"`
	AcceptRanges  bool   `json:"accept_ranges"`
}

type Result struct {
	Path         string   `json:"path"`
	Bytes        int64    `json:"bytes"`
	Resumed      bool     `json:"resumed"`
	StalePartial string   `json:"stale_partial,omitempty"`
	Metadata     Metadata `json:"metadata"`
}

type Downloader struct {
	HTTP HTTPDoer
}

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

func (d Downloader) Download(ctx context.Context, request Request) (Result, error) {
	if request.URI == "" || request.PartialPath == "" || request.FinalPath == "" {
		return Result{}, fmt.Errorf("download URI, partial path, and final path are required")
	}
	if err := os.MkdirAll(filepath.Dir(request.PartialPath), 0o700); err != nil {
		return Result{}, fmt.Errorf("create download directory: %w", err)
	}
	if info, err := os.Stat(request.FinalPath); err == nil {
		if !info.Mode().IsRegular() {
			return Result{}, fmt.Errorf("final download path is not a regular file")
		}
		return Result{Path: request.FinalPath, Bytes: info.Size()}, nil
	} else if err != nil && !os.IsNotExist(err) {
		return Result{}, err
	}

	client := d.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	partialSize, err := regularFileSize(request.PartialPath)
	if err != nil {
		return Result{}, err
	}
	previousMetadata, hasPreviousMetadata, err := readMetadata(metadataPath(request.PartialPath))
	if err != nil {
		return Result{}, err
	}
	stalePartial := ""
	resume := partialSize > 0 && hasPreviousMetadata && previousMetadata.AcceptRanges &&
		(previousMetadata.ContentLength <= 0 || partialSize < previousMetadata.ContentLength)
	if partialSize > 0 && !resume {
		name, err := markDownloadStale(request.PartialPath)
		if err != nil {
			return Result{}, err
		}
		stalePartial = name
		partialSize = 0
	}

	response, err := get(ctx, client, request.URI, partialSize, resume)
	if err != nil {
		return Result{}, err
	}
	metadata := metadataFromResponse(response)
	if resume {
		validRange := response.StatusCode == http.StatusPartialContent &&
			validContentRange(response.Header.Get("Content-Range"), partialSize, previousMetadata.ContentLength) &&
			compatibleMetadata(previousMetadata, metadata)
		switch {
		case validRange:
			metadata = mergeMetadata(metadata, previousMetadata)
		case response.StatusCode == http.StatusOK:
			name, err := markDownloadStale(request.PartialPath)
			if err != nil {
				response.Body.Close()
				return Result{}, err
			}
			stalePartial = name
			partialSize = 0
			resume = false
		case response.StatusCode == http.StatusPartialContent:
			response.Body.Close()
			name, err := markDownloadStale(request.PartialPath)
			if err != nil {
				return Result{}, err
			}
			stalePartial = name
			partialSize = 0
			resume = false
			response, err = get(ctx, client, request.URI, 0, false)
			if err != nil {
				return Result{}, err
			}
			metadata = metadataFromResponse(response)
		default:
			response.Body.Close()
			return Result{}, fmt.Errorf("download range GET returned HTTP %d", response.StatusCode)
		}
	}
	defer response.Body.Close()

	if !resume && response.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("download GET returned HTTP %d", response.StatusCode)
	}
	if resume && response.StatusCode != http.StatusPartialContent {
		return Result{}, fmt.Errorf("download range GET returned HTTP %d", response.StatusCode)
	}
	if err := writeMetadata(metadataPath(request.PartialPath), metadata); err != nil {
		return Result{}, err
	}

	flag := os.O_CREATE | os.O_WRONLY
	if resume {
		flag |= os.O_APPEND
	} else {
		flag |= os.O_TRUNC
	}
	file, err := os.OpenFile(request.PartialPath, flag, 0o600)
	if err != nil {
		return Result{}, fmt.Errorf("open partial download: %w", err)
	}
	written, copyErr := copyContext(ctx, file, response.Body)
	if syncErr := file.Sync(); syncErr != nil && copyErr == nil {
		copyErr = syncErr
	}
	if closeErr := file.Close(); closeErr != nil && copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return Result{}, fmt.Errorf("write partial download: %w", copyErr)
	}
	bytes := partialSize + written
	if metadata.ContentLength > 0 && bytes != metadata.ContentLength {
		return Result{}, fmt.Errorf("download length mismatch: got %d bytes, expected %d", bytes, metadata.ContentLength)
	}
	if err := os.Rename(request.PartialPath, request.FinalPath); err != nil {
		return Result{}, fmt.Errorf("commit download: %w", err)
	}
	if err := os.Rename(metadataPath(request.PartialPath), metadataPath(request.FinalPath)); err != nil && !os.IsNotExist(err) {
		return Result{}, fmt.Errorf("commit download metadata: %w", err)
	}
	if err := syncDir(filepath.Dir(request.FinalPath)); err != nil {
		return Result{}, fmt.Errorf("sync download directory: %w", err)
	}
	return Result{Path: request.FinalPath, Bytes: bytes, Resumed: resume, StalePartial: stalePartial, Metadata: metadata}, nil
}

func metadataPath(path string) string {
	return path + ".metadata.json"
}

func readMetadata(path string) (Metadata, bool, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return Metadata{}, false, nil
	}
	if err != nil {
		return Metadata{}, false, err
	}
	defer file.Close()
	var metadata Metadata
	if err := json.NewDecoder(file).Decode(&metadata); err != nil {
		return Metadata{}, false, fmt.Errorf("decode download metadata: %w", err)
	}
	return metadata, true, nil
}

func writeMetadata(path string, metadata Metadata) error {
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("encode download metadata: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".download-metadata-*.partial")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, path)
}

func compatibleMetadata(previous, current Metadata) bool {
	if previous.ETag != "" && current.ETag != "" && previous.ETag != current.ETag {
		return false
	}
	if previous.LastModified != "" && current.LastModified != "" && previous.LastModified != current.LastModified {
		return false
	}
	if previous.ContentLength > 0 && current.ContentLength > 0 && previous.ContentLength != current.ContentLength {
		return false
	}
	return true
}

func mergeMetadata(left, right Metadata) Metadata {
	if left.ContentLength <= 0 && right.ContentLength > 0 {
		left.ContentLength = right.ContentLength
	}
	if left.ETag == "" {
		left.ETag = right.ETag
	}
	if left.LastModified == "" {
		left.LastModified = right.LastModified
	}
	left.AcceptRanges = left.AcceptRanges || right.AcceptRanges
	return left
}

func get(ctx context.Context, client HTTPDoer, uri string, offset int64, resume bool) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return nil, err
	}
	httpx.SetUserAgent(request)
	if resume {
		request.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	return client.Do(request)
}

func metadataFromResponse(response *http.Response) Metadata {
	metadata := metadataFromHeaders(response.Header, response.ContentLength)
	if response.StatusCode == http.StatusPartialContent {
		metadata.AcceptRanges = true
		if total := contentRangeTotal(response.Header.Get("Content-Range")); total > 0 {
			metadata.ContentLength = total
		}
	}
	return metadata
}

func metadataFromHeaders(header http.Header, contentLength int64) Metadata {
	if contentLength <= 0 {
		if raw := header.Get("Content-Length"); raw != "" {
			if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil {
				contentLength = parsed
			}
		}
	}
	return Metadata{
		ContentLength: contentLength,
		ETag:          header.Get("ETag"),
		LastModified:  header.Get("Last-Modified"),
		AcceptRanges:  strings.EqualFold(header.Get("Accept-Ranges"), "bytes"),
	}
}

func regularFileSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() {
		return 0, fmt.Errorf("partial download path is not a regular file")
	}
	return info.Size(), nil
}

func validContentRange(value string, start, total int64) bool {
	if value == "" || !strings.HasPrefix(strings.ToLower(value), "bytes ") {
		return false
	}
	value = strings.TrimSpace(value[len("bytes "):])
	rangePart, totalPart, ok := strings.Cut(value, "/")
	if !ok {
		return false
	}
	startPart, _, ok := strings.Cut(rangePart, "-")
	if !ok {
		return false
	}
	parsedStart, err := strconv.ParseInt(startPart, 10, 64)
	if err != nil || parsedStart != start {
		return false
	}
	if total > 0 && totalPart != "*" {
		parsedTotal, err := strconv.ParseInt(totalPart, 10, 64)
		if err != nil || parsedTotal != total {
			return false
		}
	}
	return true
}

func contentRangeTotal(value string) int64 {
	_, totalPart, ok := strings.Cut(strings.TrimSpace(value), "/")
	if !ok || totalPart == "*" {
		return 0
	}
	total, err := strconv.ParseInt(totalPart, 10, 64)
	if err != nil || total <= 0 {
		return 0
	}
	return total
}

func markDownloadStale(partialPath string) (string, error) {
	name, err := markStale(partialPath)
	if err != nil {
		return "", err
	}
	_, _ = markStale(metadataPath(partialPath))
	return name, nil
}

func markStale(path string) (string, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	stale := fmt.Sprintf("%s.stale.%s", path, time.Now().UTC().Format("20060102T150405.000000000Z"))
	if err := os.Rename(path, stale); err != nil {
		return "", fmt.Errorf("mark stale partial: %w", err)
	}
	return stale, nil
}

func copyContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	buffer := make([]byte, 128*1024)
	var written int64
	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		count, readErr := src.Read(buffer)
		if count > 0 {
			n, writeErr := dst.Write(buffer[:count])
			written += int64(n)
			if writeErr != nil {
				return written, writeErr
			}
			if n != count {
				return written, io.ErrShortWrite
			}
		}
		if readErr == io.EOF {
			return written, nil
		}
		if readErr != nil {
			return written, readErr
		}
	}
}

func syncDir(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
