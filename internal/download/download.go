// Package download fetches videos from YouTube, Twitter/X, and other sites
// supported by yt-dlp. yt-dlp and ffmpeg must be available on PATH (or
// POLAR_RESOLVE_YTDLP may point at a specific yt-dlp binary).
package download

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Yeti47/polar-resolve/internal/logging"
)

// Platform identifies a supported video source.
type Platform string

const (
	PlatformYouTube Platform = "youtube"
	PlatformTwitter Platform = "twitter"
	PlatformOther   Platform = "other"
)

// BinaryEnv is the environment variable used to override the yt-dlp location.
const BinaryEnv = "POLAR_RESOLVE_YTDLP"

// Metadata describes a remote video.
type Metadata struct {
	ID       string
	Title    string
	Duration float64
	Ext      string
	Width    int
	Height   int
}

// Observer receives download progress updates. percent is 0-100 and may be 0
// when the total size is unknown; detail is a human-readable status string.
type Observer interface {
	OnDownloadProgress(percent int, detail string)
}

// Config holds the parameters for a single download.
type Config struct {
	URL                string
	OutputDir          string // directory the file is fetched into (required)
	Quality            string // best, 1080p, 720p, 480p, 360p (default: best)
	AudioOnly          bool   // extract audio instead of downloading video
	CookiesFromBrowser string // e.g. "chrome", "firefox" (optional)
	Logger             logging.Logger
	Observer           Observer
}

// Result describes a completed download.
type Result struct {
	Path     string
	Metadata Metadata
}

var (
	progressRe = regexp.MustCompile(`\[download\]\s+([0-9.]+)%`)
	speedRe    = regexp.MustCompile(`at\s+([^\s]+/s)`)
	etaRe      = regexp.MustCompile(`ETA\s+([0-9:]+)`)
	heightRe   = regexp.MustCompile(`([0-9]{3,4})`)
	unsafeRe   = regexp.MustCompile(`[\\/:*?"<>|\x00-\x1f]`)
)

// DetectPlatform classifies a URL by host.
func DetectPlatform(rawURL string) Platform {
	u := strings.ToLower(rawURL)
	switch {
	case strings.Contains(u, "youtube.com"),
		strings.Contains(u, "youtu.be"),
		strings.Contains(u, "youtube-nocookie.com"):
		return PlatformYouTube
	case strings.Contains(u, "twitter.com"),
		strings.Contains(u, "x.com"):
		return PlatformTwitter
	default:
		return PlatformOther
	}
}

// BinaryPath returns the path to the yt-dlp executable, honouring the
// POLAR_RESOLVE_YTDLP override.
func BinaryPath() (string, error) {
	if p := strings.TrimSpace(os.Getenv(BinaryEnv)); p != "" {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, nil
		}
		return "", fmt.Errorf("%s points to %q, which is not an executable file", BinaryEnv, p)
	}

	if p, err := exec.LookPath("yt-dlp"); err == nil {
		return p, nil
	}

	for _, p := range []string{"/usr/local/bin/yt-dlp", "/usr/bin/yt-dlp"} {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, nil
		}
	}

	return "", fmt.Errorf("yt-dlp not found on PATH (install it from https://github.com/yt-dlp/yt-dlp or set %s)", BinaryEnv)
}

// Available reports whether a usable yt-dlp binary can be located.
func Available() bool {
	_, err := BinaryPath()
	return err == nil
}

