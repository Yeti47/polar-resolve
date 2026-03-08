package web

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"

	"github.com/Yeti47/polar-resolve/internal/progress"
)

type jobStatus string

const (
	statusQueued     jobStatus = "queued"
	statusProcessing jobStatus = "processing"
	statusCompleted  jobStatus = "completed"
	statusFailed     jobStatus = "failed"
)

type jobEvent struct {
	Status   jobStatus `json:"status"`
	Progress int       `json:"progress"`
	Detail   string    `json:"detail"`
	Error    string    `json:"error,omitempty"`
}

type job struct {
	id       string
	fileType string // "image" or "video"
	status   jobStatus
	progress int
	detail   string
	errMsg   string

	inputPath  string
	outputPath string
	outputName string

	// image options
	format string

	// video options
	codec   string
	crf     int
	noAudio bool

	// common options
	tileSize    int
	tileOverlap int

	mu          sync.Mutex
	subscribers []chan jobEvent
}

func (j *job) broadcast(evt jobEvent) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.status = evt.Status
	j.progress = evt.Progress
	j.detail = evt.Detail
	j.errMsg = evt.Error
	for _, ch := range j.subscribers {
		select {
		case ch <- evt:
		default:
		}
	}
	if evt.Status == statusCompleted || evt.Status == statusFailed {
		for _, ch := range j.subscribers {
			close(ch)
		}
		j.subscribers = nil
	}
}

func (j *job) subscribe() chan jobEvent {
	j.mu.Lock()
	defer j.mu.Unlock()
	ch := make(chan jobEvent, 64)
	ch <- jobEvent{
		Status:   j.status,
		Progress: j.progress,
		Detail:   j.detail,
		Error:    j.errMsg,
	}
	if j.status != statusCompleted && j.status != statusFailed {
		j.subscribers = append(j.subscribers, ch)
	} else {
		close(ch)
	}
	return ch
}

func (j *job) unsubscribe(ch chan jobEvent) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for i, sub := range j.subscribers {
		if sub == ch {
			j.subscribers = append(j.subscribers[:i], j.subscribers[i+1:]...)
			break
		}
	}
}

func newJob() *job {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return &job{
		id:     hex.EncodeToString(b),
		status: statusQueued,
	}
}

// jobTracker implements progress.Tracker by broadcasting events to a job's subscribers.
type jobTracker struct {
	job   *job
	label string // e.g. "Tile" or "Frame"
}

var _ progress.Tracker = (*jobTracker)(nil)

func (t *jobTracker) OnProgress(current, total int) {
	pct := 0
	if total > 0 {
		pct = current * 100 / total
	}
	t.job.broadcast(jobEvent{
		Status:   statusProcessing,
		Progress: pct,
		Detail:   fmt.Sprintf("%s %d / %d", t.label, current, total),
	})
}

func (t *jobTracker) Finish() {}
