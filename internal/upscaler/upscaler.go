package upscaler

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/Yeti47/polar-resolve/internal/image"
	ort "github.com/yalue/onnxruntime_go"
)

const (
	// ScaleFactor is the upscaling factor of the Real-ESRGAN model.
	ScaleFactor = 4

	// ModelInputSize is the fixed spatial input dimension expected by the ONNX model.
	// The Real-ESRGAN-General-x4v3 model expects input tensors of shape [1, 3, 128, 128].
	ModelInputSize = 128
)

// Config holds the configuration for the upscaler.
type Config struct {
	ModelPath   string // Path to the ONNX model file
	Device      string // Execution provider: "auto", "cpu", "rocm"
	LibPath     string // Path to libonnxruntime.so (empty = auto-detect)
	TileSize    int    // Tile size for inference
	TileOverlap int    // Overlap between tiles
	Verbose     bool   // Enable verbose logging
}

// Upscaler manages the ONNX runtime session for image upscaling.
type Upscaler struct {
	config     Config
	tileConfig TileConfig
	envReady   bool
}

// New creates a new Upscaler with the given configuration.
func New(cfg Config) (*Upscaler, error) {
	if cfg.TileSize <= 0 {
		cfg.TileSize = ModelInputSize
	}
	if cfg.TileOverlap <= 0 {
		cfg.TileOverlap = 16
	}

	u := &Upscaler{
		config: cfg,
		tileConfig: TileConfig{
			TileSize: cfg.TileSize,
			Overlap:  cfg.TileOverlap,
		},
	}

	// Resolve and set the shared library path
	libPath, err := u.resolveLibPath()
	if err != nil {
		return nil, fmt.Errorf("failed to resolve ONNX Runtime library: %w", err)
	}

	if cfg.Verbose {
		fmt.Printf("[polar-resolve] Using ONNX Runtime library: %s\n", libPath)
	}

	ort.SetSharedLibraryPath(libPath)

	// Set HSA_OVERRIDE_GFX_VERSION for RDNA2 compatibility when using ROCm
	if cfg.Device == "rocm" || cfg.Device == "auto" {
		_ = os.Setenv("HSA_OVERRIDE_GFX_VERSION", "10.3.0")
	}

	// Initialize the ONNX Runtime environment
	if err := ort.InitializeEnvironment(); err != nil {
		return nil, fmt.Errorf("failed to initialize ONNX Runtime: %w", err)
	}
	u.envReady = true

	if cfg.Verbose {
		fmt.Printf("[polar-resolve] ONNX Runtime version: %s\n", ort.GetVersion())
	}

	return u, nil
}

// Close releases ONNX Runtime resources.
func (u *Upscaler) Close() {
	if u.envReady {
		if err := ort.DestroyEnvironment(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to destroy ONNX Runtime environment: %v\n", err)
		}
	}
}

// UpscaleImage upscales a single image using tiling.
func (u *Upscaler) UpscaleImage(img *image.RGBImage) (*image.RGBImage, error) {
	tiles := SplitIntoTiles(img, u.tileConfig)

	if u.config.Verbose {
		fmt.Printf("[polar-resolve] Processing %d tiles (size=%d, overlap=%d)\n",
			len(tiles), u.tileConfig.TileSize, u.tileConfig.Overlap)
	}

	for i := range tiles {
		if u.config.Verbose {
			fmt.Printf("[polar-resolve] Tile %d/%d (%dx%d at %d,%d)\n",
				i+1, len(tiles), tiles[i].Width, tiles[i].Height, tiles[i].X, tiles[i].Y)
		}

		outTile, err := u.runInference(tiles[i].Image)
		if err != nil {
			return nil, fmt.Errorf("inference failed on tile %d: %w", i+1, err)
		}
		tiles[i].Image = outTile
	}

	result := MergeTiles(tiles, img.Width, img.Height, ScaleFactor, u.tileConfig)
	return result, nil
}

// padToModelSize pads a tile to the model's fixed input size (ModelInputSize x ModelInputSize)
// by reflecting pixels at the edges. Returns the padded image.
func padToModelSize(tile *image.RGBImage) *image.RGBImage {
	if tile.Width >= ModelInputSize && tile.Height >= ModelInputSize {
		return tile
	}

	padW := ModelInputSize
	padH := ModelInputSize
	padded := image.NewRGBImage(padW, padH)

	for y := 0; y < padH; y++ {
		// Reflect y coordinate into tile bounds
		sy := y
		if sy >= tile.Height {
			sy = tile.Height - 1 - (sy-tile.Height)%tile.Height
			if sy < 0 {
				sy = 0
			}
		}
		for x := 0; x < padW; x++ {
			// Reflect x coordinate into tile bounds
			sx := x
			if sx >= tile.Width {
				sx = tile.Width - 1 - (sx-tile.Width)%tile.Width
				if sx < 0 {
					sx = 0
				}
			}
			r, g, b := tile.At(sx, sy)
			padded.Set(x, y, r, g, b)
		}
	}

	return padded
}

