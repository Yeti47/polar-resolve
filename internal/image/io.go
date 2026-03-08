package image

import (
	"fmt"
	goimage "image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// RGBImage represents a raw RGB image with float32 pixel values in [0, 1].
// Data is stored in HWC (Height, Width, Channel) order, row-major.
type RGBImage struct {
	Width  int
	Height int
	Data   []float32
}

// NewRGBImage creates a zero-initialized RGBImage.
func NewRGBImage(width, height int) *RGBImage {
	return &RGBImage{
		Width:  width,
		Height: height,
		Data:   make([]float32, width*height*3),
	}
}

// At returns the RGB pixel value at (x, y).
func (img *RGBImage) At(x, y int) (r, g, b float32) {
	idx := (y*img.Width + x) * 3
	return img.Data[idx], img.Data[idx+1], img.Data[idx+2]
}

// Set sets the RGB pixel value at (x, y).
func (img *RGBImage) Set(x, y int, r, g, b float32) {
	idx := (y*img.Width + x) * 3
	img.Data[idx] = r
	img.Data[idx+1] = g
	img.Data[idx+2] = b
}

// ToNCHW converts the HWC float32 image to an NCHW float32 tensor.
// Returns a slice of shape [1, 3, H, W].
func (img *RGBImage) ToNCHW() []float32 {
	h, w := img.Height, img.Width
	tensor := make([]float32, 1*3*h*w)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			srcIdx := (y*w + x) * 3
			rIdx := 0*h*w + y*w + x
			gIdx := 1*h*w + y*w + x
			bIdx := 2*h*w + y*w + x
			tensor[rIdx] = img.Data[srcIdx]
			tensor[gIdx] = img.Data[srcIdx+1]
			tensor[bIdx] = img.Data[srcIdx+2]
		}
	}
	return tensor
}

// FromNCHW creates an RGBImage from an NCHW float32 tensor of shape [1, 3, H, W].
func FromNCHW(tensor []float32, width, height int) *RGBImage {
	img := NewRGBImage(width, height)
	hw := height * width
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			px := y*width + x
			r := clampf(tensor[0*hw+px], 0, 1)
			g := clampf(tensor[1*hw+px], 0, 1)
			b := clampf(tensor[2*hw+px], 0, 1)
			img.Set(x, y, r, g, b)
		}
	}
	return img
}

// FromRawRGB creates an RGBImage from raw RGB24 byte data (3 bytes per pixel, row-major).
func FromRawRGB(data []byte, width, height int) *RGBImage {
	img := NewRGBImage(width, height)
	for i := 0; i < width*height; i++ {
		img.Data[i*3] = float32(data[i*3]) / 255.0
		img.Data[i*3+1] = float32(data[i*3+1]) / 255.0
		img.Data[i*3+2] = float32(data[i*3+2]) / 255.0
	}
	return img
}

// ToRawRGB converts the RGBImage to raw RGB24 byte data (3 bytes per pixel, row-major).
func (img *RGBImage) ToRawRGB() []byte {
	data := make([]byte, img.Width*img.Height*3)
	for i := 0; i < img.Width*img.Height; i++ {
		data[i*3] = uint8(clampf(img.Data[i*3]*255.0+0.5, 0, 255))
		data[i*3+1] = uint8(clampf(img.Data[i*3+1]*255.0+0.5, 0, 255))
		data[i*3+2] = uint8(clampf(img.Data[i*3+2]*255.0+0.5, 0, 255))
	}
	return data
}

// Load reads an image from disk and returns it as an RGBImage.
func Load(path string) (*RGBImage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	img, _, err := goimage.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}

	bounds := img.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()

	nrgba := goimage.NewNRGBA(goimage.Rect(0, 0, w, h))
	draw.Copy(nrgba, goimage.Point{}, img, bounds, draw.Src, nil)

	result := NewRGBImage(w, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			off := nrgba.PixOffset(x, y)
			result.Data[(y*w+x)*3] = float32(nrgba.Pix[off]) / 255.0
			result.Data[(y*w+x)*3+1] = float32(nrgba.Pix[off+1]) / 255.0
			result.Data[(y*w+x)*3+2] = float32(nrgba.Pix[off+2]) / 255.0
		}
	}

	return result, nil
}

// Save writes an RGBImage to disk. Format is inferred from the file extension.
func Save(img *RGBImage, path string) error {
	ext := strings.ToLower(filepath.Ext(path))

	nrgba := goimage.NewNRGBA(goimage.Rect(0, 0, img.Width, img.Height))
	for y := 0; y < img.Height; y++ {
		for x := 0; x < img.Width; x++ {
			off := nrgba.PixOffset(x, y)
			srcIdx := (y*img.Width + x) * 3
			nrgba.Pix[off] = uint8(clampf(img.Data[srcIdx]*255.0+0.5, 0, 255))
			nrgba.Pix[off+1] = uint8(clampf(img.Data[srcIdx+1]*255.0+0.5, 0, 255))
			nrgba.Pix[off+2] = uint8(clampf(img.Data[srcIdx+2]*255.0+0.5, 0, 255))
			nrgba.Pix[off+3] = 255
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	switch ext {
	case ".png":
		return png.Encode(f, nrgba)
	case ".jpg", ".jpeg":
		return jpeg.Encode(f, nrgba, &jpeg.Options{Quality: 95})
	default:
		return png.Encode(f, nrgba)
	}
}

func clampf(v, min, max float32) float32 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
