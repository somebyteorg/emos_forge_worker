package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Artifact struct {
	ObjectKey string `json:"object_key"`
	SizeBytes int64  `json:"size_bytes"`
}

type Local struct {
	root string
}

func NewLocal(root string) (*Local, error) {
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("storage root must be absolute")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create storage root: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("resolve storage root: %w", err)
	}
	return &Local{root: filepath.Clean(resolved)}, nil
}

func (l *Local) Stat(ctx context.Context, objectKey string) (Artifact, error) {
	if err := ctx.Err(); err != nil {
		return Artifact{}, err
	}
	path, err := l.resolveExisting(objectKey)
	if err != nil {
		return Artifact{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return Artifact{}, err
	}
	if !info.Mode().IsRegular() {
		return Artifact{}, fmt.Errorf("object is not a regular file")
	}
	return Artifact{ObjectKey: filepath.ToSlash(filepath.Clean(objectKey)), SizeBytes: info.Size()}, nil
}

func (l *Local) Exists(ctx context.Context, objectKey string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	_, err := l.resolveExisting(objectKey)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

func (l *Local) Commit(ctx context.Context, tempKey, finalKey string) (Artifact, error) {
	if err := ctx.Err(); err != nil {
		return Artifact{}, err
	}
	tempPath, err := l.resolveExisting(tempKey)
	if err != nil {
		return Artifact{}, fmt.Errorf("resolve temporary object: %w", err)
	}
	finalPath, err := l.resolveForWrite(finalKey)
	if err != nil {
		return Artifact{}, fmt.Errorf("resolve final object: %w", err)
	}
	if err := syncFile(tempPath); err != nil {
		return Artifact{}, fmt.Errorf("sync temporary object: %w", err)
	}
	if err := os.Rename(tempPath, finalPath); err != nil {
		return Artifact{}, fmt.Errorf("commit object: %w", err)
	}
	if err := syncDir(filepath.Dir(finalPath)); err != nil {
		return Artifact{}, fmt.Errorf("sync final object directory: %w", err)
	}
	return l.Stat(ctx, finalKey)
}

func (l *Local) resolveForWrite(objectKey string) (string, error) {
	path, err := l.resolve(objectKey)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("create object directory: %w", err)
	}
	resolvedParent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "", fmt.Errorf("resolve object parent: %w", err)
	}
	if !within(l.root, resolvedParent) {
		return "", fmt.Errorf("object parent escapes storage root through a symbolic link")
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("refusing to replace symbolic link object")
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	return path, nil
}

func (l *Local) resolveExisting(objectKey string) (string, error) {
	path, err := l.resolve(objectKey)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("symbolic link objects are not allowed")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	if !within(l.root, resolved) {
		return "", fmt.Errorf("object escapes storage root")
	}
	return resolved, nil
}

func (l *Local) resolve(objectKey string) (string, error) {
	if objectKey == "" || filepath.IsAbs(objectKey) || strings.ContainsRune(objectKey, 0) {
		return "", fmt.Errorf("object key must be a non-empty relative path")
	}
	clean := filepath.Clean(filepath.FromSlash(objectKey))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("object key escapes storage root")
	}
	path := filepath.Join(l.root, clean)
	if !within(l.root, path) {
		return "", fmt.Errorf("object key escapes storage root")
	}
	return path, nil
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func syncFile(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func syncDir(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
