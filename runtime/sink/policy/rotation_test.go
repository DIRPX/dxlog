package policy

import (
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	spolicy "dirpx.dev/dlog/apis/sink/policy"
)

func TestNewRotatingFileSink_EmptyPath(t *testing.T) {
	_, err := NewRotatingFileSink(FileRotationOptions{
		Path:   "",
		Policy: spolicy.Rotation{},
	})
	if err == nil {
		t.Fatalf("expected error for empty path, got nil")
	}
	if err != ErrRotationNoPath {
		t.Fatalf("err = %v, want ErrRotationNoPath", err)
	}
}

func TestRotatingFileSink_Name_DefaultAndOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")

	s, err := NewRotatingFileSink(FileRotationOptions{
		Path: path,
	})
	if err != nil {
		t.Fatalf("NewRotatingFileSink: %v", err)
	}
	if got, want := s.Name(), "file(app.log)"; got != want {
		t.Fatalf("Name() = %q, want %q", got, want)
	}

	s2, err := NewRotatingFileSink(FileRotationOptions{
		Path: path,
		Name: "custom",
	})
	if err != nil {
		t.Fatalf("NewRotatingFileSink: %v", err)
	}
	if got, want := s2.Name(), "custom"; got != want {
		t.Fatalf("Name() = %q, want %q", got, want)
	}
}

func TestRotatingFileSink_WriteCreatesAndAppends(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")

	s, err := NewRotatingFileSink(FileRotationOptions{
		Path: path,
	})
	if err != nil {
		t.Fatalf("NewRotatingFileSink: %v", err)
	}
	defer s.Close(context.Background())

	ctx := context.Background()
	if err := s.Write(ctx, []byte("one\n")); err != nil {
		t.Fatalf("Write 1: %v", err)
	}
	if err := s.Write(ctx, []byte("two\n")); err != nil {
		t.Fatalf("Write 2: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if got, want := string(data), "one\ntwo\n"; got != want {
		t.Fatalf("file content = %q, want %q", got, want)
	}
}

func TestRotatingFileSink_RotateOnSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")

	pol := spolicy.Rotation{
		MaxSizeMB: 1, // threshold: 1 MiB
	}
	sink, err := NewRotatingFileSink(FileRotationOptions{
		Path:   path,
		Policy: pol,
	})
	if err != nil {
		t.Fatalf("NewRotatingFileSink: %v", err)
	}
	defer sink.Close(context.Background())

	// Force internal size close to MaxSize so that next write triggers rotation.
	rs := sink.(*rotatingFileSink)
	maxBytes := int64(pol.MaxSizeMB) * 1024 * 1024

	rs.mu.Lock()
	rs.size = maxBytes // next Write will see size+len(entry) > maxBytes
	rs.mu.Unlock()

	// Perform a write that should cause rotation.
	if err := sink.Write(context.Background(), []byte("rotated\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// We expect: one active file + at least one rotated backup.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var active, backups int
	for _, e := range entries {
		if e.Name() == "app.log" {
			active++
		} else if strings.HasPrefix(e.Name(), "app.log.") {
			backups++
		}
	}
	if active != 1 {
		t.Fatalf("expected 1 active file, got %d", active)
	}
	if backups == 0 {
		t.Fatalf("expected at least one rotated backup file, got 0")
	}
}

func TestRotatingFileSink_RotateOnAge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")

	pol := spolicy.Rotation{
		MaxAgeDays: 1,
	}
	sink, err := NewRotatingFileSink(FileRotationOptions{
		Path:   path,
		Policy: pol,
	})
	if err != nil {
		t.Fatalf("NewRotatingFileSink: %v", err)
	}
	defer sink.Close(context.Background())

	rs := sink.(*rotatingFileSink)

	// Pretend the file is older than MaxAgeDays.
	rs.mu.Lock()
	rs.created = time.Now().Add(-48 * time.Hour) // 2 days ago
	rs.mu.Unlock()

	if err := sink.Write(context.Background(), []byte("age\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var backups int
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "app.log.") {
			backups++
		}
	}
	if backups == 0 {
		t.Fatalf("expected at least one rotated backup due to age, got 0")
	}
}

func TestRotatingFileSink_WriteAfterClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")

	sink, err := NewRotatingFileSink(FileRotationOptions{
		Path: path,
	})
	if err != nil {
		t.Fatalf("NewRotatingFileSink: %v", err)
	}

	if err := sink.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	err = sink.Write(context.Background(), []byte("x"))
	if err != ErrRotationClosed {
		t.Fatalf("Write after Close err = %v, want ErrRotationClosed", err)
	}

	err = sink.Flush(context.Background())
	if err != ErrRotationClosed {
		t.Fatalf("Flush after Close err = %v, want ErrRotationClosed", err)
	}
}

func TestRotatedFilename_Format(t *testing.T) {
	base := "/var/log/app.log"
	ts := time.Date(2025, 3, 1, 12, 34, 56, 0, time.UTC)

	got := rotatedFilename(base, ts)
	want := "/var/log/app.log.20250301-123456"
	if got != want {
		t.Fatalf("rotatedFilename = %q, want %q", got, want)
	}
}

func TestPruneBackups_DeletesOldest(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "app.log")

	// Create 3 fake backup files with different mod times.
	names := []string{
		"app.log.1",
		"app.log.2",
		"app.log.3",
	}
	for i, name := range names {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte{byte('a' + i)}, 0o640); err != nil {
			t.Fatalf("WriteFile(%s): %v", path, err)
		}
		// Stagger mod times.
		tm := time.Now().Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(path, tm, tm); err != nil {
			t.Fatalf("Chtimes(%s): %v", path, err)
		}
	}

	if err := pruneBackups(base, 2); err != nil {
		t.Fatalf("pruneBackups: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var backups []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "app.log.") {
			backups = append(backups, e.Name())
		}
	}
	if len(backups) != 2 {
		t.Fatalf("expected 2 backups after prune, got %d (%v)", len(backups), backups)
	}
}

func TestCompressFile_CreatesGzipAndRemovesSource(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "app.log.1")

	content := []byte("hello rotation")
	if err := os.WriteFile(srcPath, content, 0o640); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := compressFile(srcPath); err != nil {
		t.Fatalf("compressFile: %v", err)
	}

	// Original should be gone.
	if _, err := os.Stat(srcPath); !os.IsNotExist(err) {
		t.Fatalf("expected src to be removed, got err=%v", err)
	}

	// Gzipped file should exist and contain the original content.
	gzPath := srcPath + ".gz"
	f, err := os.Open(gzPath)
	if err != nil {
		t.Fatalf("Open gz: %v", err)
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	defer gr.Close()

	data, err := io.ReadAll(gr)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(data) != string(content) {
		t.Fatalf("gz content = %q, want %q", string(data), string(content))
	}
}
