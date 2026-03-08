package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Yeti47/polar-resolve/internal/image"
	"github.com/Yeti47/polar-resolve/internal/model"
	"github.com/Yeti47/polar-resolve/internal/upscaler"
	"github.com/spf13/cobra"
)

var (
	imgInput       string
	imgOutput      string
	imgFormat      string
	imgTileSize    int
	imgTileOverlap int
)

var imageCmd = &cobra.Command{
	Use:   "image",
	Short: "Upscale one or more images 4×",
	Long:  `Upscale images using Real-ESRGAN-General-x4vr. Supports PNG, JPEG, and WebP.`,
	RunE:  runImage,
}

func init() {
	imageCmd.Flags().StringVarP(&imgInput, "input", "i", "", "Input image file or glob pattern (required)")
	imageCmd.Flags().StringVarP(&imgOutput, "output", "o", "", "Output file or directory (default: input with _4x suffix)")
	imageCmd.Flags().StringVar(&imgFormat, "format", "", "Output format: png, jpg, webp (default: same as input)")
	imageCmd.Flags().IntVar(&imgTileSize, "tile-size", 128, "Tile size for inference (pixels)")
	imageCmd.Flags().IntVar(&imgTileOverlap, "tile-overlap", 16, "Overlap between tiles (pixels)")

	_ = imageCmd.MarkFlagRequired("input")

	rootCmd.AddCommand(imageCmd)
}

func runImage(cmd *cobra.Command, args []string) error {
	// Resolve relative paths to workspace directories
	imgInput = ResolveInputPath(imgInput)
	imgOutput = ResolveOutputPath(imgOutput)

	// Resolve input files via glob
	matches, err := filepath.Glob(imgInput)
	if err != nil {
		return fmt.Errorf("invalid glob pattern: %w", err)
	}
	if len(matches) == 0 {
		return fmt.Errorf("no files matching: %s", imgInput)
	}

	// Resolve model
	modelPath := GetModelPath()
	log := NewLogger()
	if modelPath == "" {
		Logf("No model specified, will auto-download...")
		modelPath, err = model.EnsureModel(log)
		if err != nil {
			return fmt.Errorf("failed to get model: %w", err)
		}
	}
	Logf("Using model: %s", modelPath)

	// Initialize upscaler
	u, err := upscaler.New(upscaler.Config{
		ModelPath:   modelPath,
		Device:      GetDevice(),
		LibPath:     GetLibPath(),
		TileSize:    imgTileSize,
		TileOverlap: imgTileOverlap,
		Logger:      log,
	})
	if err != nil {
		return fmt.Errorf("failed to initialize upscaler: %w", err)
	}
	defer u.Close()

	for idx, inputPath := range matches {
		if len(matches) > 1 {
			fmt.Printf("[%d/%d] %s\n", idx+1, len(matches), inputPath)
		}

		// Load image
		Logf("Loading %s...", inputPath)
		img, err := image.Load(inputPath)
		if err != nil {
			return fmt.Errorf("failed to load %s: %w", inputPath, err)
		}

		// Upscale
		Logf("Upscaling %dx%d -> %dx%d...", img.Width, img.Height, img.Width*4, img.Height*4)
		outImg, err := u.UpscaleImage(img)
		if err != nil {
			return fmt.Errorf("failed to upscale %s: %w", inputPath, err)
		}

		// Determine output path
		outPath := resolveOutputPath(inputPath, imgOutput, imgFormat, len(matches) > 1)

		// Save
		Logf("Saving to %s...", outPath)
		if err := image.Save(outImg, outPath); err != nil {
			return fmt.Errorf("failed to save %s: %w", outPath, err)
		}

		fmt.Printf("  -> %s (%dx%d)\n", outPath, outImg.Width, outImg.Height)
	}

	return nil
}

// resolveOutputPath determines the output file path.
func resolveOutputPath(inputPath, outputFlag, formatFlag string, batch bool) string {
	ext := filepath.Ext(inputPath)
	base := strings.TrimSuffix(filepath.Base(inputPath), ext)

	// Override format extension
	if formatFlag != "" {
		ext = "." + formatFlag
	}

	if outputFlag == "" {
		// Default: same directory, _4x suffix
		dir := filepath.Dir(inputPath)
		return filepath.Join(dir, base+"_4x"+ext)
	}

	info, err := os.Stat(outputFlag)
	if err == nil && info.IsDir() {
		// Output is a directory
		return filepath.Join(outputFlag, base+"_4x"+ext)
	}

	if batch {
		// Batch mode: treat output as directory
		_ = os.MkdirAll(outputFlag, 0o755)
		return filepath.Join(outputFlag, base+"_4x"+ext)
	}

	// Single file output
	return outputFlag
}
