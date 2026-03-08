package progress

import (
	"fmt"
	"time"
)

// Tracker reports progress for a multi-step operation.
type Tracker interface {
	// OnProgress is called after each unit of work completes.
	// current is 1-based, total may be 0 if unknown.
	OnProgress(current, total int)
	// Finish is called when the operation is done (allows cleanup like printing a newline).
	Finish()
}

// ConsoleTracker prints frame/tile progress to stdout using carriage returns.
type ConsoleTracker struct {
	startTime time.Time
}

// NewConsoleTracker creates a ConsoleTracker that measures elapsed time from now.
func NewConsoleTracker() *ConsoleTracker {
	return &ConsoleTracker{startTime: time.Now()}
}

func (t *ConsoleTracker) OnProgress(current, total int) {
	elapsed := time.Since(t.startTime).Seconds()
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

func (t *ConsoleTracker) Finish() {
	fmt.Println()
}

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
