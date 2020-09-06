package internal_test

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/apundir/wsbalancer/cmd"
	"github.com/apundir/wsbalancer/in"
	"github.com/apundir/wsbalancer/out"
	"github.com/gorilla/websocket"
	"github.com/ilyakaznacheev/cleanenv"
)

var (
	portRe, _        = regexp.Compile("^(.+?):(\\d+)(.*)$")
	nextPortDelta    = 0
	nextPortDeltaMax = 1000
	nextPortLock     = &sync.Mutex{}
)

// SvrBuilder function to have balancer HTTP server instances created.
// This is needed to avoid circular import dependency which is not allowed
// in go.
type SvrBuilder func(*in.ServerConfig, *in.Manager) *http.Server

// TestConfig configuration for test clients and server for integration tests
type TestConfig struct {
	Server            in.ServerConfig      `yaml:"server"`
	Upstream          out.UpstreamConfig   `yaml:"upstream"`
	Clients           []*TestClientConfig  `yaml:"test-clients"`
	Backends          []*TestBackendConfig `yaml:"test-backends"`
	ValidateTestLeaks bool                 `yaml:"validate-test-leaks"`
	InManager         *in.Manager
	OutManager        *out.Manager
	FeServer          *http.Server
	AdmSerer          *http.Server
	configFile        string
	RunParallel       bool
	InitialRoutines   int // # of goroutines running before *ANY* test case execution is started
}

// TestClientConfig for integration tests
type TestClientConfig struct {
	Endpoint         string        `yaml:"endpoint"`
	Subprotocol      []string      `yaml:"subprotocol"`
	BufferSize       int           `yaml:"buffer-size"`
	HandshakeTimeout time.Duration `yaml:"handshake-timeout"`
	Connections      *TestClientConnections
}

// TestBackendConfig for integration tests
type TestBackendConfig struct {
	Addr               string   `yaml:"addr"`
	Subprotocol        []string `yaml:"subprotocol"`
	BufferSize         int      `yaml:"buffer-size"`
	IgnoreLastTrackers bool     `yaml:"ignore-last-trackers"`
	Server             *TestBackend
}

// TestClientConnections A lock protected map for safer concurrent access
type TestClientConnections struct {
	connections map[*TestClient]bool
	lock        *sync.RWMutex
}

func newTestClientConnections() *TestClientConnections {
	return &TestClientConnections{
		connections: make(map[*TestClient]bool),
		lock:        &sync.RWMutex{},
	}
}

// Add an item to map
func (tcc *TestClientConnections) Add(con *TestClient) {
	tcc.lock.Lock()
	defer tcc.lock.Unlock()
	tcc.connections[con] = true
}

// Delete an item from map
func (tcc *TestClientConnections) Delete(con *TestClient) {
	tcc.lock.Lock()
	defer tcc.lock.Unlock()
	delete(tcc.connections, con)
}

// Len size of the map
func (tcc *TestClientConnections) Len() int {
	tcc.lock.RLock()
	defer tcc.lock.RUnlock()
	return len(tcc.connections)
}

// Contains determines if map contains given item
func (tcc *TestClientConnections) Contains(con *TestClient) bool {
	tcc.lock.RLock()
	defer tcc.lock.RUnlock()
	_, ok := tcc.connections[con]
	return ok
}

// List determines if map contains given item
func (tcc *TestClientConnections) List() []*TestClient {
	tcc.lock.RLock()
	defer tcc.lock.RUnlock()
	lst := make([]*TestClient, len(tcc.connections))
	i := 0
	for con := range tcc.connections {
		lst[i] = con
		i++
	}
	return lst
}

// GetOneBkConfig returns test configuration with one backend
func GetOneBkConfig() *TestConfig {
	return LoadConfig("test-default-config.yml")
}

// GetTwoBkConfig returns test configuration with two backends
func GetTwoBkConfig() *TestConfig {
	return LoadConfig("test-two-bkend-config.yml")
}

// GetFourBkConfig returns test configuration with four backends
func GetFourBkConfig() *TestConfig {
	return LoadConfig("test-four-bkend-config.yml")
}

