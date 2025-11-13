package policy

import (
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	asink "dirpx.dev/dlog/apis/sink"
	spolicy "dirpx.dev/dlog/apis/sink/policy"
)

// FileRotationOptions configures a rotating file sink.
//
// It is a runtime counterpart of apis/sink/policy.Rotation plus
// concrete file system details (path, file mode).
type FileRotationOptions struct {
	// Path is the path to the active log file.
	// Example: "/var/log/myapp.log".
	Path string

	// Policy describes when and how rotation should happen:
	//   - MaxSizeMB > 0 -> rotate when file size exceeds N megabytes.
	//   - MaxAgeDays > 0 -> rotate when file age exceeds N days.
	//   - MaxBackups > 0 -> keep at most N rotated files (older ones are deleted).
	//   - Compress -> optionally compress rotated files with gzip.
	Policy spolicy.Rotation

	// Name overrides the sink name. If empty, the sink reports
	// its name as "file(<base>)" where <base> is filepath.Base(Path).
	Name string

	// FileMode controls permissions for created log files.
	// When zero, a default of 0640 is used.
	FileMode os.FileMode
}

// rotatingFileSink implements asink.Sink and performs on-disk log rotation
// based on size, age and backup limits.
//
// Semantics:
//
//   - Write:
//
//     Is concurrency safe (guarded by a mutex).
//     Before each write, checks whether rotation is needed based on
//     current file size and age.
//     If rotation fails, returns an error and does not write the entry.
//
//   - Flush:
//
//     Calls file.Sync() on the underlying file.
//     Returns ErrRotationClosed after Close.
//
//   - Close:
//
//     Closes the underlying file, is idempotent, and marks the sink closed.
//     After Close, Write/Flush return ErrRotationClosed.
//
// Rotation naming scheme:
//   - Active file: Path (e.g. "/var/log/myapp.log").
//   - Rotated files: Path+".YYYYMMDD-HHMMSS" (UTC time).
//   - When Compress is true, rotated files are gzipped: ".gz" suffix added.
type rotatingFileSink struct {
	mu      sync.Mutex
	path    string
	opt     FileRotationOptions
	file    *os.File
	size    int64     // current file size in bytes
	created time.Time // last (re)open/rotation time (or file mod time)
	closed  bool
}

// Compile-time safety: *rotatingFileSink implements asink.Sink.
var _ asink.Sink = (*rotatingFileSink)(nil)

var (
	// ErrRotationClosed indicates that the sink has been closed.
	ErrRotationClosed = errors.New("sink/rotation: closed")

	// ErrRotationNoPath indicates that an empty file path was provided.
	ErrRotationNoPath = errors.New("sink/rotation: empty path")
)

// NewRotatingFileSink constructs a file-based sink with rotation.
//
// The function opens (or creates) the active log file immediately and
// inspects its current size and mod time to initialize rotation state.
//
// Returned sink is ready for concurrent use.
func NewRotatingFileSink(opt FileRotationOptions) (asink.Sink, error) {
	if opt.Path == "" {
		return nil, ErrRotationNoPath
	}
	opt.Policy = normalizeRotationPolicy(opt.Policy)
	if opt.FileMode == 0 {
		opt.FileMode = 0o640
	}

	s := &rotatingFileSink{
		path: opt.Path,
		opt:  opt,
	}
	if err := s.openCurrent(); err != nil {
		return nil, err
	}
	return s, nil
}

// Name returns the human-friendly name of the sink.
func (s *rotatingFileSink) Name() string {
	if s.opt.Name != "" {
		return s.opt.Name
	}
	base := filepath.Base(s.path)
	return "file(" + base + ")"
}

// Write writes a single encoded log entry to the current log file,
// performing rotation when needed.
//
// Behavior:
//   - If ctx is already cancelled, returns ctx.Err() without writing.
//   - If rotation is required and fails, returns the rotation error and
//     does not write the entry.
//   - If the sink is closed, returns ErrRotationClosed.
func (s *rotatingFileSink) Write(ctx context.Context, entry []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ErrRotationClosed
	}

	// Lazily open file if somehow not present (defensive).
	if s.file == nil {
		if err := s.openCurrent(); err != nil {
			return err
		}
	}

	if s.shouldRotate(time.Now(), len(entry)) {
		if err := s.rotateLocked(); err != nil {
			return err
		}
	}

	n, err := s.file.Write(entry)
	s.size += int64(n)
	if err != nil {
		return err
	}
	return nil
}

// Flush ensures that all buffered data is written to disk.
// It calls file.Sync on the underlying file.
//
// After Close, Flush returns ErrRotationClosed.
func (s *rotatingFileSink) Flush(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ErrRotationClosed
	}
	if s.file == nil {
		return nil
	}
	return s.file.Sync()
}

