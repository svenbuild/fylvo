package thumb

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"

	"github.com/cloudreve/Cloudreve/v4/pkg/setting"
)

// jpegExts are the extensions of inputs that are written as JPEG right away (Fylvo).
var jpegExts = []string{"jpg", "jpeg", "jpe"}

// toJPEGIfOpaque replaces a PNG thumbnail at path by a JPEG when JPEG is the configured format
// and the image has no transparent pixel (Fylvo). A JPEG is several times smaller; a
// transparent image stays PNG, so logos and cut-outs keep their transparency. Anything that is
// not a PNG of at most twice the thumbnail size is left as it is.
func toJPEGIfOpaque(path string, encode *setting.ThumbEncode, maxWidth, maxHeight int) error {
	if encode == nil || encode.Format != "jpg" {
		return nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || format != "png" {
		return nil
	}
	if cfg.Width > 2*maxWidth || cfg.Height > 2*maxHeight {
		return nil
	}

	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("failed to decode PNG: %w", err)
	}
	if o, ok := img.(interface{ Opaque() bool }); !ok || !o.Opaque() {
		return nil
	}

	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: encode.Quality}); err != nil {
		return fmt.Errorf("failed to encode JPEG: %w", err)
	}
	return os.WriteFile(path, out.Bytes(), 0644)
}
