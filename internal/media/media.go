package media

import (
	"bytes"
	"context"
	"crypto/sha256"

	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"golang.org/x/image/webp"

	"rivosnap/internal/config"
	"rivosnap/internal/model"
)

var uuidRe = regexp.MustCompile(`(?i)^[a-f0-9-]{36}$`)

type Store struct {
	Env        config.Env
	mu         sync.Mutex
	usageCache map[string]usageEntry
}

type usageEntry struct {
	bytes     int64
	expiresAt time.Time
}

func NewStore(env config.Env) *Store {
	return &Store{
		Env:        env,
		usageCache: make(map[string]usageEntry),
	}
}

func (s *Store) Dirs() [2]string {
	return [2]string{s.Env.StagingPath, s.Env.ThumbnailPath}
}

func (s *Store) InitDirs() error {
	for _, d := range s.Dirs() {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) PathFor(id string, size int) (string, error) {
	if !uuidRe.MatchString(id) {
		return "", errors.New("invalid image ID")
	}
	if size > 0 {
		return filepath.Join(s.Env.ThumbnailPath, fmt.Sprintf("%s.%d.webp", id, size)), nil
	}
	return filepath.Join(s.Env.StagingPath, id+".image"), nil
}

func (s *Store) Free() (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(s.Env.StagingPath, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}

func (s *Store) Pressured(reserveVideo bool) (bool, error) {
	freeBytes, err := s.Free()
	if err != nil {
		return false, err
	}
	nextMB := s.Env.MaxImageSizeMB
	if reserveVideo && s.Env.MaxVideoSizeMB > nextMB {
		nextMB = s.Env.MaxVideoSizeMB
	}
	threshold := int64(s.Env.MinFreeMB+nextMB) * 1048576
	return freeBytes < threshold, nil
}

func (s *Store) ClearUsageCache() {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.usageCache)
}

func (s *Store) Usage(dir string) (int64, error) {
	now := time.Now()
	s.mu.Lock()
	if entry, ok := s.usageCache[dir]; ok && entry.expiresAt.After(now) {
		s.mu.Unlock()
		return entry.bytes, nil
	}
	s.mu.Unlock()

	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, e := range entries {
		if info, err := e.Info(); err == nil {
			total += info.Size()
		}
	}
	s.mu.Lock()
	s.usageCache[dir] = usageEntry{bytes: total, expiresAt: now.Add(10 * time.Second)}
	s.mu.Unlock()
	return total, nil
}

func Digest(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type MediaInfo struct {
	MimeType    string
	Width       int
	Height      int
	DurationMs  *int
	FileSize    int64
	Sha256      string
	StagingPath string
}

func detectImageFormat(header []byte) string {
	if len(header) >= 3 && header[0] == 0xFF && header[1] == 0xD8 && header[2] == 0xFF {
		return "image/jpeg"
	}
	if len(header) >= 8 && bytes.Equal(header[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}) {
		return "image/png"
	}
	if len(header) >= 6 && (bytes.Equal(header[:6], []byte("GIF87a")) || bytes.Equal(header[:6], []byte("GIF89a"))) {
		return "image/gif"
	}
	if len(header) >= 12 && bytes.Equal(header[:4], []byte("RIFF")) && bytes.Equal(header[8:12], []byte("WEBP")) {
		return "image/webp"
	}
	return ""
}

func Inspect(filePath string) (MediaInfo, error) {
	st, err := os.Stat(filePath)
	if err != nil {
		return MediaInfo{}, missingOrRaw(err)
	}
	f, err := os.Open(filePath)
	if err != nil {
		return MediaInfo{}, missingOrRaw(err)
	}
	defer f.Close()

	header := make([]byte, 16)
	n, err := io.ReadFull(f, header)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return MediaInfo{}, err
	}
	mime := detectImageFormat(header[:n])
	if mime == "" {
		return MediaInfo{}, model.NewFault("UNSUPPORTED_IMAGE", "Invalid or oversized image.", false, 0)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return MediaInfo{}, model.NewFault("UNSUPPORTED_IMAGE", "Invalid or oversized image.", false, 0)
	}
	var cfg image.Config
	if mime == "image/webp" {
		cfg, err = webp.DecodeConfig(f)
	} else {
		cfg, _, err = image.DecodeConfig(f)
	}
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return MediaInfo{}, model.NewFault("UNSUPPORTED_IMAGE", "Invalid or oversized image.", false, 0)
	}
	if int64(cfg.Width)*int64(cfg.Height) > 100_000_000 {
		return MediaInfo{}, model.NewFault("UNSUPPORTED_IMAGE", "Invalid or oversized image.", false, 0)
	}
	sha, err := Digest(filePath)
	if err != nil {
		return MediaInfo{}, err
	}
	return MediaInfo{
		MimeType:   mime,
		Width:      cfg.Width,
		Height:     cfg.Height,
		DurationMs: nil,
		FileSize:   st.Size(),
		Sha256:     sha,
	}, nil
}

