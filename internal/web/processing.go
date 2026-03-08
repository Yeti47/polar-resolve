package web

import (
	"fmt"

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
	if j.fileType == "image" {
		err = s.processImageJob(j)
	} else {
		err = s.processVideoJob(j)
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
	j.broadcast(jobEvent{Status: statusProcessing, Progress: 0, Detail: "Probing video..."})

	info, err := video.Probe(j.inputPath)
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

	err = video.Process(video.ProcessConfig{
		InputPath:  j.inputPath,
		OutputPath: j.outputPath,
		Info:       info,
		Codec:      j.codec,
		CRF:        j.crf,
		NoAudio:    j.noAudio,
		Upscaler:   wrapped,
		Logger:     logging.Nop(),
		Tracker:    &jobTracker{job: j, label: "Frame"},
	})

	return err
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
