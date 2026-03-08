package upscaler

import (
	"github.com/Yeti47/polar-resolve/internal/image"
)

// Tile represents a rectangular region of an image to be processed independently.
type Tile struct {
	// Position in the source image (top-left corner)
	X, Y int
	// Size of this tile in the source image
	Width, Height int
	// The tile's image data
	Image *image.RGBImage
}

// TileConfig holds tiling parameters.
type TileConfig struct {
	TileSize int // Base tile size in pixels (default 512)
	Overlap  int // Overlap between adjacent tiles in pixels (default 32)
}

// DefaultTileConfig returns default tiling parameters.
func DefaultTileConfig() TileConfig {
	return TileConfig{
		TileSize: ModelInputSize,
		Overlap:  16,
	}
}

// SplitIntoTiles divides an image into overlapping tiles for inference.
// Each tile is TileSize × TileSize (or smaller at the edges).
func SplitIntoTiles(img *image.RGBImage, cfg TileConfig) []Tile {
	tiles := []Tile{}

	step := cfg.TileSize - cfg.Overlap
	if step <= 0 {
		step = 1
	}

	for y := 0; y < img.Height; y += step {
		for x := 0; x < img.Width; x += step {
			// Calculate tile dimensions (clamp to image bounds)
			tw := cfg.TileSize
			th := cfg.TileSize
			if x+tw > img.Width {
				tw = img.Width - x
			}
			if y+th > img.Height {
				th = img.Height - y
			}

			// Extract tile pixels
			tileImg := image.NewRGBImage(tw, th)
			for ty := 0; ty < th; ty++ {
				for tx := 0; tx < tw; tx++ {
					r, g, b := img.At(x+tx, y+ty)
					tileImg.Set(tx, ty, r, g, b)
				}
			}

			tiles = append(tiles, Tile{
				X:      x,
				Y:      y,
				Width:  tw,
				Height: th,
				Image:  tileImg,
			})
		}
	}

	return tiles
}

// MergeTiles reconstructs a full image from overlapping upscaled tiles.
// The scale factor is applied to tile positions and the output dimensions.
// Overlapping regions are blended using linear interpolation.
func MergeTiles(tiles []Tile, originalWidth, originalHeight, scale int, cfg TileConfig) *image.RGBImage {
	outW := originalWidth * scale
	outH := originalHeight * scale

	// Accumulator for weighted pixel values
	accum := make([]float32, outW*outH*3)
	weight := make([]float32, outW*outH)

	scaledOverlap := cfg.Overlap * scale

	for _, tile := range tiles {
		tileOutW := tile.Image.Width
		tileOutH := tile.Image.Height
		tileOutX := tile.X * scale
		tileOutY := tile.Y * scale

		for ty := 0; ty < tileOutH; ty++ {
			for tx := 0; tx < tileOutW; tx++ {
				outX := tileOutX + tx
				outY := tileOutY + ty

				if outX >= outW || outY >= outH {
					continue
				}

				r, g, b := tile.Image.At(tx, ty)

				// Calculate blend weight based on distance from tile edges
				w := blendWeight(tx, ty, tileOutW, tileOutH, scaledOverlap)

				idx := outY*outW + outX
				accum[idx*3] += r * w
				accum[idx*3+1] += g * w
				accum[idx*3+2] += b * w
				weight[idx] += w
			}
		}
	}

	// Normalize by accumulated weights
	result := image.NewRGBImage(outW, outH)
	for i := 0; i < outW*outH; i++ {
		if weight[i] > 0 {
			result.Data[i*3] = accum[i*3] / weight[i]
			result.Data[i*3+1] = accum[i*3+1] / weight[i]
			result.Data[i*3+2] = accum[i*3+2] / weight[i]
		}
	}

	return result
}

// blendWeight computes a linear blending weight for a pixel at (x, y) within a tile.
// Pixels near the edges (within the overlap zone) get reduced weights for smooth blending.
func blendWeight(x, y, w, h, overlap int) float32 {
	if overlap <= 0 {
		return 1.0
	}

	wx := float32(1.0)
	wy := float32(1.0)

	// Left edge ramp
	if x < overlap {
		wx = float32(x+1) / float32(overlap+1)
	}
	// Right edge ramp
	if x >= w-overlap {
		wx2 := float32(w-x) / float32(overlap+1)
		if wx2 < wx {
			wx = wx2
		}
	}
	// Top edge ramp
	if y < overlap {
		wy = float32(y+1) / float32(overlap+1)
	}
	// Bottom edge ramp
	if y >= h-overlap {
		wy2 := float32(h-y) / float32(overlap+1)
		if wy2 < wy {
			wy = wy2
		}
	}

	return wx * wy
}