// Close closes the current log file and marks the sink closed.
// Close is idempotent; subsequent calls return nil.
//
// After Close, Write and Flush return ErrRotationClosed.
func (s *rotatingFileSink) Close(ctx context.Context) error {
	_ = ctx // context is accepted for interface symmetry; not used here.

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}
	s.closed = true

	if s.file != nil {
		err := s.file.Close()
		s.file = nil
		return err
	}
	return nil
}

// openCurrent opens the active log file, initializing size and created fields.
func (s *rotatingFileSink) openCurrent() error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, s.opt.FileMode)
	if err != nil {
		return err
	}

	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}

	s.file = f
	s.size = info.Size()
	s.created = info.ModTime()
	return nil
}

// shouldRotate decides whether a rotation is required before writing
// an entry with the given size. It uses the current file size and creation time.
func (s *rotatingFileSink) shouldRotate(now time.Time, incomingBytes int) bool {
	p := s.opt.Policy
	if p.MaxSizeMB > 0 {
		maxSize := int64(p.MaxSizeMB) * 1024 * 1024
		if s.size+int64(incomingBytes) > maxSize {
			return true
		}
	}
	if p.MaxAgeDays > 0 {
		maxAge := time.Duration(p.MaxAgeDays) * 24 * time.Hour
		if now.Sub(s.created) >= maxAge {
			return true
		}
	}
	return false
}

// rotateLocked performs log rotation while the caller holds s.mu.
// It closes the current file (if any), renames it to a backup file,
// optionally compresses the backup, prunes old backups, and opens a new file.
func (s *rotatingFileSink) rotateLocked() error {
	// Close current file handle if open.
	if s.file != nil {
		_ = s.file.Close()
		s.file = nil
	}

	// If the active file exists, rename it to a backup name.
	if _, err := os.Stat(s.path); err == nil {
		backup := rotatedFilename(s.path, time.Now())
		if err := os.Rename(s.path, backup); err != nil {
			// If we cannot rename, do not proceed with opening a new file —
			// callers should see an error rather than silently losing data.
			return err
		}

		if s.opt.Policy.Compress {
			// Compression errors are best-effort: we ignore failures here,
			// rotated content is already safely on disk.
			_ = compressFile(backup)
		}

		if s.opt.Policy.MaxBackups > 0 {
			_ = pruneBackups(s.path, s.opt.Policy.MaxBackups)
		}
	}

	// Open a fresh active file.
	return s.openCurrent()
}

// normalizeRotationPolicy sanitizes Rotation fields to safe defaults.
//
// Semantics:
//   - Negative values are clamped to zero (disabled).
//   - Zero values mean "no rotation by this dimension".
func normalizeRotationPolicy(p spolicy.Rotation) spolicy.Rotation {
	if p.MaxSizeMB < 0 {
		p.MaxSizeMB = 0
	}
	if p.MaxAgeDays < 0 {
		p.MaxAgeDays = 0
	}
	if p.MaxBackups < 0 {
		p.MaxBackups = 0
	}
	return p
}

// rotatedFilename builds a rotated file path for the given base path and time.
// Example:
//
//	base: /var/log/app.log
//	t:    2025-03-01T12:34:56Z
//	->    /var/log/app.log.20250301-123456
func rotatedFilename(basePath string, t time.Time) string {
	dir := filepath.Dir(basePath)
	name := filepath.Base(basePath)
	ts := t.UTC().Format("20060102-150405")
	return filepath.Join(dir, name+"."+ts)
}

// pruneBackups removes oldest rotated files so that at most maxBackups remain.
//
// It looks for files in the same directory whose names start with
// filepath.Base(basePath) + "." (including compressed ".gz" variants).
func pruneBackups(basePath string, maxBackups int) error {
	if maxBackups <= 0 {
		return nil
	}

	dir := filepath.Dir(basePath)
	base := filepath.Base(basePath)
	prefix := base + "."

	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	type backup struct {
		path    string
		modTime time.Time
	}

	var backups []backup
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		backups = append(backups, backup{
			path:    filepath.Join(dir, name),
			modTime: info.ModTime(),
		})
	}

	if len(backups) <= maxBackups {
		return nil
	}

	sort.Slice(backups, func(i, j int) bool {
		return backups[i].modTime.Before(backups[j].modTime)
	})

	toDelete := backups[:len(backups)-maxBackups]
	for _, b := range toDelete {
		_ = os.Remove(b.path) // best-effort
	}
	return nil
}

// compressFile gzips srcPath into srcPath+".gz" and removes the original file.
func compressFile(srcPath string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()

	dstPath := srcPath + ".gz"
	dst, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return err
	}
	defer dst.Close()

	gw := gzip.NewWriter(dst)
	if _, err := io.Copy(gw, src); err != nil {
		_ = gw.Close()
		return err
	}
	if err := gw.Close(); err != nil {
		return err
	}

	// Remove original file after successful compression.
	if err := os.Remove(srcPath); err != nil {
		return err
	}
	return nil
}
