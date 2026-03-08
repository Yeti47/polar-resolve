package video

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Yeti47/polar-resolve/internal/image"
)

// FrameUpscaler is the interface for upscaling a single frame.
type FrameUpscaler interface {
	UpscaleImage(img *image.RGBImage) (*image.RGBImage, error)
}

// VideoInfo holds metadata about a video file.
type VideoInfo struct {
	Width      int
	Height     int
	FPS        float64
	FrameCount int
	CodecName  string
	Duration   float64 // seconds
}

// ProcessConfig holds the configuration for video processing.
type ProcessConfig struct {
	InputPath  string
	OutputPath string
	Info       *VideoInfo
	Codec      string
	CRF        int
	NoAudio    bool
	Upscaler   FrameUpscaler
	Verbose    bool
}

// Probe retrieves video metadata using ffprobe.
func Probe(path string) (*VideoInfo, error) {
	cmd := exec.Command("ffprobe",
		"-v", "error",
		"-print_format", "json",
		"-show_streams",
		"-show_format",
		path,
	)

	var stderr strings.Builder
	cmd.Stderr = &stderr

	output, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return nil, fmt.Errorf("ffprobe failed: %w\n%s", err, msg)
		}
		return nil, fmt.Errorf("ffprobe failed: %w", err)
	}

	var result struct {
		Streams []struct {
			CodecType  string `json:"codec_type"`
			CodecName  string `json:"codec_name"`
			Width      int    `json:"width"`
			Height     int    `json:"height"`
			RFrameRate string `json:"r_frame_rate"`
			NbFrames   string `json:"nb_frames"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}

	if err := json.Unmarshal(output, &result); err != nil {
		return nil, fmt.Errorf("parse ffprobe output: %w", err)
	}

	info := &VideoInfo{}
	for _, s := range result.Streams {
		if s.CodecType == "video" {
			info.Width = s.Width
			info.Height = s.Height
			info.CodecName = s.CodecName
			info.FPS = parseFrameRate(s.RFrameRate)
			if s.NbFrames != "" {
				info.FrameCount, _ = strconv.Atoi(s.NbFrames)
			}
			break
		}
	}

	if info.Width == 0 || info.Height == 0 {
		return nil, fmt.Errorf("no video stream found in %s", path)
	}

	if result.Format.Duration != "" {
		info.Duration, _ = strconv.ParseFloat(result.Format.Duration, 64)
	}

	if info.FrameCount == 0 && info.Duration > 0 && info.FPS > 0 {
		info.FrameCount = int(math.Round(info.Duration * info.FPS))
	}

	return info, nil
}

// DefaultOutputPath returns the default output path for a video file (adds _4x suffix).
func DefaultOutputPath(inputPath string) string {
	ext := filepath.Ext(inputPath)
	base := strings.TrimSuffix(inputPath, ext)
	return base + "_4x" + ext
}

// Process upscales a video by extracting frames, upscaling each, and re-encoding.
func Process(cfg ProcessConfig) error {
	info := cfg.Info
	outW := info.Width * 4
	outH := info.Height * 4
	frameSize := info.Width * info.Height * 3
	outFrameSize := outW * outH * 3

	// Step 1: Extract audio to temp file (unless --no-audio is set)
	audioPath := cfg.OutputPath + ".audio.mka"
	hasAudio := false
	if !cfg.NoAudio {
		hasAudio = extractAudio(cfg.InputPath, audioPath)
		if hasAudio && cfg.Verbose {
			fmt.Println("[polar-resolve] Audio stream extracted")
		}
	}
	defer os.Remove(audioPath)

	// Step 2: Start ffmpeg decoder (input -> raw RGB frames via pipe)
	decodeCmd := exec.Command("ffmpeg",
		"-i", cfg.InputPath,
		"-f", "rawvideo",
		"-pix_fmt", "rgb24",
		"-v", "error",
		"pipe:1",
	)
	decodePipe, err := decodeCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("create decode pipe: %w", err)
	}
	decodeCmd.Stderr = os.Stderr
	if err := decodeCmd.Start(); err != nil {
		return fmt.Errorf("start decoder: %w", err)
	}

	// Step 3: Start ffmpeg encoder (raw RGB frames via pipe -> output file)
	encodeArgs := []string{
		"-f", "rawvideo",
		"-pix_fmt", "rgb24",
		"-s", fmt.Sprintf("%dx%d", outW, outH),
		"-r", fmt.Sprintf("%.6f", info.FPS),
		"-i", "pipe:0",
	}
	if hasAudio {
		encodeArgs = append(encodeArgs, "-i", audioPath, "-c:a", "copy")
	}
	encodeArgs = append(encodeArgs,
		"-c:v", cfg.Codec,
		"-crf", strconv.Itoa(cfg.CRF),
		"-pix_fmt", "yuv420p",
		"-v", "error",
		"-y",
		cfg.OutputPath,
	)

	encodeCmd := exec.Command("ffmpeg", encodeArgs...)
	encodePipe, err := encodeCmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("create encode pipe: %w", err)
	}
	encodeCmd.Stderr = os.Stderr
	if err := encodeCmd.Start(); err != nil {
		return fmt.Errorf("start encoder: %w", err)
	}

	// Step 4: Process frames
	frameBuf := make([]byte, frameSize)
	frameNum := 0
	startTime := time.Now()

	for {
		_, err := io.ReadFull(decodePipe, frameBuf)
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return fmt.Errorf("read frame %d: %w", frameNum, err)
		}
		frameNum++

		frameImg := image.FromRawRGB(frameBuf, info.Width, info.Height)

		upscaled, err := cfg.Upscaler.UpscaleImage(frameImg)
		if err != nil {
			return fmt.Errorf("upscale frame %d: %w", frameNum, err)
		}

		outData := upscaled.ToRawRGB()
		if len(outData) != outFrameSize {
			return fmt.Errorf("frame %d: expected %d bytes, got %d", frameNum, outFrameSize, len(outData))
		}
		if _, err := encodePipe.Write(outData); err != nil {
			return fmt.Errorf("write frame %d: %w", frameNum, err)
		}

		printProgress(frameNum, info.FrameCount, startTime)
	}

	encodePipe.Close()
	fmt.Println()

	if err := decodeCmd.Wait(); err != nil {
		return fmt.Errorf("decoder error: %w", err)
	}
	if err := encodeCmd.Wait(); err != nil {
		return fmt.Errorf("encoder error: %w", err)
	}

	return nil
}

// extractAudio extracts the audio stream from a video file.
func extractAudio(inputPath, outputPath string) bool {
	cmd := exec.Command("ffmpeg",
		"-i", inputPath,
		"-vn",
		"-c:a", "copy",
		"-v", "error",
		"-y",
		outputPath,
	)
	err := cmd.Run()
	if err != nil {
		return false
	}
	info, err := os.Stat(outputPath)
	return err == nil && info.Size() > 0
}

// printProgress displays progress information.
func printProgress(current, total int, startTime time.Time) {
	elapsed := time.Since(startTime).Seconds()
	fps := float64(current) / elapsed

	if total > 0 {
		pct := float64(current) / float64(total) * 100
		eta := 0.0
		if fps > 0 {
			eta = float64(total-current) / fps
		}
		fmt.Printf("\r  Frame %d/%d (%.1f%%) | %.2f fps | ETA: %s",
			current, total, pct, fps, formatDuration(eta))
	} else {
		fmt.Printf("\r  Frame %d | %.2f fps | Elapsed: %s",
			current, fps, formatDuration(elapsed))
	}
}

// parseFrameRate parses a fractional frame rate string like "30000/1001".
func parseFrameRate(s string) float64 {
	parts := strings.Split(s, "/")
	if len(parts) != 2 {
		f, _ := strconv.ParseFloat(s, 64)
		return f
	}
	num, _ := strconv.ParseFloat(parts[0], 64)
	den, _ := strconv.ParseFloat(parts[1], 64)
	if den == 0 {
		return 0
	}
	return num / den
}

// formatDuration formats seconds into a human-readable duration string.
func formatDuration(seconds float64) string {
	if seconds < 0 {
		return "N/A"
	}
	d := time.Duration(seconds * float64(time.Second))
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%02dm%02ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}
