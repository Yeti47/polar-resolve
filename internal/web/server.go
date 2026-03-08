package web

import (
	_ "embed"
	"fmt"
	"os"
	"sync"

	"github.com/Yeti47/polar-resolve/internal/logging"
	"github.com/Yeti47/polar-resolve/internal/model"
	"github.com/Yeti47/polar-resolve/internal/upscaler"
	"github.com/gin-gonic/gin"
)

//go:embed index.html
var indexHTML string

// ServerConfig holds the configuration for the web server.
type ServerConfig struct {
	Bind      string
	Port      int
	ModelPath string
	Device    string
	LibPath   string
	Verbose   bool
	Logger    logging.Logger
}

// Server is the web UI server.
type Server struct {
	config   ServerConfig
	log      logging.Logger
	upscaler *upscaler.Upscaler
	jobs     sync.Map
	tmpDir   string
	queue    chan *job
	ready    chan struct{}
	initSt   *initStatus
}

// NewServer creates a new web UI server. Model download and upscaler
// initialisation happen in the background; the HTTP server is available
// immediately so the browser can show download progress.
func NewServer(cfg ServerConfig) (*Server, error) {
	log := cfg.Logger
	if log == nil {
		log = logging.Nop()
	}

	tmpDir, err := os.MkdirTemp("", "polar-resolve-web-*")
	if err != nil {
		return nil, fmt.Errorf("create temp directory: %w", err)
	}

	s := &Server{
		config: cfg,
		log:    log,
		tmpDir: tmpDir,
		queue:  make(chan *job, 100),
		ready:  make(chan struct{}),
		initSt: &initStatus{phase: phaseInitializing},
	}

	go s.backgroundInit()
	go s.processQueue()

	return s, nil
}

// backgroundInit downloads the model (if needed) and creates the upscaler.
func (s *Server) backgroundInit() {
	modelPath := s.config.ModelPath

	if modelPath == "" {
		s.initSt.set(phaseDownloading, 0, -1)
		var err error
		modelPath, err = model.EnsureModelWithProgress(s.log, &initDownloadObserver{initSt: s.initSt})
		if err != nil {
			s.initSt.setError(fmt.Sprintf("Failed to download model: %v", err))
			return
		}
	}

	s.initSt.set(phaseInitializing, 0, 0)

	u, err := upscaler.New(upscaler.Config{
		ModelPath:   modelPath,
		Device:      s.config.Device,
		LibPath:     s.config.LibPath,
		TileSize:    128,
		TileOverlap: 16,
		Logger:      s.log,
	})
	if err != nil {
		s.initSt.setError(fmt.Sprintf("Failed to initialize upscaler: %v", err))
		return
	}

	s.upscaler = u
	s.initSt.setReady()
	close(s.ready)
}

// Run starts the HTTP server on the configured address.
func (s *Server) Run() error {
	defer s.cleanup()

	if !s.config.Verbose {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.Default()
	r.MaxMultipartMemory = 32 << 20 // 32 MiB in memory; larger files spill to disk

	r.GET("/", s.handleIndex)
	api := r.Group("/api")
	{
		api.GET("/status", s.handleStatus)
		api.POST("/jobs", s.handleCreateJob)
		api.GET("/jobs/:id/events", s.handleJobEvents)
		api.GET("/jobs/:id/download", s.handleDownload)
	}

	addr := fmt.Sprintf("%s:%d", s.config.Bind, s.config.Port)
	fmt.Printf("polar-resolve web UI available at http://%s\n", addr)
	return r.Run(addr)
}

func (s *Server) cleanup() {
	if s.upscaler != nil {
		s.upscaler.Close()
	}
	os.RemoveAll(s.tmpDir)
}
