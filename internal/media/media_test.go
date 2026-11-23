package media

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"golang.org/x/image/webp"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"rivosnap/internal/config"
	"rivosnap/internal/model"
	"testing"
	"time"
)

func TestVideoDimensionLimit(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	source := filepath.Join(dir, "source")
	if err := os.WriteFile(source, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		height  string
		allowed bool
	}{{"10000", true}, {"10001", false}} {
		probe := "#!/bin/sh\necho '{\"streams\":[{\"width\":10000,\"height\":" + tc.height + "}],\"format\":{\"format_name\":\"mp4\"}}'\n"
		if err := os.WriteFile(filepath.Join(dir, "ffprobe"), []byte(probe), 0700); err != nil {
			t.Fatal(err)
		}
		_, err := InspectVideo(context.Background(), source)
		if (err == nil) != tc.allowed {
			t.Fatalf("height %s: %v", tc.height, err)
		}
	}
}
func TestNoThumbnailPublishedWhenEncoderFails(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	s := NewStore(config.Env{ThumbnailPath: dir})
	id := "00000000-0000-4000-8000-000000000001"
	if _, err := s.CacheThumbnail(context.Background(), id, 320, []byte("fixture")); err == nil {
		t.Fatal("reported success without encoder")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("leftover thumbnail files: %v, %v", entries, err)
	}
}

func TestShortVideoThumbnailFallsBackToFirstFrame(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg required")
	}
	dir := t.TempDir()
	s := NewStore(config.Env{StagingPath: dir, ThumbnailPath: dir})
	id := "00000000-0000-4000-8000-000000000001"
	source, err := s.PathFor(id, 0)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "color=s=32x24:d=0.4", "-c:v", "mpeg4", "-f", "mp4", source).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v: %s", err, out)
	}
	path, err := s.Thumbnail(context.Background(), id, 320, "VIDEO")
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	im, err := webp.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if im.Bounds().Dx() != 32 || im.Bounds().Dy() != 24 {
		t.Fatalf("unexpected dimensions: %v", im.Bounds())
	}
}

