package thumb

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/pkg/setting"
	"github.com/stretchr/testify/require"
)

func writeTestPNG(t *testing.T, w, h int, alpha uint8) string {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 90, A: 255})
		}
	}
	img.Set(w/2, h/2, color.NRGBA{R: 1, G: 2, B: 3, A: alpha})
	path := filepath.Join(t.TempDir(), "thumb")
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	require.NoError(t, os.WriteFile(path, buf.Bytes(), 0644))
	return path
}

func formatOf(t *testing.T, path string) string {
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	_, format, err := image.DecodeConfig(bytes.NewReader(data))
	require.NoError(t, err)
	return format
}

func TestToJPEGIfOpaque(t *testing.T) {
	jpg := &setting.ThumbEncode{Format: "jpg", Quality: 80}

	// An opaque PNG becomes a JPEG.
	opaque := writeTestPNG(t, 400, 300, 255)
	require.NoError(t, toJPEGIfOpaque(opaque, jpg, 400, 300))
	require.Equal(t, "jpeg", formatOf(t, opaque))

	// One transparent pixel keeps the PNG.
	transparent := writeTestPNG(t, 400, 300, 0)
	require.NoError(t, toJPEGIfOpaque(transparent, jpg, 400, 300))
	require.Equal(t, "png", formatOf(t, transparent))

	// Other formats configured: unchanged.
	png1 := writeTestPNG(t, 400, 300, 255)
	require.NoError(t, toJPEGIfOpaque(png1, &setting.ThumbEncode{Format: "png", Quality: 95}, 400, 300))
	require.Equal(t, "png", formatOf(t, png1))

	// Far larger than a thumbnail (e.g. an embedded cover passed through): not decoded.
	big := writeTestPNG(t, 1000, 700, 255)
	require.NoError(t, toJPEGIfOpaque(big, jpg, 400, 300))
	require.Equal(t, "png", formatOf(t, big))
}

func TestBytesPerPixel(t *testing.T) {
	require.Equal(t, int64(3), bytesPerPixel(color.YCbCrModel, []byte{0xFF, 0xD8, 0xFF, 0xC0}))
	// Progressive JPEG (SOF2): coefficients on top of the pixels.
	require.Equal(t, int64(8), bytesPerPixel(color.YCbCrModel, []byte{0xFF, 0xD8, 0xFF, 0xC2}))
	// Grey PNG may become RGBA (transparent colour), 16-bit grey RGBA64.
	require.Equal(t, int64(4), bytesPerPixel(color.GrayModel, nil))
	require.Equal(t, int64(8), bytesPerPixel(color.Gray16Model, nil))
	// 40 MP baseline JPEG passes, 24 MP progressive does not.
	require.LessOrEqual(t, int64(40e6)*bytesPerPixel(color.YCbCrModel, nil), int64(maxDecodeBytes))
	require.Greater(t, int64(24e6)*bytesPerPixel(color.YCbCrModel, []byte{0xFF, 0xC2}), int64(maxDecodeBytes))
}