// LoadConfig Loads given configuration from testdata folder
func LoadConfig(config string) *TestConfig {
	_, filename, _, _ := runtime.Caller(0)
	cfgFile := filepath.Dir(filepath.Dir(filename)) + "/testdata/" + config
	if _, err := os.Stat(cfgFile); err != nil {
		panic(cfgFile + " does not exist\n")
	}
	var cfg TestConfig
	err := cleanenv.ReadConfig(cfgFile, &cfg)
	if err != nil {
		panic(err.Error())
	}
	nextPortLock.Lock()
	_curDelta := nextPortDelta
	nextPortDelta++
	if nextPortDelta >= nextPortDeltaMax {
		nextPortDelta = 0
	}
	nextPortLock.Unlock()
	cfg.incrementPorts(_curDelta)
	blkDuration := time.Duration(0)
	for _, tc := range cfg.Clients {
		tc.Connections = newTestClientConnections()
		if tc.HandshakeTimeout == blkDuration {
			tc.HandshakeTimeout = (4 * time.Second)
		}
	}
	cfg.configFile = config
	return &cfg
}

// ForTest creates a new test configuration IF the tests are
// configured to run in parallel. If parallel execution is NOT
// required than current configuration is returned AS-IS.
func (tc *TestConfig) ForTest() *TestConfig {
	if !tc.RunParallel {
		return tc
	}
	return tc.ForParallelTest()
}

// ForParallelTest creates a new test configuration and configure
// it to run in parallel.
func (tc *TestConfig) ForParallelTest() *TestConfig {
	tCfg := LoadConfig(tc.configFile)
	tCfg.RunParallel = true
	tCfg.InitialRoutines = tc.InitialRoutines
	tCfg.StartAll()
	return tCfg
}

// StopForTest stops the backend and balancer IF the tests are configured to run
// in parallel. If parallel execution is NOT required than this function doesn't
// do anything
func (tc *TestConfig) StopForTest() {
	if !tc.RunParallel {
		return
	}
	tc.StopAll()
}

// incrementPorts increment all ports in configuration by given delta
func (tc *TestConfig) incrementPorts(delta int) {
	tc.Server.FrontendHTTP.Addr = incrPort(tc.Server.FrontendHTTP.Addr, delta)
	tc.Server.AdminHTTP.Addr = incrPort(tc.Server.AdminHTTP.Addr, delta)
	for _, ab := range tc.Upstream.Backends {
		ab.URL = incrPort(ab.URL, delta)
	}
	for _, tc := range tc.Clients {
		tc.Endpoint = incrPort(tc.Endpoint, delta)
	}
	for _, tb := range tc.Backends {
		tb.Addr = incrPort(tb.Addr, delta)
	}
}

func incrPort(inAddr string, delta int) string {
	matches := portRe.FindStringSubmatch(inAddr)
	if len(matches) < 4 {
		panic("Couldn't extract port from url " + inAddr)
	}
	pt, _ := strconv.Atoi(matches[2])
	pt = pt + delta
	return matches[1] + ":" + strconv.Itoa(pt) + matches[3]
}

// ResetStats reset all stats from all test clients and all backend test servers
func (tc *TestConfig) ResetStats() {
	for _, ab := range tc.Backends {
		ab.Server.ResetStats()
	}
	for _, ac := range tc.Clients {
		for _, ak := range ac.Connections.List() {
			ak.ResetStats()
		}
	}
}

// StartAll starts balancer followed by all backends
func (tc *TestConfig) StartAll() {
	for _, bk := range tc.Backends {
		bk.GetServer().Start()
	}
	tc.StartBalancer()
}

// StartBalancer starts the balancer, build frontend and admin http server using
// provided functions. These functions decouples cyclic import dependencies
func (tc *TestConfig) StartBalancer() {
	tc.OutManager, _ = out.NewManager(&tc.Upstream)
	tc.InManager = in.NewManager(&tc.Server, tc.OutManager)
	tc.InManager.Start()
	tc.FeServer = cmd.BuildFrontendServer(&tc.Server, tc.InManager)
	tc.AdmSerer = cmd.BuildAdminServer(&tc.Server, tc.InManager)
	go tc.FeServer.ListenAndServe()
	go tc.AdmSerer.ListenAndServe()
}

