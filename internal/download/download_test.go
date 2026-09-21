package download

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectPlatform(t *testing.T) {
	cases := map[string]Platform{
		"https://www.youtube.com/watch?v=abc": PlatformYouTube,
		"https://youtu.be/abc":                PlatformYouTube,
		"https://m.youtube.com/watch?v=abc":   PlatformYouTube,
		"https://twitter.com/user/status/1":   PlatformTwitter,
		"https://x.com/user/status/1":         PlatformTwitter,
		"https://example.com/video.mp4":       PlatformOther,
	}
	for url, want := range cases {
		if got := DetectPlatform(url); got != want {
			t.Errorf("DetectPlatform(%q) = %q, want %q", url, got, want)
		}
	}
}

func TestSanitizeFilename(t *testing.T) {
	cases := map[string]string{
		"Me at the zoo":         "Me at the zoo",
		"a/b\\c:d*e?f\"g<h>i|j": "a_b_c_d_e_f_g_h_i_j",
		"  spaced   out  ":      "spaced out",
		"...":                   "video",
		"":                      "video",
		"line\nbreak":           "line_break",
	}
	for in, want := range cases {
		if got := SanitizeFilename(in); got != want {
			t.Errorf("SanitizeFilename(%q) = %q, want %q", in, got, want)
		}
	}

	long := make([]byte, 200)
	for i := range long {
		long[i] = 'a'
	}
	if got := SanitizeFilename(string(long)); len(got) > 120 {
		t.Errorf("SanitizeFilename truncated to %d chars, want <= 120", len(got))
	}
}

func TestFormatSelector(t *testing.T) {
	cases := map[string]string{
		"":      "bv*+ba/b",
		"best":  "bv*+ba/b",
		"1080p": "bv*[height<=1080]+ba/b[height<=1080]/bv*+ba/b",
		"720":   "bv*[height<=720]+ba/b[height<=720]/bv*+ba/b",
	}
	for in, want := range cases {
		if got := formatSelector(in); got != want {
			t.Errorf("formatSelector(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReadManifest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".yt-dlp-manifest")
	line := "/tmp/video.mp4\tid123\tMy Video\t12.5\tmp4\t1920\t1080\n"
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	gotPath, meta := readManifest(path)
	if gotPath != "/tmp/video.mp4" {
		t.Errorf("path = %q, want /tmp/video.mp4", gotPath)
	}
	if meta.ID != "id123" || meta.Title != "My Video" {
		t.Errorf("meta id/title = %q/%q", meta.ID, meta.Title)
	}
	if meta.Duration != 12.5 || meta.Ext != "mp4" || meta.Width != 1920 || meta.Height != 1080 {
		t.Errorf("meta = %+v", meta)
	}
}