// Probe fetches metadata for a URL without downloading it.
func Probe(url string) (*Metadata, error) {
	bin, err := BinaryPath()
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(bin,
		"--dump-single-json",
		"--no-warnings",
		"--skip-download",
		url,
	)

	var stderr strings.Builder
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		return nil, wrapYtDlpError(err, stderr.String())
	}

	var raw struct {
		ID       string  `json:"id"`
		Title    string  `json:"title"`
		Duration float64 `json:"duration"`
		Ext      string  `json:"ext"`
		Width    int     `json:"width"`
		Height   int     `json:"height"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parse yt-dlp metadata: %w", err)
	}

	return &Metadata{
		ID:       raw.ID,
		Title:    raw.Title,
		Duration: raw.Duration,
		Ext:      raw.Ext,
		Width:    raw.Width,
		Height:   raw.Height,
	}, nil
}

// Download fetches the video described by cfg and returns the path to the
// resulting file along with its metadata. The file is written into cfg.OutputDir.
func Download(cfg Config) (*Result, error) {
	log := cfg.Logger
	if log == nil {
		log = logging.Nop()
	}

	if strings.TrimSpace(cfg.URL) == "" {
		return nil, fmt.Errorf("download URL is empty")
	}
	if cfg.OutputDir == "" {
		return nil, fmt.Errorf("download output directory is empty")
	}
	if err := os.MkdirAll(cfg.OutputDir, 0o755); err != nil {
		return nil, fmt.Errorf("create download directory: %w", err)
	}

	bin, err := BinaryPath()
	if err != nil {
		return nil, err
	}

	manifestPath := filepath.Join(cfg.OutputDir, ".yt-dlp-manifest")
	_ = os.Remove(manifestPath)
	defer os.Remove(manifestPath)

	args := []string{
		"--no-playlist",
		"--no-warnings",
		"--newline",
		"--no-simulate",
		"--no-write-thumbnail",
		"--no-write-info-json",
		"--no-mtime",
		"--retries", "3",
		"--fragment-retries", "3",
		"--print-to-file", manifestTemplate, manifestPath,
		"-o", filepath.Join(cfg.OutputDir, "%(title)s.%(ext)s"),
	}

	if cfg.AudioOnly {
		args = append(args,
			"-f", "bestaudio/best",
			"--extract-audio",
			"--audio-format", "mp3",
			"--audio-quality", "0",
		)
	} else {
		args = append(args,
			"-f", formatSelector(cfg.Quality),
			"--merge-output-format", "mp4",
		)
	}

	if cfg.CookiesFromBrowser != "" {
		args = append(args, "--cookies-from-browser", cfg.CookiesFromBrowser)
	}

	args = append(args, cfg.URL)

	log.Infof("Running yt-dlp: %s", strings.Join(args, " "))

	cmd := exec.Command(bin, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("create yt-dlp stdout pipe: %w", err)
	}

	var stderr strings.Builder
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start yt-dlp: %w", err)
	}

	scanProgress(stdout, cfg.Observer)

	if err := cmd.Wait(); err != nil {
		return nil, wrapYtDlpError(err, stderr.String())
	}

	downloadedPath, meta := readManifest(manifestPath)
	if downloadedPath == "" || !fileExists(downloadedPath) {
		// Fall back to scanning the output directory for the newest file.
		downloadedPath = newestFile(cfg.OutputDir)
	}
	if downloadedPath == "" {
		return nil, fmt.Errorf("yt-dlp reported success but no output file was found in %s", cfg.OutputDir)
	}

	if meta.ID == "" {
		meta.ID = strings.TrimSuffix(filepath.Base(downloadedPath), filepath.Ext(downloadedPath))
	}
	if meta.Title == "" {
		meta.Title = meta.ID
	}
	if meta.Ext == "" {
		meta.Ext = strings.TrimPrefix(filepath.Ext(downloadedPath), ".")
	}

	log.Infof("Downloaded %s", downloadedPath)

	return &Result{Path: downloadedPath, Metadata: meta}, nil
}

const manifestTemplate = "after_move:%(filepath)s\t%(id)s\t%(title)s\t%(duration)s\t%(ext)s\t%(width)s\t%(height)s"

// scanProgress reads yt-dlp's stdout and forwards download progress.
func scanProgress(r io.Reader, observer Observer) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if observer == nil {
			continue
		}
		m := progressRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		pct, err := parsePercent(m[1])
		if err != nil {
			continue
		}
		observer.OnDownloadProgress(pct, progressDetail(line, pct))
	}
}

func progressDetail(line string, pct int) string {
	detail := fmt.Sprintf("%d%%", pct)
	if m := speedRe.FindStringSubmatch(line); m != nil {
		detail += " at " + m[1]
	}
	if m := etaRe.FindStringSubmatch(line); m != nil {
		detail += " ETA " + m[1]
	}
	return detail
}

func parsePercent(s string) (int, error) {
	var f float64
	if _, err := fmt.Sscanf(s, "%f", &f); err != nil {
		return 0, err
	}
	pct := int(f + 0.5)
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return pct, nil
}

// formatSelector maps a quality label to a yt-dlp format selector.
func formatSelector(quality string) string {
	q := strings.ToLower(strings.TrimSpace(quality))
	if q == "" || q == "best" {
		return "bv*+ba/b"
	}
	m := heightRe.FindString(q)
	if m == "" {
		return "bv*+ba/b"
	}
	return fmt.Sprintf("bv*[height<=%s]+ba/b[height<=%s]/bv*+ba/b", m, m)
}

// readManifest parses the tab-separated manifest line written by yt-dlp.
func readManifest(path string) (string, Metadata) {
	f, err := os.Open(path)
	if err != nil {
		return "", Metadata{}
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	var lastLine string
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			lastLine = line
		}
	}
	if lastLine == "" {
		return "", Metadata{}
	}

	fields := strings.Split(lastLine, "\t")
	meta := Metadata{}
	if len(fields) > 1 {
		meta.ID = strings.TrimSpace(fields[1])
	}
	if len(fields) > 2 {
		meta.Title = strings.TrimSpace(fields[2])
	}
	if len(fields) > 3 {
		fmt.Sscanf(strings.TrimSpace(fields[3]), "%f", &meta.Duration)
	}
	if len(fields) > 4 {
		meta.Ext = strings.TrimSpace(fields[4])
	}
	if len(fields) > 5 {
		fmt.Sscanf(strings.TrimSpace(fields[5]), "%d", &meta.Width)
	}
	if len(fields) > 6 {
		fmt.Sscanf(strings.TrimSpace(fields[6]), "%d", &meta.Height)
	}

	return strings.TrimSpace(fields[0]), meta
}

func newestFile(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var best string
	var bestMod int64
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") || strings.HasSuffix(e.Name(), ".part") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if mod := info.ModTime().UnixNano(); mod > bestMod {
			bestMod = mod
			best = filepath.Join(dir, e.Name())
		}
	}
	return best
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func wrapYtDlpError(err error, stderr string) error {
	msg := strings.TrimSpace(stderr)
	if msg == "" {
		return fmt.Errorf("yt-dlp: %w", err)
	}
	return fmt.Errorf("yt-dlp: %w\n%s", err, msg)
}

// SanitizeFilename turns a video title into a safe base filename (without
// extension). An empty or fully stripped title becomes "video".
func SanitizeFilename(name string) string {
	name = strings.TrimSpace(name)
	name = unsafeRe.ReplaceAllString(name, "_")
	name = strings.Trim(name, ". ")
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return "video"
	}
	const maxLen = 120
	if len(name) > maxLen {
		name = strings.TrimSpace(name[:maxLen])
	}
	return name
}
