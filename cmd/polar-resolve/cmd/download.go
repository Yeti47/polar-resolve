package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Yeti47/polar-resolve/internal/download"
	"github.com/Yeti47/polar-resolve/internal/video"
	"github.com/spf13/cobra"
)

var (
	dlURLs        []string
	dlOutput      string
	dlQuality     string
	dlAudioOnly   bool
	dlCookies     string
	dlUpscale     bool
	dlCodec       string
	dlCRF         int
	dlTileSize    int
	dlTileOverlap int
	dlNoAudio     bool
)

var downloadCmd = &cobra.Command{
	Use:   "download [url...]",
	Short: "Download videos from YouTube, Twitter/X, and other sites",
	Long: `Download videos with yt-dlp (https://github.com/yt-dlp/yt-dlp).

YouTube, Twitter/X, and hundreds of other sites are supported. Relative
output paths are resolved under /workspace/output. Pass --upscale to run the
downloaded video through the 4× Real-ESRGAN upscaler in one step.`,
	Args: cobra.ArbitraryArgs,
	RunE: runDownload,
}

func init() {
	downloadCmd.Flags().StringArrayVarP(&dlURLs, "url", "u", nil, "Video URL to download (repeatable)")
	downloadCmd.Flags().StringVarP(&dlOutput, "output", "o", "", "Output file or directory (default: /workspace/output)")
	downloadCmd.Flags().StringVarP(&dlQuality, "quality", "q", "best", "Max quality: best, 1080p, 720p, 480p, 360p")
	downloadCmd.Flags().BoolVar(&dlAudioOnly, "audio-only", false, "Extract audio only (MP3) instead of video")
	downloadCmd.Flags().StringVar(&dlCookies, "cookies-from-browser", "", "Load cookies from a browser (chrome, firefox, edge, ...) for private/age-restricted videos")
	downloadCmd.Flags().BoolVar(&dlUpscale, "upscale", false, "4× upscale the downloaded video after downloading")

	downloadCmd.Flags().StringVar(&dlCodec, "codec", "libx264", "Output codec when --upscale is set (libx264, libx265)")
	downloadCmd.Flags().IntVar(&dlCRF, "crf", 18, "Constant rate factor when --upscale is set (lower=better)")
	downloadCmd.Flags().IntVar(&dlTileSize, "tile-size", 128, "Tile size for inference when --upscale is set (pixels)")
	downloadCmd.Flags().IntVar(&dlTileOverlap, "tile-overlap", 16, "Overlap between tiles when --upscale is set (pixels)")
	downloadCmd.Flags().BoolVar(&dlNoAudio, "no-audio", false, "Remove audio track when --upscale is set")

	rootCmd.AddCommand(downloadCmd)
}

func runDownload(cmd *cobra.Command, args []string) error {
	urls := make([]string, 0, len(dlURLs)+len(args))
	for _, u := range append(append([]string{}, dlURLs...), args...) {
		if u = strings.TrimSpace(u); u != "" {
			urls = append(urls, u)
		}
	}
	if len(urls) == 0 {
		return fmt.Errorf("at least one URL is required (use --url or pass URLs as arguments)")
	}

	if !download.Available() {
		_, err := download.BinaryPath()
		return err
	}

	dir, file, err := resolveDownloadOutput(dlOutput, len(urls) == 1)
	if err != nil {
		return err
	}

	opts := videoOptions{
		Codec:       dlCodec,
		CRF:         dlCRF,
		TileSize:    dlTileSize,
		TileOverlap: dlTileOverlap,
		NoAudio:     dlNoAudio,
	}

	for i, rawURL := range urls {
		platform := download.DetectPlatform(rawURL)
		if len(urls) > 1 {
			fmt.Printf("[%d/%d] %s (%s)\n", i+1, len(urls), rawURL, platform)
		} else {
			fmt.Printf("Downloading %s (%s)...\n", rawURL, platform)
		}

		obs := &consoleDownloadObserver{}
		res, err := download.Download(download.Config{
			URL:                rawURL,
			OutputDir:          dir,
			Quality:            dlQuality,
			AudioOnly:          dlAudioOnly,
			CookiesFromBrowser: dlCookies,
			Logger:             NewLogger(),
			Observer:           obs,
		})
		obs.finish()
		if err != nil {
			return fmt.Errorf("download failed: %w", err)
		}

		finalPath := res.Path
		if file != "" {
			finalPath = file
		} else {
			ext := filepath.Ext(res.Path)
			finalPath = filepath.Join(dir, download.SanitizeFilename(res.Metadata.Title)+ext)
			if _, statErr := os.Stat(finalPath); statErr == nil && finalPath != res.Path {
				// Avoid clobbering an existing file: disambiguate with the video id.
				finalPath = filepath.Join(dir, download.SanitizeFilename(res.Metadata.Title)+" ["+res.Metadata.ID+"]"+ext)
			}
		}
		if finalPath != res.Path {
			if err := moveFile(res.Path, finalPath); err != nil {
				return fmt.Errorf("move downloaded file: %w", err)
			}
		}
		fmt.Printf("  -> %s\n", finalPath)

		if dlAudioOnly {
			if dlUpscale {
				Logf("--upscale ignored for audio-only downloads")
			}
			continue
		}

		if dlUpscale {
			upscaled := video.DefaultOutputPath(finalPath)
			fmt.Printf("Upscaling %s -> %s...\n", finalPath, upscaled)
			if err := upscaleVideoFile(finalPath, upscaled, opts); err != nil {
				return err
			}
		}
	}

	return nil
}

// resolveDownloadOutput interprets the --output flag. It returns the directory
// the download is fetched into and, for a single download targeting an explicit
// file, the final file path (empty when the output is a directory).
func resolveDownloadOutput(outputFlag string, single bool) (dir, file string, err error) {
	if outputFlag == "" {
		outputFlag = "."
	}
	outputFlag = ResolveOutputPath(outputFlag)

	if info, statErr := os.Stat(outputFlag); statErr == nil && info.IsDir() {
		return outputFlag, "", nil
	}

	if single && filepath.Ext(outputFlag) != "" {
		if err := os.MkdirAll(filepath.Dir(outputFlag), 0o755); err != nil {
			return "", "", fmt.Errorf("create output directory: %w", err)
		}
		return filepath.Dir(outputFlag), outputFlag, nil
	}

	if err := os.MkdirAll(outputFlag, 0o755); err != nil {
		return "", "", fmt.Errorf("create output directory: %w", err)
	}
	return outputFlag, "", nil
}

// moveFile renames src to dst, falling back to a copy when the two paths are on
// different filesystems.
func moveFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dst)
		return err
	}
	return os.Remove(src)
}

// consoleDownloadObserver renders yt-dlp progress on a single terminal line.
type consoleDownloadObserver struct {
	lastLen int
	open    bool
}

func (o *consoleDownloadObserver) OnDownloadProgress(_ int, detail string) {
	line := "  Downloading " + detail
	pad := ""
	if n := o.lastLen - len(line); n > 0 {
		pad = strings.Repeat(" ", n)
	}
	fmt.Printf("\r%s%s", line, pad)
	o.lastLen = len(line)
	o.open = true
}

func (o *consoleDownloadObserver) finish() {
	if o.open {
		fmt.Println()
	}
}