// probeTimeout bounds one ffprobe run. A file that keeps ffprobe busy this long is unsupported,
// so it is never retried as a tool fault.
var probeTimeout = 30 * time.Second

func InspectVideo(ctx context.Context, filePath string) (MediaInfo, error) {
	st, err := os.Stat(filePath)
	if err != nil {
		// A missing file is unsupported; any other stat failure is an environment fault (AUD3-007).
		if errors.Is(err, os.ErrNotExist) {
			return MediaInfo{}, model.NewFault("UNSUPPORTED_VIDEO", "Invalid or unreadable video.", false, 0)
		}
		return MediaInfo{}, err
	}
	cctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	out, err := exec.CommandContext(
		cctx,
		"ffprobe",
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=width,height:format=format_name,duration",
		"-of", "json",
		filePath,
	).Output()
	if err != nil {
		return MediaInfo{}, classifyProbeError(ctx, cctx, err)
	}
	var parsed struct {
		Streams []struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"streams"`
		Format struct {
			FormatName string `json:"format_name"`
			Duration   string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil || len(parsed.Streams) == 0 {
		return MediaInfo{}, model.NewFault("UNSUPPORTED_VIDEO", "Invalid or unreadable video.", false, 0)
	}
	stream := parsed.Streams[0]
	fmtName := parsed.Format.FormatName
	mime := ""
	if strings.Contains(fmtName, "webm") {
		mime = "video/webm"
	} else if strings.Contains(fmtName, "mp4") || strings.Contains(fmtName, "mov") || strings.Contains(fmtName, "m4a") {
		mime = "video/mp4"
	}
	if mime == "" || stream.Width <= 0 || stream.Height <= 0 || stream.Width > 100_000_000/stream.Height {
		return MediaInfo{}, model.NewFault("UNSUPPORTED_VIDEO", "Unsupported video format.", false, 0)
	}
	var durPtr *int
	if durSec, err := strconv.ParseFloat(parsed.Format.Duration, 64); err == nil && !math.IsNaN(durSec) && !math.IsInf(durSec, 0) && durSec > 0 {
		ms := int(math.Round(durSec * 1000))
		durPtr = &ms
	}
	sha, err := Digest(filePath)
	if err != nil {
		return MediaInfo{}, err
	}
	return MediaInfo{
		MimeType:   mime,
		Width:      stream.Width,
		Height:     stream.Height,
		DurationMs: durPtr,
		FileSize:   st.Size(),
		Sha256:     sha,
	}, nil
}

// classifyProbeError separates tool faults from hostile files for a failed ffprobe run.
// Shutdown or a caller deadline, a missing binary and SIGKILL (usually the OOM killer) are
// transient. A run that hits probeTimeout, exits non-zero or dies of another signal is the file's fault.
func classifyProbeError(parent, probe context.Context, err error) error {
	if parent.Err() != nil {
		return model.NewFault("MEDIA_TOOL", "Media tools were unavailable. Retry later.", true, 0)
	}
	if probe.Err() != nil {
		return model.NewFault("UNSUPPORTED_VIDEO", "Invalid or unreadable video.", false, 0)
	}
	if errors.Is(err, exec.ErrNotFound) {
		return model.NewFault("MEDIA_TOOL", "Media tools were unavailable. Retry later.", true, 0)
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() && status.Signal() == syscall.SIGKILL {
			return model.NewFault("MEDIA_TOOL", "Media tools were unavailable. Retry later.", true, 0)
		}
	}
	return model.NewFault("UNSUPPORTED_VIDEO", "Invalid or unreadable video.", false, 0)
}

// missingOrRaw: a file that is gone cannot be retried into existence, so it stays unsupported.
// Any other open failure is an environment fault and keeps its raw error for a transient retry.
func missingOrRaw(err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return model.NewFault("UNSUPPORTED_IMAGE", "Invalid or oversized image.", false, 0)
	}
	return err
}

func runFFmpegThumbnail(ctx context.Context, inputPath, outputPath string, size int, seekOneSec bool) error {
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	scaleExpr := fmt.Sprintf("scale='min(%d,iw)':'min(%d,ih)':force_original_aspect_ratio=decrease", size, size)
	args := []string{"-y"}
	if seekOneSec {
		args = append(args, "-ss", "00:00:01")
	}
	args = append(args,
		"-i", inputPath,
		"-frames:v", "1",
		"-vf", scaleExpr,
		"-vcodec", "libwebp",
		"-f", "webp",
		outputPath,
	)
	if err := exec.CommandContext(cctx, "ffmpeg", args...).Run(); err != nil {
		return err
	}
	// Seeking past EOF may succeed without producing an image.
	f, err := os.Open(outputPath)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = webp.DecodeConfig(f)
	return err
}

func (s *Store) Thumbnail(ctx context.Context, id string, size int, mediaType string) (string, error) {
	target, err := s.PathFor(id, size)
	if err != nil {
		return "", err
	}
	source, err := s.PathFor(id, 0)
	if err != nil {
		return "", err
	}
	tmp := fmt.Sprintf("%s.%s.part", target, uuid.NewString())
	defer os.Remove(tmp)

	if mediaType == "VIDEO" {
		if err := runFFmpegThumbnail(ctx, source, tmp, size, true); err != nil {
			if err := runFFmpegThumbnail(ctx, source, tmp, size, false); err != nil {
				return "", err
			}
		}
	} else {
		if err := runFFmpegThumbnail(ctx, source, tmp, size, false); err != nil {
			return "", err
		}
	}
	if err := os.Rename(tmp, target); err != nil {
		return "", err
	}
	return target, nil
}

func (s *Store) CacheThumbnail(ctx context.Context, id string, size int, input []byte) (string, error) {
	target, err := s.PathFor(id, size)
	if err != nil {
		return "", err
	}
	tmpIn := fmt.Sprintf("%s.%s.in.part", target, uuid.NewString())
	tmpOut := fmt.Sprintf("%s.%s.part", target, uuid.NewString())
	defer os.Remove(tmpIn)
	defer os.Remove(tmpOut)

	if err := os.WriteFile(tmpIn, input, 0o600); err != nil {
		return "", err
	}
	if err := runFFmpegThumbnail(ctx, tmpIn, tmpOut, size, false); err != nil {
		return "", err
	}
	if err := os.Rename(tmpOut, target); err != nil {
		return "", err
	}
	return target, nil
}

func (s *Store) Stage(ctx context.Context, id string, body io.Reader, mediaType string) (MediaInfo, error) {
	if body == nil {
		return MediaInfo{}, model.NewFault("LINE_DOWNLOAD", "Empty LINE response.", true, 0)
	}
	target, err := s.PathFor(id, 0)
	if err != nil {
		return MediaInfo{}, err
	}
	tmp := fmt.Sprintf("%s.%s.part", target, uuid.NewString())
	defer os.Remove(tmp)

	maxMB := s.Env.MaxImageSizeMB
	if mediaType == "VIDEO" {
		maxMB = s.Env.MaxVideoSizeMB
	}
	maxBytes := int64(maxMB) * 1048576

	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return MediaInfo{}, err
	}
	written, copyErr := io.Copy(f, io.LimitReader(body, maxBytes+1))
	closeErr := f.Close()
	if copyErr != nil {
		return MediaInfo{}, model.NewFault("LINE_DOWNLOAD", "Failed reading LINE media.", true, 0)
	}
	if closeErr != nil {
		return MediaInfo{}, closeErr
	}
	if written > maxBytes {
		return MediaInfo{}, model.NewFault("FILE_SIZE", "LINE media exceeds size limit.", false, 0)
	}
	var info MediaInfo
	if mediaType == "VIDEO" {
		info, err = InspectVideo(ctx, tmp)
	} else {
		info, err = Inspect(tmp)
	}
	// Keep the bytes even when inspection fails: never silently delete an unuploaded original.
	if renameErr := os.Rename(tmp, target); renameErr != nil {
		return MediaInfo{}, renameErr
	}
	info.StagingPath = target
	return info, err
}

func Touch(filePath string) error {
	now := time.Now()
	return os.Chtimes(filePath, now, now)
}