// StopBalancer closes frontend and admin http servers
func (tc *TestConfig) StopBalancer() {
	tc.InManager.Stop()
	tc.OutManager.Stop()
	if tc.FeServer != nil {
		tc.FeServer.Close()
		tc.FeServer = nil
	}
	if tc.AdmSerer != nil {
		tc.AdmSerer.Close()
		tc.AdmSerer = nil
	}
}

// StopAll terminate all connections and stop all running server
func (tc *TestConfig) StopAll() {
	for _, ct := range tc.Clients {
		ct.GetClient().ResetStats()
		ct.CloseAll()
	}
	for _, sv := range tc.Backends {
		if sv.Server == nil {
			continue
		}
		sv.Server.ResetStats()
		sv.Server.CloseAllConnections()
		sv.Server.HTTPServer.Close()
	}
	tc.StopBalancer()
}

// GetLatestBackend returns the backend test server which accepted
// the latest connection successfully from balancer.
// In case none of the backend server have accepted any connection
// yet than this method will return first backend server
func (tc *TestConfig) GetLatestBackend() *TestBackend {
	lca := time.Time{}
	var lc *TestBackend = tc.Backends[0].Server
	if len(tc.Backends) < 2 {
		return lc // single backend
	}
	for _, bk := range tc.Backends {
		if lca.Before(bk.Server.LastConnectedAt()) {
			lca = bk.Server.LastConnectedAt()
			lc = bk.Server
		}
	}
	return lc
}

// GetServer returns properly initialized backend server.
// the backend server instance is cached thus repetitive
// calls to this API will return the same server
func (cfg *TestBackendConfig) GetServer() *TestBackend {
	if cfg.Server != nil {
		return cfg.Server
	}
	cfg.Server = newTestBackend(cfg)
	return cfg.Server
}

func (cfg *TestClientConfig) registerClients(clients ...*TestClient) {
	if cfg.Connections == nil {
		cfg.Connections = newTestClientConnections()
	}
	for _, ac := range clients {
		cfg.Connections.Add(ac)
	}
}

// GetClient returns a new client for this config
func (cfg *TestClientConfig) GetClient() *TestClient {
	client := newTestClient(cfg)
	cfg.registerClients(client)
	return client
}

// Close closes given client
func (cfg *TestClientConfig) Close(client *TestClient) error {
	return cfg.CloseWithCode(client, websocket.CloseNormalClosure)
}

// CloseWithCode closes given client with given code
func (cfg *TestClientConfig) CloseWithCode(client *TestClient, closeCode int) error {
	if client == nil {
		return nil
	}
	cfg.Connections.Delete(client)
	return client.closeWithCode(closeCode)
}

// CloseAll closes all client with normal closure code
func (cfg *TestClientConfig) CloseAll() {
	cfg.CloseAllWithCode(websocket.CloseNormalClosure)
}

// CloseAllWithCode closes all clients with given code
func (cfg *TestClientConfig) CloseAllWithCode(closeCode int) {
	for _, ac := range cfg.Connections.List() {
		cfg.CloseWithCode(ac, closeCode)
	}
}

// GetConnected creates a new connection with balancer and returns the connected client.
// Reports Fatal or Error if connection state is NOT as per expectations.
func (cfg *TestClientConfig) GetConnected(t *testing.T,
	headers http.Header,
	expectFailure bool,
	expectedStatusCode int) *TestClient {
	ac := cfg.GetClient()
	var resp *http.Response
	var err error
	if headers != nil {
		resp, err = ac.ConnectWithHeaders(headers)
	} else {
		resp, err = ac.Connect()
	}
	if err == nil {
		if !expectFailure {
			return ac
		}
		t.Errorf("Was expecting connection failure but got successful "+
			"connection to %v with status code %v", cfg.Endpoint, resp.StatusCode)
		cfg.Close(ac)
		return nil
	}
	if err != nil {
		cfg.Close(ac)
		if expectFailure {
			if expectedStatusCode > 0 {
				if resp.StatusCode != expectedStatusCode {
					t.Errorf("connection failure expected code %v, found %v",
						expectedStatusCode, resp.StatusCode)
				}
			}
			return nil
		}
		t.Fatal("Failed to connect to balancer ", err, resp)
	}
	return nil
}
