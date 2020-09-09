package main

import (
	"flag"
	"net/http"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/apundir/wsbalancer/cmd"
	it "github.com/apundir/wsbalancer/internal"
	"github.com/apundir/wsbalancer/out"
	"github.com/gorilla/websocket"
)

// main package contains all functional integration tests. All tests are
// conducted with fully functional system using proper websocket backend servers
// and websocket clients.

var flagTestLogLevel = flag.String("loglevel", "panic", "logging level to kep for test")
var flagTestParallel = flag.Bool("parallel", false, "execute tests in parallel, defaults to serial execution")
var flagTestValidateLeaks = flag.Bool("check-for-leaks", true, "check for goroutine leaks after every test, defaults to true")

type functionalTest struct {
	Name string
	TC   func(*testing.T, *it.TestConfig)
}

func (ft *functionalTest) run(t *testing.T, inConfig *it.TestConfig) {
	config := inConfig.ForTest()
	t.Run(ft.Name, func(t *testing.T) {
		initGoRoutines := runtime.NumGoroutine()
		if config.RunParallel {
			t.Parallel()
		}
		ft.TC(t, config)
		config.StopForTest()
		if !config.RunParallel {
			for _, cn := range inConfig.Clients {
				cn.CloseAll()
			}
			for _, sv := range inConfig.Backends {
				sv.Server.CloseAllConnections()
				sv.Server.ResetStats()
			}
		}
		ft.validateLeaks(t, config, initGoRoutines)
	})
}

func (ft *functionalTest) validateLeaks(t *testing.T, config *it.TestConfig, initGoRoutines int) {
	if config.RunParallel {
		return // leak validation not supported for parallel tests
	}
	if !config.ValidateTestLeaks {
		return
	}
	curRoutines := runtime.NumGoroutine()
	for i := 0; i < 100; i++ {
		if curRoutines <= initGoRoutines {
			break
		}
		time.Sleep(10 * time.Millisecond)
		curRoutines = runtime.NumGoroutine()
	}
	if curRoutines > initGoRoutines {
		t.Errorf("# of go routines(%v) after tests are more than initial routines count(%v)",
			curRoutines, initGoRoutines)
	}
	sl := it.GetAdminSessionList(t, config)
	for i := 0; i < 10; i++ {
		if len(sl) < 1 {
			break
		}
		time.Sleep(100 * time.Millisecond)
		sl = it.GetAdminSessionList(t, config)
	}
	if len(sl) > 0 {
		t.Errorf("%v sessions still active on server after tests", len(sl))
	}
	for _, acc := range config.Clients {
		if acc.Connections.Len() > 0 {
			t.Errorf("%v TestClients not cleaned up even after tests", acc.Connections.Len())
		}
	}
	for _, sc := range config.Backends {
		if sc.Server.Connections.Len() > 0 {
			t.Errorf("%v client connections active on test server even after tests",
				sc.Server.Connections.Len())
		}
	}
}

func Test(t *testing.T) {
	cmd.SetLogLevel(*flagTestLogLevel)
	testWithConfig(t, "basics", it.GetOneBkConfig)
	testWithConfig(t, "2bk", it.GetTwoBkConfig)
	testWithConfig(t, "4bk", it.GetFourBkConfig)
}

func testWithConfig(t *testing.T, name string, configBuilder func() *it.TestConfig) {
	config := configBuilder()
	config.ValidateTestLeaks = *flagTestValidateLeaks
	if *flagTestParallel {
		config.RunParallel = true
	} else {
		config.StartAll()
		defer config.StopAll()
	}
	config.InitialRoutines = runtime.NumGoroutine()
	t.Run(name, func(t *testing.T) {
		BasicsTest(t, config)
	})
}

func BasicsTest(t *testing.T, inConfig *it.TestConfig) {
	config := inConfig
	tests := []functionalTest{
		{"hello", helloTest},
		{"non-websocket-request", nonWsRequest},
		{"ping", pingTest},
		{"server-ping", serverPingTest},
		{"header", headerTest},
		{"reconnect", reconnectTest},
		{"disconnect-normal-closure", closeClientOnRemoteNormalClosureTest},
		{"be-reject", beRejectTest},
		{"admin", adminTests},
		{"breaker", breakerTests},
	}
	for _, tc := range tests {
		tc := tc
		tc.run(t, config)
	}
}

func helloTest(t *testing.T, config *it.TestConfig) {
	ac := config.Clients[0].GetConnected(t, nil, false, 0)
	defer config.Clients[0].Close(ac)
	dt := string(ac.SendRecvTestMessage(t, nil, false, false))
	t.Logf("Received %v", dt)
	lc := config.GetLatestBackend().LastReceived()
	if lc == nil {
		t.Error("Server does not have any record of the message sent")
		return
	}
	if lc.MessageType != websocket.TextMessage || string(lc.Data) != dt {
		t.Errorf("Unexpected data at backend, received type %v, data %v",
			lc.MessageType, string(lc.Data))
	}
}

// pingTest tests for ping/pong between the client and the balancer
func pingTest(t *testing.T, config *it.TestConfig) {
	ac := config.Clients[0].GetConnected(t, nil, false, 0)
	defer config.Clients[0].Close(ac)
	pingedAt := time.Now()
	ac.Ping("")
	// give a ond to ensure pong is AFTER ping
	time.Sleep(1 * time.Millisecond)
	// reading is necessary for pong to be processed
	ac.SendRecvTestMessage(t, nil, false, false)
	pong := ac.LastPongAt
	if pingedAt.Before(pong) {
		return
	}
	t.Errorf("ping failed, pinged at %v, received pong %v", pingedAt, pong)
}

