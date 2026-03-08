package web

import (
	"sync"

	"github.com/Yeti47/polar-resolve/internal/model"
)

// initPhase represents a stage of the server's background initialisation.
type initPhase string

const (
	phaseDownloading  initPhase = initPhase(model.PhaseDownloading)
	phaseInitializing initPhase = "initializing"
	phaseReady        initPhase = "ready"
	phaseError        initPhase = "error"
)

// initStatus tracks the background model-download + upscaler-init progress.
type initStatus struct {
	mu          sync.RWMutex
	phase       initPhase
	current     int64 // bytes downloaded so far (downloading phase)
	total       int64 // total bytes (-1 if unknown)
	errMsg      string
	subscribers []chan struct{}
}

func (s *initStatus) get() (phase initPhase, current, total int64, errMsg string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.phase, s.current, s.total, s.errMsg
}

func (s *initStatus) set(phase initPhase, current, total int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.phase = phase
	s.current = current
	s.total = total
	for _, ch := range s.subscribers {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (s *initStatus) setError(msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.phase = phaseError
	s.errMsg = msg
	for _, ch := range s.subscribers {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	for _, ch := range s.subscribers {
		close(ch)
	}
	s.subscribers = nil
}

func (s *initStatus) setReady() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.phase = phaseReady
	for _, ch := range s.subscribers {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	for _, ch := range s.subscribers {
		close(ch)
	}
	s.subscribers = nil
}

func (s *initStatus) subscribe() chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := make(chan struct{}, 64)
	if s.phase == phaseReady || s.phase == phaseError {
		ch <- struct{}{}
		close(ch)
	} else {
		s.subscribers = append(s.subscribers, ch)
	}
	return ch
}

func (s *initStatus) unsubscribe(ch chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, sub := range s.subscribers {
		if sub == ch {
			s.subscribers = append(s.subscribers[:i], s.subscribers[i+1:]...)
			break
		}
	}
}

// initDownloadObserver implements model.DownloadObserver by forwarding
// progress updates to the server's initStatus.
type initDownloadObserver struct {
	initSt *initStatus
}

func (o *initDownloadObserver) OnDownloadProgress(phase model.DownloadPhase, current, total int64) {
	o.initSt.set(initPhase(phase), current, total)
}
