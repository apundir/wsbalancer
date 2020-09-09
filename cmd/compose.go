package cmd

import (
	"net/http"
	"strings"
	"sync"

	"github.com/apundir/wsbalancer/in"
	"github.com/apundir/wsbalancer/inst"
	slog "github.com/go-eden/slf4go"
)

var (
	logLevelOnce *sync.Once = &sync.Once{}
)

// BuildFrontendServer builds http server for handling frontend websocket
// requests. The is configured to respond to all requests as if they are
// websocket requests.
func BuildFrontendServer(cfg *in.ServerConfig, inMgr *in.Manager) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/", inMgr.ServeWs)
	return &http.Server{
		Addr:           cfg.FrontendHTTP.Addr,
		Handler:        mux,
		ReadTimeout:    cfg.FrontendHTTP.ReadTimeout,
		WriteTimeout:   cfg.FrontendHTTP.WriteTimeout,
		MaxHeaderBytes: cfg.FrontendHTTP.MaxHeaderBytes,
	}
}

// BuildAdminServer builds the admin http server for serving admin API requests.
// The admin server is purposefully kept separate since this endpoint will not
// (MUST NOT) be exposed to general public and is intended to be used over an
// internal secure network only.
//
// If enabled, prometheus endpoint will be exposed on the admin server itself.
func BuildAdminServer(cfg *in.ServerConfig, inMgr *in.Manager) *http.Server {
	mux := http.NewServeMux()
	inMgr.RegisterHTTPHandlers(mux)
	if cfg.EnableInstrumentation {
		pp := &inst.PrometheusProvider{}
		inMgr.AddSessionListener(pp)
		inMgr.AddValveProvider(pp)
		inst.RegisterHTTPHandlers(mux)
		slog.Info("Enabled prometheus instrumentation")
	}
	return &http.Server{
		Addr:           cfg.AdminHTTP.Addr,
		Handler:        mux,
		ReadTimeout:    cfg.AdminHTTP.ReadTimeout,
		WriteTimeout:   cfg.AdminHTTP.WriteTimeout,
		MaxHeaderBytes: cfg.AdminHTTP.MaxHeaderBytes,
	}
}

// SetLogLevel sets the global log level for balancer. This method shall be
// called as early as possible in the initialization phases.
func SetLogLevel(level string) {
	logLevelOnce.Do(func() {
		switch strings.ToUpper(level) {
		case "TRACE":
			slog.SetLevel(slog.TraceLevel)
		case "DEBUG":
			slog.SetLevel(slog.DebugLevel)
		case "INFO":
			slog.SetLevel(slog.InfoLevel)
		case "WARN":
			slog.SetLevel(slog.WarnLevel)
		case "PANIC":
			slog.SetLevel(slog.PanicLevel)
		case "FATAL":
			slog.SetLevel(slog.FatalLevel)
		default:
			slog.SetLevel(slog.PanicLevel)
		}
	})
}