// serverPingTest tests for ping/pong b/w balancer and the backend servers
func serverPingTest(t *testing.T, config *it.TestConfig) {
	newPingTime := time.Second
	origPt := make(map[*out.BackendConfig]time.Duration, len(config.Upstream.Backends))
	for _, ab := range config.Upstream.Backends {
		origPt[ab] = ab.PingTime
		ab.PingTime = newPingTime
	}
	defer func() {
		for c, t := range origPt {
			c.PingTime = t
		}
	}()
	sTime := time.Now()
	// establish as many connections as there are backend servers
	// the connections will be balanced across all backend servers
	allClients := make([]*it.TestClient, len(origPt))
	for i := range allClients {
		allClients[i] = config.Clients[0].GetConnected(t, nil, false, 0)
		if allClients[i] == nil {
			return // already failed
		}
	}
	time.Sleep(200 * time.Millisecond)
	for i := 0; i < 2; i++ {
		time.Sleep(newPingTime)
		var lp time.Time
		for _, bk := range config.Backends {
			lp = bk.Server.LastPingAt()
			if !sTime.Before(lp) {
				t.Errorf("Ping was expected AFTER %v, last ping was at %v", sTime, lp)
				return
			}
		}
		sTime = lp
	}
}

func closeClientOnRemoteNormalClosureTest(t *testing.T, config *it.TestConfig) {
	clients := make([]*it.TestClient, 0)
	for range config.Backends {
		ac := config.Clients[0].GetConnected(t, nil, false, 0)
		ac.SendRecvTestMessage(t, nil, false, false)
		clients = append(clients, ac)
	}
	if t.Failed() {
		return
	}
	// close all connection with normal closure, it should result
	// in balancer closing the client connection as well
	for _, bk := range config.Backends {
		bk.Server.CloseAllConnections()
	}
	for _, ac := range clients {
		ac.SendRecvTestMessage(t, "ACC", true, false)
	}
}

func reconnectTest(t *testing.T, config *it.TestConfig) {
	clients := make([]*it.TestClient, 0)
	hdrs := http.Header{}
	hdrs.Add(config.Server.ReconnectHeader, strconv.Itoa(1))
	for range config.Backends {
		ac := config.Clients[0].GetClient()
		ac.ConnectWithHeaders(hdrs)
		ac.SendRecvTestMessage(t, nil, false, false)
		clients = append(clients, ac)
	}
	if t.Failed() {
		return
	}
	// verify ReconnectHeader is suppressed by balancer and it did not reach any of the backend
	for _, bk := range config.Backends {
		rh := bk.Server.LastHeader().Get(config.Server.ReconnectHeader)
		if rh != "" {
			t.Errorf("Client is able to spoof balancer with %v header", config.Server.ReconnectHeader)
			return
		}
	}
	// this should trigger reconnection
	// t.Log("Closing all connections on test backend servers")
	for _, bk := range config.Backends {
		bk.Server.CloseAllConnectionsWithCode(websocket.CloseInternalServerErr)
	}
	// Give time to balancer to recognize connection termination
	time.Sleep(200 * time.Millisecond)
	// t.Log("Attempting to send and receive another message on existing channel")
	for _, ac := range clients {
		ac.SendRecvTestMessage(t, nil, false, false)
	}
	// verify that we got reconnection header on server side
	for _, bk := range config.Backends {
		rh := bk.Server.LastHeader().Get(config.Server.ReconnectHeader)
		if rh != "1" {
			t.Errorf("Expected header %v not found with expected value %v, received val: %v",
				config.Server.ReconnectHeader, "1", rh)
		}
	}
}

func beRejectTest(t *testing.T, config *it.TestConfig) {
	tdata := make(map[int]int)
	tdata[300] = 300
	tdata[303] = 303
	tdata[400] = 400
	tdata[401] = 401
	tdata[402] = 402
	tdata[403] = 403
	tdata[500] = 502
	tdata[501] = 502
	tdata[502] = 502
	tdata[503] = 502
	tests := make([]functionalTest, 0, len(tdata))
	for send, expect := range tdata {
		tests = append(tests, functionalTest{strconv.Itoa(send), func(t *testing.T, config *it.TestConfig) {
			beRejectTestWithCode(t, config, send, expect)
		}})
	}
	for _, tc := range tests {
		tc := tc
		tc.run(t, config)
	}
}

func beRejectTestWithCode(t *testing.T, config *it.TestConfig,
	codeToSend int, codeToExpect int) {
	origAcceptor := make(map[*it.TestBackend]func(http.ResponseWriter, *http.Request))
	for _, bk := range config.Backends {
		origAcceptor[bk.Server] = bk.Server.CustomAcceptor
		bk.Server.CustomAcceptor = func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, http.StatusText(codeToSend), codeToSend)
		}
	}
	defer func() {
		for bk, fn := range origAcceptor {
			bk.CustomAcceptor = fn
		}
	}()
	config.Clients[0].GetConnected(t, nil, true, codeToExpect)
}

func nonWsRequest(t *testing.T, config *it.TestConfig) {
	var hClient = &http.Client{}
	defer hClient.CloseIdleConnections()
	ep := "http://" + config.Server.FrontendHTTP.Addr
	resp, err := hClient.Get(ep)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
		return
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("Was expecting status code %v, found %v", http.StatusBadRequest, resp.StatusCode)
	}
	resp.Body.Close()
}
