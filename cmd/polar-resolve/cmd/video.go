package cmd

import (
	"fmt"

	"github.com/Yeti47/polar-resolve/internal/model"
	"github.com/Yeti47/polar-resolve/internal/upscaler"
	"github.com/Yeti47/polar-resolve/internal/video"
	"github.com/spf13/cobra"
)

var (
	vidInput       string
	vidOutput      string
	vidCodec       string
	vidCRF         int
	vidTileSize    int
	vidTileOverlap int
)

var videoCmd = &cobra.Command{
	Use:   "video",
	Short: "Upscale a video 4×",
	Long:  `Upscale a video using Real-ESRGAN-general-x4vr. Frames are processed through ffmpeg.`,
	RunE:  runVideo,
}

func init() {
	videoCmd.Flags().StringVarP(&vidInput, "input", "i", "", "Input video file (required)")
	videoCmd.Flags().StringVarP(&vidOutput, "output", "o", "", "Output video file (default: input with _4x suffix)")
	videoCmd.Flags().StringVar(&vidCodec, "codec", "libx264", "Output video codec (libx264, libx265)")
	videoCmd.Flags().IntVar(&vidCRF, "crf", 18, "Constant rate factor (quality, lower=better)")
	videoCmd.Flags().IntVar(&vidTileSize, "tile-size", 128, "Tile size for inference (pixels)")
	videoCmd.Flags().IntVar(&vidTileOverlap, "tile-overlap", 16, "Overlap between tiles (pixels)")

	_ = videoCmd.MarkFlagRequired("input")

	rootCmd.AddCommand(videoCmd)
}

func runVideo(cmd *cobra.Command, args []string) error {
	// Resolve model
	modelPath := GetModelPath()
	var err error
	if modelPath == "" {
		Logf("No model specified, will auto-download...")
		modelPath, err = model.EnsureModel()
		if err != nil {
			return fmt.Errorf("failed to get model: %w", err)
		}
	}
	Logf("Using model: %s", modelPath)

	// Probe input video
	Logf("Probing %s...", vidInput)
	info, err := video.Probe(vidInput)
	if err != nil {
		return fmt.Errorf("failed to probe video: %w", err)
	}
	Logf("Video: %dx%d, %.2f fps, %d frames, codec=%s",
		info.Width, info.Height, info.FPS, info.FrameCount, info.CodecName)

	// Resolve output path
	outPath := vidOutput
	if outPath == "" {
		outPath = video.DefaultOutputPath(vidInput)
	}

	// Initialize upscaler
	u, err := upscaler.New(upscaler.Config{
		ModelPath:   modelPath,
		Device:      GetDevice(),
		LibPath:     GetLibPath(),
		TileSize:    vidTileSize,
		TileOverlap: vidTileOverlap,
		Verbose:     IsVerbose(),
	})
	if err != nil {
		return fmt.Errorf("failed to initialize upscaler: %w", err)
	}
	defer u.Close()

	// Process video
	err = video.Process(video.ProcessConfig{
		InputPath:  vidInput,
		OutputPath: outPath,
		Info:       info,
		Codec:      vidCodec,
		CRF:        vidCRF,
		Upscaler:   u,
		Verbose:    IsVerbose(),
	})
	if err != nil {
		return fmt.Errorf("video processing failed: %w", err)
	}

	fmt.Printf("Done: %s (%dx%d)\n", outPath, info.Width*4, info.Height*4)
	return nil
}
