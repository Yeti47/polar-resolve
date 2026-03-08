package model

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/Yeti47/polar-resolve/internal/logging"
)

const (
	// DefaultModelURL is the Qualcomm AI Hub ONNX float export of Real-ESRGAN-General-x4v3.
	// Source: https://huggingface.co/qualcomm/Real-ESRGAN-General-x4v3
	DefaultModelURL = "https://qaihub-public-assets.s3.us-west-2.amazonaws.com/qai-hub-models/models/real_esrgan_general_x4v3/releases/v0.46.0/real_esrgan_general_x4v3-onnx-float.zip"

	// DefaultModelFilename is the ONNX model file inside the extracted archive.
	DefaultModelFilename = "real_esrgan_general_x4v3.onnx"

	// DefaultModelDir is the subdirectory name inside the cache for this model's files.
	DefaultModelDir = "real_esrgan_general_x4v3"

	// ModelZipSHA256 is the expected SHA256 hash of the downloaded zip archive.
	ModelZipSHA256 = "391da19c39c9ffec2ae094a3dacf25f893e337fccd4925db8771922e961287a7"
)

// DownloadPhase represents a stage in the model preparation process.
type DownloadPhase string

const (
	PhaseDownloading DownloadPhase = "downloading"
	PhaseVerifying   DownloadPhase = "verifying"
	PhaseExtracting  DownloadPhase = "extracting"
	PhaseReady       DownloadPhase = "ready"
)

// DownloadObserver receives model preparation progress updates.
type DownloadObserver interface {
	// OnDownloadProgress is called when the download phase or byte counts change.
	// current and total are byte counts during PhaseDownloading; for other phases they are 0.
	OnDownloadProgress(phase DownloadPhase, current, total int64)
}

// CacheDir returns the model cache directory (~/.cache/polar-resolve/models/).
// Inside a container, POLAR_RESOLVE_MODEL_DIR overrides the location.
func CacheDir() (string, error) {
	if dir := os.Getenv("POLAR_RESOLVE_MODEL_DIR"); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("cannot create model directory %s: %w", dir, err)
		}
		return dir, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	dir := filepath.Join(home, ".cache", "polar-resolve", "models")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("cannot create cache directory %s: %w", dir, err)
	}
	return dir, nil
}

// DefaultModelPath returns the expected path of the cached ONNX model file.
func DefaultModelPath() (string, error) {
	dir, err := CacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, DefaultModelDir, DefaultModelFilename), nil
}

// EnsureModel checks if the model exists in the cache, downloads and extracts
// it if not, and returns the path to the .onnx file.
//
// The model archive contains both the .onnx graph and an external .data file
// with the weights. Both must be in the same directory for ONNX Runtime to
// load the model.
func EnsureModel(log logging.Logger) (string, error) {
	return EnsureModelWithProgress(log, nil)
}

// EnsureModelWithProgress is like EnsureModel but reports download progress
// through the optional observer.
func EnsureModelWithProgress(log logging.Logger, observer DownloadObserver) (string, error) {
	if log == nil {
		log = logging.Nop()
	}

	modelPath, err := DefaultModelPath()
	if err != nil {
		return "", err
	}

	// Check if the model is already extracted
	if info, err := os.Stat(modelPath); err == nil && info.Size() > 0 {
		return modelPath, nil
	}

	cacheDir, err := CacheDir()
	if err != nil {
		return "", err
	}

	// Download the zip archive
	zipPath := filepath.Join(cacheDir, "real_esrgan_general_x4v3-onnx-float.zip")
	log.Infof("Downloading model to %s...", zipPath)
	if err := downloadFile(log, zipPath, DefaultModelURL, observer); err != nil {
		return "", fmt.Errorf("failed to download model: %w", err)
	}

	// Verify the zip checksum
	if ModelZipSHA256 != "" {
		if observer != nil {
			observer.OnDownloadProgress(PhaseVerifying, 0, 0)
		}
		ok, err := verifyChecksum(zipPath, ModelZipSHA256)
		if err != nil {
			_ = os.Remove(zipPath)
			return "", fmt.Errorf("checksum verification failed: %w", err)
		}
		if !ok {
			_ = os.Remove(zipPath)
			return "", fmt.Errorf("downloaded archive checksum mismatch")
		}
	}

	// Extract the archive
	destDir := filepath.Join(cacheDir, DefaultModelDir)
	log.Infof("Extracting model to %s...", destDir)
	if observer != nil {
		observer.OnDownloadProgress(PhaseExtracting, 0, 0)
	}
	if err := extractZip(zipPath, destDir); err != nil {
		_ = os.RemoveAll(destDir)
		return "", fmt.Errorf("failed to extract model archive: %w", err)
	}

	// Remove the zip to save space
	_ = os.Remove(zipPath)

	// Verify the extracted ONNX file exists
	if _, err := os.Stat(modelPath); err != nil {
		return "", fmt.Errorf("model file not found after extraction: %s", modelPath)
	}

	if observer != nil {
		observer.OnDownloadProgress(PhaseReady, 0, 0)
	}

	return modelPath, nil
}

// extractZip extracts a zip archive, flattening the top-level directory into destDir.
// The Qualcomm zip archives have a single top-level directory; we strip it so
// that destDir directly contains the model files.
func extractZip(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}

	// Detect the common top-level prefix to strip
	prefix := ""
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			// Use the first directory entry as the prefix to strip
			prefix = f.Name
			break
		}
	}

	for _, f := range r.File {
		name := f.Name
		// Strip the top-level directory prefix
		if prefix != "" {
			name = strings.TrimPrefix(name, prefix)
		}
		if name == "" || name == "/" {
			continue
		}

		destPath := filepath.Join(destDir, name)

		// Prevent zip slip
		if !strings.HasPrefix(destPath, filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("illegal file path in archive: %s", f.Name)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(destPath, 0o755); err != nil {
				return err
			}
			continue
		}

		if err := extractZipFile(f, destPath); err != nil {
			return err
		}
	}

	return nil
}

func extractZipFile(f *zip.File, destPath string) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}

	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	out, err := os.Create(destPath)
	if err != nil {
		return err
	}

	_, err = io.Copy(out, rc)
	if closeErr := out.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	return err
}

// downloadFile downloads a file from url and saves it to dest.
func downloadFile(log logging.Logger, dest, url string, observer DownloadObserver) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}

	tmpPath := dest + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return err
	}

	total := resp.ContentLength // -1 if unknown
	var written int64
	var w io.Writer = f

	if observer != nil {
		observer.OnDownloadProgress(PhaseDownloading, 0, total)
		w = &progressWriter{w: f, total: total, observer: observer}
	}

	written, err = io.Copy(w, resp.Body)
	if closeErr := f.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(tmpPath)
		return err
	}

	log.Infof("Downloaded %d bytes", written)
	return os.Rename(tmpPath, dest)
}

// progressWriter wraps an io.Writer and reports download progress.
type progressWriter struct {
	w        io.Writer
	written  int64
	total    int64
	observer DownloadObserver
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n, err := pw.w.Write(p)
	pw.written += int64(n)
	pw.observer.OnDownloadProgress(PhaseDownloading, pw.written, pw.total)
	return n, err
}

// verifyChecksum checks the SHA256 hash of a file.
func verifyChecksum(path, expected string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false, err
	}

	actual := hex.EncodeToString(h.Sum(nil))
	return actual == expected, nil
}
