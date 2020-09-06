package inst

import (
	"net/http"

	"github.com/apundir/wsbalancer/common"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	namespace = "wsbalancer"
	dimFe     = "frontend"
	dimBe     = "backend"
	dimDir    = "direction"
	dirF2B    = "f2b"
	dirB2F    = "b2f"
)

var (
	newSessionCtr = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "new_session",
		Help:      "New sessions created in balancer",
	}, []string{dimFe, dimBe})
	msgSizeHg = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "message_size",
		Help:      "Message size metric",
		Buckets:   []float64{128, 256, 512, 1024, 2048, 5120},
	}, []string{dimDir, dimFe, dimBe})
	curSessionGg = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "active_sessions",
		Help:      "Currently active sessions in balancer",
	}, []string{dimFe, dimBe})
)

// PrometheusProvider Prometheus valve provider for usage within balancer
// frontend
type PrometheusProvider struct{}

// RegisterHTTPHandlers registers and exposes prometheus metrics at /metrics
// endpoint to supplied mux
func RegisterHTTPHandlers(http *http.ServeMux) {
	http.Handle("/metrics", promhttp.Handler())
}

// AddValves adds prometheus instrumentation valve at the begining of the
// supplied chain
func (pip *PrometheusProvider) AddValves(session *common.ProxySession,
	currentValves []common.MessageValve) []common.MessageValve {
	valves := make([]common.MessageValve, len(currentValves)+1)
	valves[0] = NewInstrumentValve(session.FEConnection.FrontendID(), session.BEConnection.BackendID())
	copy(valves, currentValves)
	return valves
}

// SessionCreated registers a new session in promethus total session counter and
// increments the current active session gauge.
func (pip *PrometheusProvider) SessionCreated(session *common.ProxySession) {
	newSessionCtr.With(prometheus.Labels{dimBe: session.BEConnection.BackendID(),
		dimFe: session.FEConnection.FrontendID()}).Inc()
	curSessionGg.With(prometheus.Labels{dimBe: session.BEConnection.BackendID(),
		dimFe: session.FEConnection.FrontendID()}).Inc()
}

// SessionTerminated decrements the current active session gauge.
func (pip *PrometheusProvider) SessionTerminated(session *common.ProxySession) {
	curSessionGg.With(prometheus.Labels{
		dimBe: session.BEConnection.BackendID(),
		dimFe: session.FEConnection.FrontendID()}).Dec()
}

// InstrumentValve collect instrumentation data for reporting using prometheus
type InstrumentValve struct {
	// Id of the frontend endpoint
	frontend string

	// Id of the backend server
	backend string
}

// NewInstrumentValve initialize a new valve with given frontend and backend.
// frontend and backend are expected to be ID values (string) for these systems.
func NewInstrumentValve(frontend string, backend string) *InstrumentValve {
	return &InstrumentValve{
		frontend: frontend,
		backend:  backend,
	}
}

// BeforeTransmit instrument messages before they are transmitted to backend
func (inst *InstrumentValve) BeforeTransmit(msg *common.WsMessage) (*common.WsMessage, error) {
	if msg.IsControl() {
		return msg, nil
	}
	msgSizeHg.With(prometheus.Labels{dimBe: inst.backend,
		dimFe: inst.frontend, dimDir: dirF2B}).Observe(float64(len(msg.Data)))
	return msg, nil
}

// AfterReceive instrument messages after being received from backend
func (inst *InstrumentValve) AfterReceive(msg *common.WsMessage) (*common.WsMessage, error) {
	if msg.IsControl() {
		return msg, nil
	}
	msgSizeHg.With(prometheus.Labels{dimBe: inst.backend,
		dimFe: inst.frontend, dimDir: dirB2F}).Observe(float64(len(msg.Data)))
	return msg, nil
}
