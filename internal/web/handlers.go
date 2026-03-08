package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
)

func (s *Server) handleIndex(c *gin.Context) {
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(indexHTML))
}

func (s *Server) handleStatus(c *gin.Context) {
	ch := s.initSt.subscribe()

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	// Send current state immediately
	sendInitStatus := func() {
		phase, cur, tot, errMsg := s.initSt.get()
		evt := map[string]interface{}{
			"phase": string(phase),
		}
		if phase == phaseDownloading {
			evt["current"] = cur
			evt["total"] = tot
		}
		if errMsg != "" {
			evt["error"] = errMsg
		}
		data, _ := json.Marshal(evt)
		c.SSEvent("status", string(data))
	}

	c.Stream(func(w io.Writer) bool {
		sendInitStatus()
		phase, _, _, _ := s.initSt.get()
		if phase == phaseReady || phase == phaseError {
			return false
		}
		// Wait for next update
		select {
		case _, ok := <-ch:
			if !ok {
				sendInitStatus()
				return false
			}
			return true
		case <-c.Request.Context().Done():
			s.initSt.unsubscribe(ch)
			return false
		}
	})
}

func (s *Server) handleCreateJob(c *gin.Context) {
	// Reject if not ready
	select {
	case <-s.ready:
	default:
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Server is still initializing. Please wait."})
		return
	}
	header, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no file uploaded"})
		return
	}

	ext := strings.ToLower(filepath.Ext(header.Filename))
	var fileType string
	switch ext {
	case ".png", ".jpg", ".jpeg", ".webp":
		fileType = "image"
	case ".mp4", ".mkv", ".avi", ".mov", ".webm":
		fileType = "video"
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported file type"})
		return
	}

	j := newJob()
	jobDir := filepath.Join(s.tmpDir, j.id)
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	inputPath := filepath.Join(jobDir, "input"+ext)
	if err := c.SaveUploadedFile(header, inputPath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save file"})
		return
	}

	baseName := filepath.Base(header.Filename)

	j.fileType = fileType
	j.inputPath = inputPath
	j.tileSize = intFormValue(c, "tileSize", 128)
	j.tileOverlap = intFormValue(c, "tileOverlap", 16)

	if fileType == "image" {
		j.format = c.PostForm("format")
		outExt := ext
		if j.format != "" {
			outExt = "." + j.format
		}
		j.outputPath = filepath.Join(jobDir, "output"+outExt)
		j.outputName = strings.TrimSuffix(baseName, ext) + "_4x" + outExt
	} else {
		j.codec = c.DefaultPostForm("codec", "libx264")
		j.crf = intFormValue(c, "crf", 18)
		j.noAudio = c.PostForm("noAudio") == "true"
		j.outputPath = filepath.Join(jobDir, "output.mp4")
		j.outputName = strings.TrimSuffix(baseName, ext) + "_4x.mp4"
	}

	s.jobs.Store(j.id, j)
	s.queue <- j

	c.JSON(http.StatusAccepted, gin.H{"id": j.id, "type": fileType})
}

func (s *Server) handleJobEvents(c *gin.Context) {
	id := c.Param("id")
	val, ok := s.jobs.Load(id)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "job not found"})
		return
	}

	j := val.(*job)
	ch := j.subscribe()

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	c.Stream(func(w io.Writer) bool {
		select {
		case evt, ok := <-ch:
			if !ok {
				return false
			}
			data, _ := json.Marshal(evt)
			c.SSEvent("progress", string(data))
			return evt.Status != statusCompleted && evt.Status != statusFailed
		case <-c.Request.Context().Done():
			j.unsubscribe(ch)
			return false
		}
	})
}

func (s *Server) handleDownload(c *gin.Context) {
	id := c.Param("id")
	val, ok := s.jobs.Load(id)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "job not found"})
		return
	}

	j := val.(*job)
	j.mu.Lock()
	status := j.status
	outputPath := j.outputPath
	outputName := j.outputName
	j.mu.Unlock()

	if status != statusCompleted {
		c.JSON(http.StatusBadRequest, gin.H{"error": "job not completed"})
		return
	}

	c.FileAttachment(outputPath, outputName)
}

func intFormValue(c *gin.Context, key string, defaultVal int) int {
	s := c.PostForm(key)
	if s == "" {
		return defaultVal
	}
	var v int
	if _, err := fmt.Sscanf(s, "%d", &v); err != nil {
		return defaultVal
	}
	return v
}
