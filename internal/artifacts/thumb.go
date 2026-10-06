package artifacts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"golang.org/x/image/bmp"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/model"
)

// ThumbSize is the longest edge, in pixels, of a generated gallery thumbnail.
const ThumbSize = 320

// maxThumbPixels bounds the decoded size of a source image so a small file
// declaring huge dimensions cannot exhaust daemon memory.
const maxThumbPixels = 64 * 1000 * 1000

// ErrNoThumbnail means the artifact has no server-side thumbnail (unsupported
// format, or ffmpeg unavailable); the client falls back to a type icon.
var ErrNoThumbnail = errors.New("no thumbnail available")

// thumbSlots bounds concurrent generation: a gallery requests every tile at once.
var thumbSlots = make(chan struct{}, 4)

// lookFFmpeg is a test seam. The daemon often runs under launchd with a minimal
// PATH, so common Homebrew locations are probed explicitly.
var lookFFmpeg = func() (string, error) {
	if p, err := exec.LookPath("ffmpeg"); err == nil {
		return p, nil
	}
	for _, p := range []string{"/opt/homebrew/bin/ffmpeg", "/usr/local/bin/ffmpeg"} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", ErrNoThumbnail
}

// Thumbnail returns the path of a cached JPEG thumbnail for art, generating it
// on demand from srcPath (the already-resolved source file). The cache entry is
// reused while it is at least as new as the source.
func Thumbnail(ctx context.Context, taskID string, art *model.Artifact, srcPath string) (string, error) {
	if art.Type != model.ArtifactImage && art.Type != model.ArtifactVideo {
		return "", ErrNoThumbnail
	}
	srcInfo, err := os.Stat(srcPath)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(art.Filename))
	dir := filepath.Join(agent.ArtifactsDir(taskID), model.ArtifactThumbDir)
	out := filepath.Join(dir, hex.EncodeToString(sum[:8])+".jpg")
	if ti, err := os.Stat(out); err == nil && !ti.ModTime().Before(srcInfo.ModTime()) {
		return out, nil
	}

	select {
	case thumbSlots <- struct{}{}:
		defer func() { <-thumbSlots }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".thumb-*.tmp")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpName) //nolint:errcheck

	if art.Type == model.ArtifactImage {
		err = imageThumb(srcPath, tmpName)
	} else {
		err = videoThumb(ctx, srcPath, tmpName)
	}
	if err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, out); err != nil {
		return "", err
	}
	return out, nil
}

func imageThumb(src, dst string) error {
	f, err := os.Open(src) //nolint:gosec // G304: src is a manifest-gated, prefix-validated artifact path.
	if err != nil {
		return err
	}
	defer f.Close()
	cfg, format, err := image.DecodeConfig(f)
	if err != nil {
		if errors.Is(err, image.ErrFormat) {
			return ErrNoThumbnail
		}
		return err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > maxThumbPixels {
		return ErrNoThumbnail
	}
	if _, err := f.Seek(0, 0); err != nil {
		return err
	}
	var img image.Image
	if format == "bmp" {
		img, err = bmp.Decode(f)
	} else {
		img, _, err = image.Decode(f)
	}
	if err != nil {
		return err
	}
	return writeJPEGThumb(img, dst)
}

func writeJPEGThumb(img image.Image, dst string) error {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w > ThumbSize || h > ThumbSize {
		if w >= h {
			h = h * ThumbSize / w
			w = ThumbSize
		} else {
			w = w * ThumbSize / h
			h = ThumbSize
		}
		if w < 1 {
			w = 1
		}
		if h < 1 {
			h = 1
		}
	}
	// Flatten onto white so transparent PNG/GIF/WebP pixels don't become black in JPEG.
	canvas := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	xdraw.ApproxBiLinear.Scale(canvas, canvas.Bounds(), img, b, xdraw.Over, nil)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, canvas, &jpeg.Options{Quality: 80}); err != nil {
		return err
	}
	return os.WriteFile(dst, buf.Bytes(), 0o600)
}

// videoThumb extracts one frame with ffmpeg, preferring the 1s mark (past
// common black lead-ins) and falling back to frame 0 for very short clips.
func videoThumb(ctx context.Context, src, dst string) error {
	bin, err := lookFFmpeg()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	for _, seek := range []string{"1", "0"} {
		_ = os.Truncate(dst, 0)
		cmd := exec.CommandContext(ctx, bin, "-v", "error", "-ss", seek, "-i", src,
			"-frames:v", "1", "-vf", fmt.Sprintf("scale='min(%d,iw)':-2", ThumbSize),
			"-c:v", "mjpeg", "-f", "image2", "-y", dst)
		if err := cmd.Run(); err == nil {
			if fi, err := os.Stat(dst); err == nil && fi.Size() > 0 {
				return nil
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return ErrNoThumbnail
}
