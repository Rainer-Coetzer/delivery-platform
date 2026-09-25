package blob

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocal(t *testing.T) {
	ctx := context.Background()
	l, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	n, err := l.Put(ctx, "proof/a1/0-photo", strings.NewReader("jpeg bytes"), 100)
	if err != nil || n != 10 {
		t.Fatalf("put: %d %v", n, err)
	}
	r, err := l.Open(ctx, "proof/a1/0-photo")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(r)
	r.Close()
	if string(b) != "jpeg bytes" {
		t.Fatalf("read back %q", b)
	}

	if _, err := l.Put(ctx, "proof/a1/1-photo", strings.NewReader(strings.Repeat("x", 101)), 100); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("over the limit: %v", err)
	}
	if _, err := l.Open(ctx, "proof/a1/1-photo"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a refused upload leaves nothing behind: %v", err)
	}
	if _, err := l.Put(ctx, "proof/a1/2-photo", strings.NewReader(""), 100); err == nil {
		t.Fatal("an empty file is refused")
	}
	entries, _ := os.ReadDir(filepath.Join(l.Root, "proof", "a1"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".upload-") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

func TestKeysCannotEscapeTheRoot(t *testing.T) {
	l, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"../etc/passwd", "proof/../../x", "/abs/path", "proof", "Proof/A", "proof//x", `proof\..\x`} {
		if _, err := l.Put(context.Background(), key, strings.NewReader("x"), 10); err == nil {
			t.Errorf("key %q must be refused", key)
		}
		if _, err := l.Open(context.Background(), key); err == nil {
			t.Errorf("key %q must be refused on read", key)
		}
	}
}

func TestNewLocalNeedsAWritableRoot(t *testing.T) {
	f := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewLocal(filepath.Join(f, "blobs")); err == nil {
		t.Fatal("a root under a file can't be created")
	}
}
