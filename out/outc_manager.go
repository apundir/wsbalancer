package out

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/apundir/wsbalancer/common"
	slog "github.com/go-eden/slf4go"
	"github.com/gorilla/websocket"
)

var (
	mlog = slog.NewLogger("out.mgr")
	blog = slog.NewLogger("out.bk")
	olog = slog.NewLogger("out.oc")
)

// for sending and receiving backend configuration as json
type newBackendDef struct {
	URL                string   `json:"url"`
	ID                 string   `json:"id"`
	PingTime           string   `json:"ping-time"`
	ReadBufferSize     int      `json:"read-buffer-size"`
	WriteBufferSize    int      `json:"write-buffer-size"`
	BreakerThreshold   int      `json:"breaker-threshold"`
	HandshakeTimeout   string   `json:"handshake-timeout"`
	PassHeaders        []string `json:"pass-headers"`
	AbnormalCloseCodes []int    `json:"abnormal-close-codes"`
}

// Manager is a pool that manages all available backends
type Manager struct {
	// list of backend server objects
	backends []*backend

	// disabled backends
	disabledBe []*backend

	// index of the next backend that shall be used
	// All enabled backends are used in round robin
	// TODO: Add upstream server weighted connection policy manager
	nextBackendIdx int

	// protect nextUsURLIdx from concurrent writes
	nxtIdxLock *sync.Mutex

	// dialer to use for dialing backends
	dialer *websocket.Dialer

	// configuration for the upstream system including backends
	config *UpstreamConfig
}

// NewManager create a new initialized backend Manager
func NewManager(config *UpstreamConfig) (*Manager, error) {
	config.sanitize()
	if len(config.Backends) < 1 {
		return nil, errors.New("At least one backend must be provided")
	}
	bks := make([]*backend, len(config.Backends))
	dialer := &websocket.Dialer{
		HandshakeTimeout: config.HandshakeTimeout,
		ReadBufferSize:   config.ReadBufferSize,
		WriteBufferSize:  config.WriteBufferSize,
	}
	for i, bk := range config.Backends {
		bks[i] = newBackend(bk)
	}
	return &Manager{
		nextBackendIdx: 0,
		nxtIdxLock:     &sync.Mutex{},
		backends:       bks,
		disabledBe:     make([]*backend, 0, 10),
		dialer:         dialer,
		config:         config,
	}, nil
}

// Stop shuts down all backends, primarily used for test purposes
func (mgr *Manager) Stop() {
	allBks := make([]*backend, len(mgr.backends)+len(mgr.disabledBe))
	copy(allBks, mgr.backends)
	copy(allBks[len(mgr.backends):], mgr.disabledBe)
	mgr.backends = make([]*backend, 0)
	mgr.disabledBe = make([]*backend, 0)
	for _, bk := range allBks {
		bk.delete()
	}
}

// ListBackends - lists all backend as json
func (mgr *Manager) ListBackends() string {
	allBks := make([]*backend, len(mgr.backends)+len(mgr.disabledBe))
	copy(allBks, mgr.backends)
	copy(allBks[len(mgr.backends):], mgr.disabledBe)
	var b strings.Builder
	b.Write([]byte{'['})
	for i, bk := range allBks {
		b.WriteString(bk.toJSON())
		if i < len(allBks)-1 {
			b.Write([]byte{','})
		}
	}
	b.Write([]byte{']'})
	return b.String()
}