// cropOutput extracts the top-left region of an upscaled image,
// corresponding to the original (unpadded) tile area.
func cropOutput(img *image.RGBImage, w, h int) *image.RGBImage {
	if img.Width == w && img.Height == h {
		return img
	}
	cropped := image.NewRGBImage(w, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, b := img.At(x, y)
			cropped.Set(x, y, r, g, b)
		}
	}
	return cropped
}

// runInference runs the ONNX model on a single tile image.
// Tiles smaller than ModelInputSize are padded; the output is cropped back.
func (u *Upscaler) runInference(tile *image.RGBImage) (*image.RGBImage, error) {
	origW := tile.Width
	origH := tile.Height

	// Pad tile to model's fixed input size if necessary
	padded := padToModelSize(tile)

	var h, w int64 = int64(padded.Height), int64(padded.Width)
	outH := h * ScaleFactor
	outW := w * ScaleFactor

	// Prepare input tensor (NCHW layout)
	inputData := padded.ToNCHW()
	inputShape := ort.NewShape(1, 3, h, w)
	inputTensor, err := ort.NewTensor(inputShape, inputData)
	if err != nil {
		return nil, fmt.Errorf("create input tensor: %w", err)
	}
	defer inputTensor.Destroy()

	// Prepare output tensor
	outputShape := ort.NewShape(1, 3, outH, outW)
	outputData := make([]float32, 1*3*outH*outW)
	outputTensor, err := ort.NewTensor(outputShape, outputData)
	if err != nil {
		return nil, fmt.Errorf("create output tensor: %w", err)
	}
	defer outputTensor.Destroy()

	// Create session options and configure execution provider
	options, err := ort.NewSessionOptions()
	if err != nil {
		return nil, fmt.Errorf("create session options: %w", err)
	}
	defer options.Destroy()

	if err := u.configureProvider(options); err != nil {
		return nil, fmt.Errorf("configure execution provider: %w", err)
	}

	if err := options.SetIntraOpNumThreads(runtime.NumCPU()); err != nil {
		return nil, fmt.Errorf("set thread count: %w", err)
	}

	// Create and run session
	session, err := ort.NewAdvancedSession(
		u.config.ModelPath,
		[]string{"image"},
		[]string{"upscaled_image"},
		[]ort.ArbitraryTensor{inputTensor},
		[]ort.ArbitraryTensor{outputTensor},
		options,
	)
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	defer session.Destroy()

	if err := session.Run(); err != nil {
		return nil, fmt.Errorf("run inference: %w", err)
	}

	result := image.FromNCHW(outputTensor.GetData(), int(outW), int(outH))

	// Crop output back to original tile's upscaled dimensions
	result = cropOutput(result, origW*ScaleFactor, origH*ScaleFactor)
	return result, nil
}

// configureProvider sets up the execution provider on the session options.
// Uses the generic AppendExecutionProvider API (available since ORT 1.12 / Go bindings v1.22).
func (u *Upscaler) configureProvider(options *ort.SessionOptions) error {
	device := u.config.Device

	if device == "rocm" || device == "auto" {
		err := options.AppendExecutionProvider("ROCMExecutionProvider", map[string]string{
			"device_id": "0",
		})
		if err != nil {
			if device == "rocm" {
				return fmt.Errorf("ROCm execution provider not available: %w", err)
			}
			if u.config.Verbose {
				fmt.Printf("[polar-resolve] ROCm not available, falling back to CPU: %v\n", err)
			}
		} else {
			if u.config.Verbose {
				fmt.Println("[polar-resolve] Using ROCMExecutionProvider")
			}
			return nil
		}
	}

	if u.config.Verbose {
		fmt.Println("[polar-resolve] Using CPUExecutionProvider")
	}
	return nil
}

// resolveLibPath finds the appropriate libonnxruntime.so.
func (u *Upscaler) resolveLibPath() (string, error) {
	// 1. Explicit path from config
	if u.config.LibPath != "" {
		if _, err := os.Stat(u.config.LibPath); err != nil {
			return "", fmt.Errorf("specified library not found: %s", u.config.LibPath)
		}
		return u.config.LibPath, nil
	}

	// 2. Check well-known container path (set by Dockerfile)
	containerLib := "/opt/onnxruntime/lib/libonnxruntime.so"
	if _, err := os.Stat(containerLib); err == nil {
		return containerLib, nil
	}

	// 3. Check cache directory
	home, err := os.UserHomeDir()
	if err == nil {
		cacheLib := filepath.Join(home, ".cache", "polar-resolve", "lib", "libonnxruntime.so")
		if _, err := os.Stat(cacheLib); err == nil {
			return cacheLib, nil
		}
	}

	// 4. Check common system paths
	systemPaths := []string{
		"/usr/lib/libonnxruntime.so",
		"/usr/local/lib/libonnxruntime.so",
		"/opt/rocm/lib/libonnxruntime.so",
	}
	for _, p := range systemPaths {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	return "", fmt.Errorf("libonnxruntime.so not found; use --lib-path or run in the Docker container")
}
