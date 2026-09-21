package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Yeti47/polar-resolve/internal/download"
	"github.com/Yeti47/polar-resolve/internal/logging"
	"github.com/gin-gonic/gin"
)

func newTestServer(t *testing.T, ready bool) *Server {
	t.Helper()
	tmp := t.TempDir()
	s := &Server{
		config: ServerConfig{Logger: logging.Nop()},
		log:    logging.Nop(),
		tmpDir: tmp,
		queue:  make(chan *job, 10),
		ready:  make(chan struct{}),
		initSt: &initStatus{phase: phaseInitializing},
	}
	if ready {
		close(s.ready)
	}
	return s
}

func multipartBody(t *testing.T, fields map[string]string) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return body, w.FormDataContentType()
}

func TestCreateDownloadJob(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServer(t, true)

	body, contentType := multipartBody(t, map[string]string{
		"url":     "https://www.youtube.com/watch?v=abc123",
		"quality": "720p",
		"upscale": "true",
		"codec":   "libx265",
		"crf":     "20",
	})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/jobs", body)
	c.Request.Header.Set("Content-Type", contentType)

	s.handleCreateJob(c)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Type != "download" {
		t.Errorf("type = %q, want download", resp.Type)
	}

	val, ok := s.jobs.Load(resp.ID)
	if !ok {
		t.Fatal("job not stored")
	}
	j := val.(*job)
	if j.sourceURL != "https://www.youtube.com/watch?v=abc123" {
		t.Errorf("sourceURL = %q", j.sourceURL)
	}
	if j.quality != "720p" || !j.upscale || j.codec != "libx265" || j.crf != 20 {
		t.Errorf("options not parsed: %+v", j)
	}
	if j.jobDir == "" {
		t.Error("jobDir not set")
	}
	if len(s.queue) != 1 {
		t.Errorf("queue length = %d, want 1", len(s.queue))
	}
}

func TestCreateDownloadJobRejectsBadURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServer(t, true)

	for _, bad := range []string{"ftp://example.com/x", "not a url", "javascript:alert(1)"} {
		body, contentType := multipartBody(t, map[string]string{"url": bad})
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/jobs", body)
		c.Request.Header.Set("Content-Type", contentType)

		s.handleCreateJob(c)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("url %q: status = %d, want 400", bad, rec.Code)
		}
	}
}

func TestCreateJobRequiresReady(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServer(t, false)

	body, contentType := multipartBody(t, map[string]string{"url": "https://youtu.be/abc"})
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/jobs", body)
	c.Request.Header.Set("Content-Type", contentType)

	s.handleCreateJob(c)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

// TestProcessDownloadJob exercises the full download job path against a real
// URL. It is skipped unless POLAR_RESOLVE_TEST_URL is set, so the default test
// run stays offline.
func TestProcessDownloadJob(t *testing.T) {
	testURL := os.Getenv("POLAR_RESOLVE_TEST_URL")
	if testURL == "" {
		t.Skip("set POLAR_RESOLVE_TEST_URL to run")
	}
	if !downloadAvailable() {
		t.Skip("yt-dlp not available")
	}

	s := newTestServer(t, true)
	j := newJob()
	j.fileType = "download"
	j.jobDir = filepath.Join(s.tmpDir, j.id)
	if err := os.MkdirAll(j.jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	j.sourceURL = testURL
	j.quality = "360p"

	if err := s.processDownloadJob(j); err != nil {
		t.Fatalf("processDownloadJob: %v", err)
	}

	if j.outputPath == "" {
		t.Fatal("outputPath not set")
	}
	info, err := os.Stat(j.outputPath)
	if err != nil {
		t.Fatalf("output missing: %v", err)
	}
	if info.Size() == 0 {
		t.Error("output file is empty")
	}
	if !strings.HasSuffix(j.outputName, filepath.Ext(j.outputPath)) {
		t.Errorf("outputName %q does not match outputPath %q", j.outputName, j.outputPath)
	}
	fmt.Fprintf(io.Discard, "")
}

// downloadAvailable reports whether yt-dlp can be located.
func downloadAvailable() bool {
	return download.Available()
}