// BackendConfig returns configuration of given backend
func (mgr *Manager) BackendConfig(id string) string {
	var bk *backend
	if idx := findIndex(mgr.disabledBe, id); idx > -1 {
		bk = mgr.disabledBe[idx]
	} else if idx = findIndex(mgr.backends, id); idx > -1 {
		bk = mgr.backends[idx]
	}
	if bk == nil {
		return "{}"
	}
	bkData := &newBackendDef{
		URL:                bk.config.URL,
		ID:                 bk.config.ID,
		BreakerThreshold:   bk.config.BreakerThreshold,
		ReadBufferSize:     bk.config.ReadBufferSize,
		WriteBufferSize:    bk.config.WriteBufferSize,
		PingTime:           bk.config.PingTime.String(),
		HandshakeTimeout:   bk.config.HandshakeTimeout.String(),
		PassHeaders:        bk.config.PassHeaders,
		AbnormalCloseCodes: bk.config.AbnormalCloseCodes,
	}
	resp, _ := json.Marshal(bkData)
	return string(resp)
}

// AddBackend - add given backend as NEW backend
func (mgr *Manager) AddBackend(data []byte) string {
	bkData := newBackendDef{}
	err := json.Unmarshal(data, &bkData)
	if err != nil {
		mlog.Warnf("Error parsing %v => %v", string(data), err)
		return "{\"result\":\"failed\"}"
	}
	if bkData.URL == "" {
		return "{\"result\":\"failed\",\"reason\":\"no url present in request\"}"
	}
	// Verify this backend don't exist already
	for _, bk := range mgr.backends {
		if bk.config.URL == bkData.URL {
			return "{\"result\":\"failed\",\"reason\":\"url already exists\"}"
		}
	}
	nbk := mgr.config.appendBackend(bkData.URL, bkData.ID)
	nbk.PingTime = mgr.parseDuration(bkData.PingTime, nbk.PingTime)
	nbk.HandshakeTimeout = mgr.parseDuration(bkData.HandshakeTimeout, nbk.HandshakeTimeout)
	if bkData.ReadBufferSize > 0 {
		nbk.ReadBufferSize = bkData.ReadBufferSize
	}
	if bkData.WriteBufferSize > 0 {
		nbk.WriteBufferSize = bkData.WriteBufferSize
	}
	if bkData.BreakerThreshold != 0 {
		nbk.BreakerThreshold = bkData.BreakerThreshold
	}
	if len(bkData.PassHeaders) > 0 {
		nbk.PassHeaders = bkData.PassHeaders
	}
	if len(bkData.AbnormalCloseCodes) > 0 {
		nbk.AbnormalCloseCodes = bkData.AbnormalCloseCodes
	}
	mgr.backends = append(mgr.backends, newBackend(nbk))
	return "{\"result\":\"success\"}"
}

func findIndex(be []*backend, id string) int {
	if id == "" {
		return -1
	}
	for i, bk := range be {
		if bk.config.ID == id {
			return i
		}
	}
	return -1
}

func removeFromIndex(be []*backend, idx int) []*backend {
	newBe := make([]*backend, len(be)-1)
	copy(newBe, be[0:idx])
	copy(newBe[idx:], be[idx+1:])
	return newBe
}

// DisableBackend - disable given backend thus no more NEW connections to this B/E
func (mgr *Manager) DisableBackend(id string) (int, error) {
	if findIndex(mgr.disabledBe, id) > -1 {
		// this backend is already disabled
		return common.ResultNoActionReqd, nil
	}
	idx := findIndex(mgr.backends, id)
	if idx < 0 {
		return common.ResultNoActionReqd, nil
	}
	beToDisable := mgr.backends[idx]
	beToDisable.enabled = false
	mgr.disabledBe = append(mgr.disabledBe, beToDisable)
	mgr.backends = removeFromIndex(mgr.backends, idx)
	mgr.nextBackendIdx = 0 // reset next backend index
	return common.ResultSuccess, nil
}

// EnableBackend - disable given backend thus no more NEW connections to this B/E
func (mgr *Manager) EnableBackend(id string) (int, error) {
	if findIndex(mgr.backends, id) > -1 {
		// this backend is already enabled
		return common.ResultNoActionReqd, nil
	}
	idx := findIndex(mgr.disabledBe, id)
	if idx < 0 {
		return common.ResultFailed, nil
	}
	beToEnable := mgr.disabledBe[idx]
	beToEnable.enabled = true
	mgr.backends = append(mgr.backends, beToEnable)
	mgr.disabledBe = removeFromIndex(mgr.disabledBe, idx)
	return common.ResultSuccess, nil
}