func TestInspectDoesNotFullyDecode(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 48))
	for y := range 48 {
		for x := range 64 {
			img.Set(x, y, color.RGBA{R: uint8(x * 4), G: uint8(y * 5), B: uint8(x ^ y), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	sos := bytes.Index(buf.Bytes(), []byte{0xFF, 0xDA})
	if sos < 0 {
		t.Fatal("no SOS marker")
	}
	path := filepath.Join(t.TempDir(), "truncated.jpg")
	if err := os.WriteFile(path, buf.Bytes()[:sos+20], 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := Inspect(path)
	if err != nil {
		t.Fatalf("Inspect decoded beyond the header: %v", err)
	}
	if info.MimeType != "image/jpeg" || info.Width != 64 || info.Height != 48 || info.Sha256 == "" {
		t.Fatalf("unexpected info: %+v", info)
	}
}

func stageStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	return NewStore(config.Env{StagingPath: dir, ThumbnailPath: dir, MaxImageSizeMB: 1}), dir
}

func assertNoPart(t *testing.T, dir string) {
	t.Helper()
	parts, err := filepath.Glob(filepath.Join(dir, "*.part"))
	if err != nil || len(parts) != 0 {
		t.Fatalf("leftover .part files: %v, %v", parts, err)
	}
}

func TestStageKeepsFileOnInspectFailure(t *testing.T) {
	s, dir := stageStore(t)
	junk := make([]byte, 4096)
	if _, err := rand.Read(junk); err != nil {
		t.Fatal(err)
	}
	junk[0] = 0 // never a valid magic number
	info, err := s.Stage(context.Background(), "00000000-0000-4000-8000-000000000002", bytes.NewReader(junk), "IMAGE")
	if err == nil {
		t.Fatal("random bytes accepted as an image")
	}
	if info.StagingPath == "" {
		t.Fatal("inspect failure did not report the kept staging path")
	}
	kept, err := os.ReadFile(info.StagingPath)
	if err != nil || !bytes.Equal(kept, junk) {
		t.Fatalf("original not kept intact: %v", err)
	}
	assertNoPart(t, dir)
}

func TestStageDeletesPartOnOversize(t *testing.T) {
	s, dir := stageStore(t)
	info, err := s.Stage(context.Background(), "00000000-0000-4000-8000-000000000003", bytes.NewReader(make([]byte, 1048577)), "IMAGE")
	if err == nil || info.StagingPath != "" {
		t.Fatalf("oversize accepted: %+v, %v", info, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("leftover files: %v, %v", entries, err)
	}
}

// probeStub puts an ffprobe stub with script first in PATH for this test.
func probeStub(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ffprobe"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// Tool and filesystem faults are transient (AUD3-007); a file that makes ffprobe fail or hang is not.
func TestInspectVideoToolFaults(t *testing.T) {
	video := filepath.Join(t.TempDir(), "clip.mp4")
	if err := os.WriteFile(video, []byte("not really a video"), 0o600); err != nil {
		t.Fatal(err)
	}
	defaultTimeout := probeTimeout
	t.Cleanup(func() { probeTimeout = defaultTimeout })

	cases := []struct {
		name        string
		script      string
		absent      bool          // no ffprobe anywhere in PATH
		timeout     time.Duration // probeTimeout override, 0 keeps the default
		cancelAfter time.Duration // cancel the parent context after this long, 0 keeps it live
		wantCode    string
	}{
		{name: "killed with SIGKILL", script: "#!/bin/sh\nkill -9 $$\n", wantCode: "MEDIA_TOOL"},
		{name: "binary absent", absent: true, wantCode: "MEDIA_TOOL"},
		{name: "parent cancelled while running", script: "#!/bin/sh\nexec sleep 5\n", cancelAfter: 100 * time.Millisecond, wantCode: "MEDIA_TOOL"},
		{name: "exit 1 on garbage", script: "#!/bin/sh\nexit 1\n", wantCode: "UNSUPPORTED_VIDEO"},
		{name: "SIGSEGV", script: "#!/bin/sh\nkill -SEGV $$\n", wantCode: "UNSUPPORTED_VIDEO"},
		{name: "SIGABRT", script: "#!/bin/sh\nkill -ABRT $$\n", wantCode: "UNSUPPORTED_VIDEO"},
		{name: "inner timeout with live parent", script: "#!/bin/sh\nexec sleep 5\n", timeout: 200 * time.Millisecond, wantCode: "UNSUPPORTED_VIDEO"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			probeTimeout = defaultTimeout
			if tc.timeout > 0 {
				probeTimeout = tc.timeout
			}
			if tc.absent {
				t.Setenv("PATH", t.TempDir())
			} else {
				probeStub(t, tc.script)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancelAfter > 0 {
				time.AfterFunc(tc.cancelAfter, cancel)
			}

			_, err := InspectVideo(ctx, video)
			var f *model.Fault
			if !errors.As(err, &f) {
				t.Fatalf("expected a fault, got %v", err)
			}
			if f.Code != tc.wantCode {
				t.Fatalf("expected %s, got %s (%s)", tc.wantCode, f.Code, f.Message)
			}
			if wantTransient := tc.wantCode == "MEDIA_TOOL"; f.Transient != wantTransient {
				t.Fatalf("%s: transient = %v, want %v", f.Code, f.Transient, wantTransient)
			}
		})
	}
}

// A file that cannot be opened is an environment fault, not a bad image (AUD3-007).
func TestInspectOpenErrorIsNotUnsupported(t *testing.T) {
	var f *model.Fault
	// A path below a regular file fails stat with ENOTDIR, not ErrNotExist, and works as root too.
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectVideo(context.Background(), filepath.Join(blocker, "clip.mp4")); err == nil || errors.As(err, &f) {
		t.Fatalf("an unreadable video path must yield a non-fault error, got %v", err)
	}
	if _, err := InspectVideo(context.Background(), filepath.Join(t.TempDir(), "gone.mp4")); !errors.As(err, &f) || f.Code != "UNSUPPORTED_VIDEO" {
		t.Fatalf("a missing video must stay UNSUPPORTED_VIDEO, got %v", err)
	}
	if _, err := Inspect(t.TempDir()); err == nil || errors.As(err, &f) {
		t.Fatalf("a directory must yield a non-fault error, got %v", err)
	}

	if os.Geteuid() == 0 {
		t.Skip("root reads unreadable files")
	}
	unreadable := filepath.Join(t.TempDir(), "locked.jpg")
	if err := os.WriteFile(unreadable, []byte("\xff\xd8\xff"), 0o000); err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(unreadable); err == nil || errors.As(err, &f) {
		t.Fatalf("an unreadable file must yield a non-fault error, got %v", err)
	}
}
