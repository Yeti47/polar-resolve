package web

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Yeti47/polar-resolve/internal/download"
	"github.com/Yeti47/polar-resolve/internal/image"
	"github.com/Yeti47/polar-resolve/internal/logging"
	"github.com/Yeti47/polar-resolve/internal/upscaler"
	"github.com/Yeti47/polar-resolve/internal/video"
)

func (s *Server) processQueue() {
	// Wait until the upscaler is ready before processing any jobs.
	<-s.ready
	for j := range s.queue {
		s.processJob(j)
	}
}

func (s *Server) processJob(j *job) {
	j.broadcast(jobEvent{Status: statusProcessing, Progress: 0, Detail: "Starting..."})

	var err error
	switch j.fileType {
	case "image":
		err = s.processImageJob(j)
	case "video":
		err = s.processVideoJob(j)
	case "download":
		err = s.processDownloadJob(j)
	default:
		err = fmt.Errorf("unknown job type %q", j.fileType)
	}

	if err != nil {
		j.broadcast(jobEvent{Status: statusFailed, Error: err.Error()})
		return
	}

	j.broadcast(jobEvent{Status: statusCompleted, Progress: 100, Detail: "Done"})
}

func (s *Server) processImageJob(j *job) error {
	j.broadcast(jobEvent{Status: statusProcessing, Progress: 0, Detail: "Loading image..."})

	img, err := image.Load(j.inputPath)
	if err != nil {
		return fmt.Errorf("load image: %w", err)
	}

	j.broadcast(jobEvent{
		Status:   statusProcessing,
		Progress: 0,
		Detail:   fmt.Sprintf("Upscaling %dx%d -> %dx%d...", img.Width, img.Height, img.Width*4, img.Height*4),
	})

	tc := &upscaler.TileConfig{
		TileSize: j.tileSize,
		Overlap:  j.tileOverlap,
	}

	tracker := &jobTracker{job: j, label: "Tile"}

	result, err := s.upscaler.UpscaleImageWithProgress(img, tc, tracker)
	if err != nil {
		return fmt.Errorf("upscale: %w", err)
	}

	j.broadcast(jobEvent{Status: statusProcessing, Progress: 99, Detail: "Saving..."})

	if err := image.Save(result, j.outputPath); err != nil {
		return fmt.Errorf("save: %w", err)
	}

	return nil
}

func (s *Server) processVideoJob(j *job) error {
	return s.upscaleVideoTo(j, j.inputPath, j.outputPath, j.outputName)
}

// processDownloadJob fetches a remote video (YouTube, Twitter/X, ...) and then
// either stores it as-is, extracts its audio, or upscales it 4×.
func (s *Server) processDownloadJob(j *job) error {
	j.broadcast(jobEvent{
		Status:   statusProcessing,
		Progress: 0,
		Detail:   fmt.Sprintf("Downloading from %s...", download.DetectPlatform(j.sourceURL)),
	})

	res, err := download.Download(download.Config{
		URL:                j.sourceURL,
		OutputDir:          j.jobDir,
		Quality:            j.quality,
		AudioOnly:          j.audioOnly,
		CookiesFromBrowser: j.cookies,
		Logger:             logging.Nop(),
		Observer:           &downloadJobObserver{job: j},
	})
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}

	base := download.SanitizeFilename(res.Metadata.Title)
	ext := filepath.Ext(res.Path)

	if j.audioOnly {
		j.broadcast(jobEvent{Status: statusProcessing, Progress: 99, Detail: "Saving..."})
		final := filepath.Join(j.jobDir, "output"+ext)
		if err := moveFile(res.Path, final); err != nil {
			return fmt.Errorf("save: %w", err)
		}
		j.setOutput(final, base+ext)
		return nil
	}

	if j.upscale {
		return s.upscaleVideoTo(j, res.Path, filepath.Join(j.jobDir, "output.mp4"), base+"_4x.mp4")
	}

	j.broadcast(jobEvent{Status: statusProcessing, Progress: 99, Detail: "Saving..."})
	final := filepath.Join(j.jobDir, "output"+ext)
	if err := moveFile(res.Path, final); err != nil {
		return fmt.Errorf("save: %w", err)
	}
	j.setOutput(final, base+ext)
	return nil
}

// upscaleVideoTo probes and 4× upscales a single video file using the shared
// server upscaler, recording the output on the job.
func (s *Server) upscaleVideoTo(j *job, inputPath, outputPath, outputName string) error {
	j.broadcast(jobEvent{Status: statusProcessing, Progress: 0, Detail: "Probing video..."})

	info, err := video.Probe(inputPath)
	if err != nil {
		return fmt.Errorf("probe: %w", err)
	}

	j.broadcast(jobEvent{
		Status:   statusProcessing,
		Progress: 0,
		Detail:   fmt.Sprintf("Processing %dx%d video (%d frames)...", info.Width, info.Height, info.FrameCount),
	})

	wrapped := &tileConfigUpscaler{
		inner: s.upscaler,
		tc: upscaler.TileConfig{
			TileSize: j.tileSize,
			Overlap:  j.tileOverlap,
		},
	}

	if err := video.Process(video.ProcessConfig{
		InputPath:  inputPath,
		OutputPath: outputPath,
		Info:       info,
		Codec:      j.codec,
		CRF:        j.crf,
		NoAudio:    j.noAudio,
		Upscaler:   wrapped,
		Logger:     logging.Nop(),
		Tracker:    &jobTracker{job: j, label: "Frame"},
	}); err != nil {
		return err
	}

	j.setOutput(outputPath, outputName)
	return nil
}

// downloadJobObserver forwards yt-dlp progress to a job's subscribers.
type downloadJobObserver struct {
	job *job
}

func (o *downloadJobObserver) OnDownloadProgress(percent int, detail string) {
	if percent < 0 {
		percent = 0
	}
	if percent > 99 {
		percent = 99
	}
	o.job.broadcast(jobEvent{
		Status:   statusProcessing,
		Progress: percent,
		Detail:   "Downloading: " + detail,
	})
}

// tileConfigUpscaler wraps an Upscaler with a per-call TileConfig so it
// satisfies the video.FrameUpscaler interface.
type tileConfigUpscaler struct {
	inner *upscaler.Upscaler
	tc    upscaler.TileConfig
}

func (w *tileConfigUpscaler) UpscaleImage(img *image.RGBImage) (*image.RGBImage, error) {
	return w.inner.UpscaleImageWithProgress(img, &w.tc, nil)
}

// moveFile renames src to dst, falling back to a copy across filesystems.
func moveFile(src, dst string) error {
	if src == dst {
		return nil
	}
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
	if _, err := out.ReadFrom(in); err != nil {
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
