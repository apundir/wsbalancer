package out

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/apundir/wsbalancer/common"
	"github.com/gorilla/websocket"
	circuit "github.com/rubyist/circuitbreaker"
)

// an abstraction to facilitate easy swapping of breaker with
// dummy no-action breaker when this functionality is NOT required
type breaker interface {
	Tripped() bool
	Ready() bool
	Reset()
	Success()
	Fail()
}

// dummyBreaker is a noop implementation of breaker interface.
// This shall be used to disable breaker functionality altogether
type dummyBreaker struct{}

func (db *dummyBreaker) Tripped() bool { return false }
func (db *dummyBreaker) Ready() bool   { return true }
func (db *dummyBreaker) Reset()        {}
func (db *dummyBreaker) Success()      {}
func (db *dummyBreaker) Fail()         {}

type backend struct {
	// this backendConfiguration
	config *BackendConfig

	// dialer that shall be used for establishing connections
	dialer *websocket.Dialer

	// total connections established for this backend so far
	totalConnections uint32

	// currently active connections for this backend
	activeConnections int32

	// total errors from this backend, does not include 3XX and 4XX responses
	totalFailures uint32

	// true if this backend is enabled, false if this is disabled
	// A disabled backend wouldn't participate in new connection
	// requests.
	enabled bool

	// channel that receive signal as and when one of the connection
	// created by this backend is closed
	conDone chan struct{}

	// receives a signal when this backend is deleted
	deleted chan struct{}

	// circuit breaker for backend server
	breaker breaker

	// for receiving breaker events
	breakerEvt chan circuit.ListenerEvent
}

func newBackend(config *BackendConfig) *backend {
	dialer := &websocket.Dialer{
		HandshakeTimeout: config.HandshakeTimeout,
		ReadBufferSize:   config.ReadBufferSize,
		WriteBufferSize:  config.WriteBufferSize,
	}
	bk := &backend{
		config:     config,
		dialer:     dialer,
		enabled:    true,
		conDone:    make(chan struct{}, 100),
		deleted:    make(chan struct{}),
		breakerEvt: make(chan circuit.ListenerEvent, 10),
	}
	if config.BreakerThreshold > 0 {
		_bk := circuit.NewConsecutiveBreaker(int64(config.BreakerThreshold))
		_bk.AddListener(bk.breakerEvt)
		bk.breaker = _bk
	} else {
		bk.breaker = &dummyBreaker{}
	}
	go bk.startManagementListener()
	return bk
}

func (bk *backend) delete() {
	bk.enabled = false
	go func() {
		// Wait at max 1 minute for connections to drain
		for i := 0; i < 600; i++ {
			if atomic.LoadInt32(&bk.activeConnections) < 1 {
				blog.Debug("All connections drained from backend: ", bk.config.ID)
				bk.deleted <- struct{}{}
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		blog.Warnf("backend %v still have %v active connection, deleting anyway",
			bk.config.ID, atomic.LoadInt32(&bk.activeConnections))
		bk.deleted <- struct{}{}
	}()
}

func (bk *backend) startManagementListener() {
	for {
		select {
		case <-bk.conDone:
			atomic.AddInt32(&bk.activeConnections, -1)
		case <-bk.deleted:
			blog.Infof("backend %v deleted", bk.config.ID)
			return
		case evt := <-bk.breakerEvt:
			switch evt.Event {
			case circuit.BreakerTripped:
				blog.Info("circuit breaker tripped for backend ", bk.config.ID)
			case circuit.BreakerReset:
				blog.Info("circuit breaker reset for backend ", bk.config.ID)
			case circuit.BreakerFail:
				blog.Info("circuit breaker marked failed for backend ", bk.config.ID)
			case circuit.BreakerReady:
				blog.Info("circuit breaker ready for backend ", bk.config.ID)
			}
		}
	}
}

func (bk *backend) toJSON() string {
	return fmt.Sprintf("{\"url\":\"%v\",\"id\":\"%v\","+
		"\"activeConnections\":%v,\"enabled\":%v,\"tripped\":%v"+
		",\"totalConnections\":%v,\"totalFailures\":%v}",
		bk.config.URL, bk.config.ID, atomic.LoadInt32(&bk.activeConnections), bk.enabled,
		bk.breaker.Tripped(), atomic.LoadUint32(&bk.totalConnections),
		atomic.LoadUint32(&bk.totalFailures))
}

func (bk *backend) reset() {
	bk.breaker.Reset()
}

func (bk *backend) tripped() bool {
	return bk.breaker.Tripped()
}

func (bk *backend) DialUpstream(dialContext *common.BackendDialContext) (con *outc, err error) {
	if !bk.breaker.Ready() {
		blog.Infof("%v backend tripped, not ready for retry", bk.config.ID)
		return nil, common.NewSystemUnavailableErrorWithCode(bk.config.ID, "CircuitTripped", 0)
	}
	beURL := bk.config.URL + dialContext.URL.RequestURI()
	blog.Info("Connecting to: ", beURL)
	reqHeaders := dialContext.DestHeaders.Clone()
	reqHeaders.Del(common.SubprotocolHeader)

	var oc *outc = nil
	if dialContext.OldConnection != nil {
		_oc, ok := dialContext.OldConnection.(*outc)
		if ok {
			blog.Debugf("dialing for replacing old connection %v", _oc.id)
			oc = _oc
		}
	}

	c, res, er := bk.dialerForConnection(dialContext).Dial(beURL, reqHeaders)
	if res != nil && res.StatusCode >= 100 && res.StatusCode < 300 {
		oc := newOutc(c, dialContext.Receive, dialContext.Send, bk.config.PingTime,
			bk.config.ID, bk.conDone, bk.filterPassHeaders(res), oc)
		oc.id = fmt.Sprintf("%p", oc)[2:]
		bk.breaker.Success()
		atomic.AddInt32(&bk.activeConnections, 1)
		atomic.AddUint32(&bk.totalConnections, 1)
		return oc, nil
	}
	return nil, bk.error(res, er)
}

func (bk *backend) dialerForConnection(dialContext *common.BackendDialContext) *websocket.Dialer {
	inSubprotocols := dialContext.DestHeaders.Values(common.SubprotocolHeader)
	if len(inSubprotocols) < 1 {
		return bk.dialer
	}
	origDialer := *bk.dialer
	// make a copy of the struct, preserving original AS-IS
	newDialer := origDialer
	// change the subprotocol in the copied struct
	newDialer.Subprotocols = inSubprotocols
	return &newDialer
}

func (bk *backend) filterPassHeaders(res *http.Response) http.Header {
	headers := http.Header{}
	for _, ah := range bk.config.PassHeaders {
		hv := res.Header.Values(ah)
		for _, av := range hv {
			headers.Add(ah, av)
		}
	}
	return headers
}

func (bk *backend) error(res *http.Response, er error) error {
	blog.Warn("upstream connection error: ", er)
	errTxt := "connection error"
	if er != nil {
		errTxt = er.Error()
	}
	statusCode := 0
	if res != nil {
		statusCode = res.StatusCode
	}
	if statusCode >= 300 && statusCode < 500 {
		return common.NewConnectionDeclinedError(bk.config.ID, errTxt, statusCode)
	}
	bk.breaker.Fail()
	atomic.AddUint32(&bk.totalFailures, 1)
	return common.NewSystemUnavailableErrorWithCode(bk.config.ID, errTxt, statusCode)
}
