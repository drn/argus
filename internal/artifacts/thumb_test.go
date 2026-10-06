package artifacts

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func writePNG(t *testing.T, path string, w, h int, c color.Color) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	testutil.NoError(t, png.Encode(&buf, img))
	testutil.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	testutil.NoError(t, os.WriteFile(path, buf.Bytes(), 0o600))
}

func decodeJPEG(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path)
	testutil.NoError(t, err)
	defer f.Close()
	img, err := jpeg.Decode(f)
	testutil.NoError(t, err)
	return img
}

func TestThumbnail_ImageDownscaleAndCache(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	src := filepath.Join(agent.ArtifactsDir("task"), "shots", "wide.png")
	writePNG(t, src, 1000, 500, color.NRGBA{R: 200, A: 255})
	art := &model.Artifact{TaskID: "task", Filename: "shots/wide.png", Type: model.ArtifactImage}

	out, err := Thumbnail(context.Background(), "task", art, src)
	testutil.NoError(t, err)
	b := decodeJPEG(t, out).Bounds()
	testutil.Equal(t, b.Dx(), ThumbSize)
	testutil.Equal(t, b.Dy(), ThumbSize/2)

	// Second call reuses the cache entry (same path, untouched mtime).
	st1, _ := os.Stat(out)
	out2, err := Thumbnail(context.Background(), "task", art, src)
	testutil.NoError(t, err)
	testutil.Equal(t, out2, out)
	st2, _ := os.Stat(out2)
	testutil.True(t, st1.ModTime().Equal(st2.ModTime()))

	// A newer source invalidates the cache.
	writePNG(t, src, 100, 400, color.NRGBA{B: 200, A: 255})
	future := time.Now().Add(2 * time.Second)
	testutil.NoError(t, os.Chtimes(src, future, future))
	out3, err := Thumbnail(context.Background(), "task", art, src)
	testutil.NoError(t, err)
	b = decodeJPEG(t, out3).Bounds()
	testutil.Equal(t, b.Dy(), ThumbSize)
	testutil.Equal(t, b.Dx(), 80)
}

func TestThumbnail_SmallImageNotUpscaled(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	src := filepath.Join(agent.ArtifactsDir("task"), "s.png")
	writePNG(t, src, 40, 30, color.NRGBA{G: 255, A: 255})
	out, err := Thumbnail(context.Background(), "task", &model.Artifact{Filename: "s.png", Type: model.ArtifactImage}, src)
	testutil.NoError(t, err)
	b := decodeJPEG(t, out).Bounds()
	testutil.Equal(t, b.Dx(), 40)
	testutil.Equal(t, b.Dy(), 30)
}

func TestThumbnail_TransparencyFlattensToWhite(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	src := filepath.Join(agent.ArtifactsDir("task"), "t.png")
	writePNG(t, src, 20, 20, color.NRGBA{A: 0})
	out, err := Thumbnail(context.Background(), "task", &model.Artifact{Filename: "t.png", Type: model.ArtifactImage}, src)
	testutil.NoError(t, err)
	r, g, bl, _ := decodeJPEG(t, out).At(10, 10).RGBA()
	testutil.True(t, r>>8 > 240 && g>>8 > 240 && bl>>8 > 240)
}

func TestThumbnail_Unsupported(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := agent.ArtifactsDir("task")
	testutil.NoError(t, os.MkdirAll(dir, 0o700))
	text := filepath.Join(dir, "a.txt")
	testutil.NoError(t, os.WriteFile(text, []byte("hi"), 0o600))
	svg := filepath.Join(dir, "a.svg")
	testutil.NoError(t, os.WriteFile(svg, []byte("<svg/>"), 0o600))
	bad := filepath.Join(dir, "bad.png")
	testutil.NoError(t, os.WriteFile(bad, []byte("not an image"), 0o600))

	for name, art := range map[string]*model.Artifact{
		"text":    {Filename: "a.txt", Type: model.ArtifactText},
		"svg":     {Filename: "a.svg", Type: model.ArtifactImage},
		"corrupt": {Filename: "bad.png", Type: model.ArtifactImage},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Thumbnail(context.Background(), "task", art, filepath.Join(dir, art.Filename))
			testutil.True(t, errors.Is(err, ErrNoThumbnail))
		})
	}
	t.Run("missing source", func(t *testing.T) {
		_, err := Thumbnail(context.Background(), "task", &model.Artifact{Filename: "x.png", Type: model.ArtifactImage}, filepath.Join(dir, "x.png"))
		testutil.True(t, err != nil && !errors.Is(err, ErrNoThumbnail))
	})
}

func TestThumbnail_OversizedDimensionsRejected(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	src := filepath.Join(agent.ArtifactsDir("task"), "huge.png")
	// 9000x9000 = 81 MP, a tiny solid-color file that would decode to ~324 MB.
	writePNG(t, src, 9000, 9000, color.NRGBA{A: 255})
	_, err := Thumbnail(context.Background(), "task", &model.Artifact{Filename: "huge.png", Type: model.ArtifactImage}, src)
	testutil.True(t, errors.Is(err, ErrNoThumbnail))
}

func TestThumbnail_VideoWithoutFFmpeg(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	old := lookFFmpeg
	lookFFmpeg = func() (string, error) { return "", ErrNoThumbnail }
	t.Cleanup(func() { lookFFmpeg = old })
	src := filepath.Join(agent.ArtifactsDir("task"), "v.mp4")
	testutil.NoError(t, os.MkdirAll(filepath.Dir(src), 0o700))
	testutil.NoError(t, os.WriteFile(src, []byte("x"), 0o600))
	_, err := Thumbnail(context.Background(), "task", &model.Artifact{Filename: "v.mp4", Type: model.ArtifactVideo}, src)
	testutil.True(t, errors.Is(err, ErrNoThumbnail))
}

func TestThumbnail_VideoFFmpeg(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns ffmpeg")
	}
	bin, err := lookFFmpeg()
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	t.Setenv("HOME", t.TempDir())
	src := filepath.Join(agent.ArtifactsDir("task"), "v.mp4")
	testutil.NoError(t, os.MkdirAll(filepath.Dir(src), 0o700))
	gen := exec.Command(bin, "-v", "error", "-f", "lavfi", "-i", "testsrc=size=640x360:rate=10:duration=0.5",
		"-pix_fmt", "yuv420p", "-y", src)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg can't synthesize test clip: %v %s", err, out)
	}
	out, err := Thumbnail(context.Background(), "task", &model.Artifact{Filename: "v.mp4", Type: model.ArtifactVideo}, src)
	testutil.NoError(t, err)
	b := decodeJPEG(t, out).Bounds()
	testutil.Equal(t, b.Dx(), ThumbSize)
}

func TestThumbnail_CanceledContextWhileQueued(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	src := filepath.Join(agent.ArtifactsDir("task"), "c.png")
	writePNG(t, src, 10, 10, color.NRGBA{A: 255})
	for i := 0; i < cap(thumbSlots); i++ {
		thumbSlots <- struct{}{}
	}
	t.Cleanup(func() {
		for i := 0; i < cap(thumbSlots); i++ {
			<-thumbSlots
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Thumbnail(ctx, "task", &model.Artifact{Filename: "c.png", Type: model.ArtifactImage}, src)
	testutil.ErrorIs(t, err, context.Canceled)
}
