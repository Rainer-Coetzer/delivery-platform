// Package blob stores the files the platform keeps: proof photos and
// signatures, job documents. The database holds what a file is (owner,
// type, size); a blob store holds the bytes under a key.
//
// Local is the one implementation: a directory, which in compose is a
// named volume. Back it up with the database. A buyer who wants S3 or
// similar implements Store (ADR-0004 tier 2).
package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

// ErrNotFound: no blob under that key.
var ErrNotFound = errors.New("blob not found")

// ErrTooLarge: the upload went past the size the caller allowed.
var ErrTooLarge = errors.New("file is too large")

type Store interface {
	// Put stores r under key, reading at most max bytes; more is
	// ErrTooLarge and nothing is kept. It returns the size stored.
	Put(ctx context.Context, key string, r io.Reader, max int64) (int64, error)
	Open(ctx context.Context, key string) (io.ReadCloser, error)
}

// validKey keeps keys to paths we generate (module/id/name), so a key can
// never climb out of the root, whatever a bug upstream puts in it.
var validKey = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*(/[a-z0-9][a-z0-9._-]*)+$`)

// Local stores blobs as files under Root.
type Local struct{ Root string }

// NewLocal checks the root exists and is writable now, at boot, rather
// than at the first upload in production.
func NewLocal(root string) (*Local, error) {
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("blob store %s: %w", root, err)
	}
	probe, err := os.CreateTemp(root, ".write-check-*")
	if err != nil {
		return nil, fmt.Errorf("blob store %s is not writable: %w", root, err)
	}
	probe.Close()
	os.Remove(probe.Name())
	return &Local{Root: root}, nil
}

func (l *Local) path(key string) (string, error) {
	if !validKey.MatchString(key) {
		return "", fmt.Errorf("blob key %q is not a valid key", key)
	}
	return filepath.Join(l.Root, filepath.FromSlash(key)), nil
}

// Put writes to a temporary file and renames it into place, so a reader
// never sees half a file and a failed upload leaves nothing behind.
func (l *Local) Put(_ context.Context, key string, r io.Reader, max int64) (int64, error) {
	dst, err := l.path(key)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return 0, fmt.Errorf("store %s: %w", key, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".upload-*")
	if err != nil {
		return 0, fmt.Errorf("store %s: %w", key, err)
	}
	defer os.Remove(tmp.Name()) // a no-op once renamed
	n, err := io.Copy(tmp, io.LimitReader(r, max+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return 0, fmt.Errorf("store %s: %w", key, err)
	}
	if n > max {
		return 0, fmt.Errorf("%w: over %d bytes", ErrTooLarge, max)
	}
	if n == 0 {
		return 0, fmt.Errorf("store %s: the file is empty", key)
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return 0, fmt.Errorf("store %s: %w", key, err)
	}
	return n, nil
}

func (l *Local) Open(_ context.Context, key string) (io.ReadCloser, error) {
	p, err := l.path(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, key)
	}
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", key, err)
	}
	return f, nil
}