// DeleteBackend - delete this backend and terminate all connections to this B/E
func (mgr *Manager) DeleteBackend(id string) (int, error) {
	var idx int
	if idx = findIndex(mgr.disabledBe, id); idx > -1 {
		mgr.disabledBe[idx].delete()
		mgr.deleteBackendConfig(mgr.disabledBe[idx].config.ID)
		mgr.disabledBe = removeFromIndex(mgr.disabledBe, idx)
		return common.ResultSuccess, nil
	}
	idx = findIndex(mgr.backends, id)
	if idx < 0 {
		return common.ResultNoActionReqd, nil
	}
	beToDel := mgr.backends[idx]
	beToDel.delete()
	mgr.backends = removeFromIndex(mgr.backends, idx)
	mgr.deleteBackendConfig(beToDel.config.ID)
	mgr.nextBackendIdx = 0 // reset next backend index
	return common.ResultSuccess, nil
}

func (mgr *Manager) deleteBackendConfig(id string) {
	newBkCfg := make([]*BackendConfig, len(mgr.config.Backends)-1)
	newIdx := 0
	for _, bk := range mgr.config.Backends {
		if bk.ID == id {
			continue
		}
		newBkCfg[newIdx] = bk
		newIdx++
	}
	mgr.config.Backends = newBkCfg
}

// ResetBackend resets the circuit breaker so that subsequent
// calls are allowed to this backend
func (mgr *Manager) ResetBackend(id string) (int, error) {
	var idx int
	// search through disabled backends first
	if idx = findIndex(mgr.disabledBe, id); idx > -1 {
		if mgr.disabledBe[idx].tripped() {
			mgr.disabledBe[idx].reset()
			return common.ResultSuccess, nil
		}
	}
	// now search through enabled backends
	idx = findIndex(mgr.backends, id)
	if idx < 0 {
		return common.ResultFailed, nil
	}
	beToReset := mgr.backends[idx]
	if beToReset.tripped() {
		beToReset.reset()
		return common.ResultSuccess, nil
	}
	return common.ResultNoActionReqd, nil
}

func (mgr *Manager) nextBackendList() []*backend {
	mgr.nxtIdxLock.Lock()
	mgr.nextBackendIdx++
	bkList := make([]*backend, len(mgr.backends))
	for i := 0; i < len(mgr.backends); i++ {
		bkList[i] = mgr.backends[(mgr.nextBackendIdx+i)%len(mgr.backends)]
	}
	mgr.nxtIdxLock.Unlock()
	return bkList
}

// DialUpstream - Creates connection to an backend server and returns the object
func (mgr *Manager) DialUpstream(dialContext *common.BackendDialContext) (common.BackendConnection, error) {
	nxtBkList := mgr.nextBackendList()
	var con *outc
	var err error
	var bk *backend
	for _, bk = range nxtBkList {
		con, err = bk.DialUpstream(dialContext)
		if con != nil {
			break
		}
		if _, declined := err.(*common.ConnectionDeclinedError); declined {
			return nil, err
		}
	}

	if con == nil {
		return nil, common.NewSystemUnavailableError("system", "All backend exhausted!")
	}

	return con, nil
}

// parseDuration parse given string as Duration and return it's value.
// If parsing is not successful or string is blank, returns
// fallback value
func (mgr *Manager) parseDuration(str string, fallback time.Duration) time.Duration {
	if str == "" {
		return fallback
	}
	pv, err := time.ParseDuration(str)
	if err == nil {
		return pv
	}
	mlog.Errorf("Unable to convert %v to duration. %v", str, err)
	return fallback
}
