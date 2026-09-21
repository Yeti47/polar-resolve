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
	vidNoAudio     bool
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
	videoCmd.Flags().BoolVar(&vidNoAudio, "no-audio", false, "Remove audio track from the output video")

	_ = videoCmd.MarkFlagRequired("input")

	rootCmd.AddCommand(videoCmd)
}

// videoOptions groups the upscaling parameters shared by the `video` command
// and the `download --upscale` flow.
type videoOptions struct {
	Codec       string
	CRF         int
	TileSize    int
	TileOverlap int
	NoAudio     bool
}

func runVideo(cmd *cobra.Command, args []string) error {
	// Resolve relative paths to workspace directories
	vidInput = ResolveInputPath(vidInput)
	vidOutput = ResolveOutputPath(vidOutput)

	// Resolve output path
	outPath := vidOutput
	if outPath == "" {
		outPath = video.DefaultOutputPath(vidInput)
	}

	return upscaleVideoFile(vidInput, outPath, videoOptions{
		Codec:       vidCodec,
		CRF:         vidCRF,
		TileSize:    vidTileSize,
		TileOverlap: vidTileOverlap,
		NoAudio:     vidNoAudio,
	})
}

// upscaleVideoFile runs the Real-ESRGAN 4× upscaler over a single video file.
func upscaleVideoFile(inputPath, outPath string, opts videoOptions) error {
	// Resolve model
	modelPath := GetModelPath()
	var err error
	log := NewLogger()
	if modelPath == "" {
		Logf("No model specified, will auto-download...")
		modelPath, err = model.EnsureModel(log)
		if err != nil {
			return fmt.Errorf("failed to get model: %w", err)
		}
	}
	Logf("Using model: %s", modelPath)

	// Probe input video
	Logf("Probing %s...", inputPath)
	info, err := video.Probe(inputPath)
	if err != nil {
		return fmt.Errorf("failed to probe video: %w", err)
	}
	Logf("Video: %dx%d, %.2f fps, %d frames, codec=%s",
		info.Width, info.Height, info.FPS, info.FrameCount, info.CodecName)

	// Initialize upscaler
	u, err := upscaler.New(upscaler.Config{
		ModelPath:   modelPath,
		Device:      GetDevice(),
		LibPath:     GetLibPath(),
		TileSize:    opts.TileSize,
		TileOverlap: opts.TileOverlap,
		Logger:      log,
	})
	if err != nil {
		return fmt.Errorf("failed to initialize upscaler: %w", err)
	}
	defer u.Close()

	// Process video
	err = video.Process(video.ProcessConfig{
		InputPath:  inputPath,
		OutputPath: outPath,
		Info:       info,
		Codec:      opts.Codec,
		CRF:        opts.CRF,
		NoAudio:    opts.NoAudio,
		Upscaler:   u,
		Logger:     log,
	})
	if err != nil {
		return fmt.Errorf("video processing failed: %w", err)
	}

	fmt.Printf("Done: %s (%dx%d)\n", outPath, info.Width*4, info.Height*4)
	return nil
}
