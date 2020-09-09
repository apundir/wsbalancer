package main

import (
	"net/http"
	"sync"
	"testing"
	"time"

	it "github.com/apundir/wsbalancer/internal"
)

func breakerTests(t *testing.T, config *it.TestConfig) {
	tests := []functionalTest{
		{"disable", breakerDisableTest},
		{"trip-auto-clear", func(t *testing.T, c *it.TestConfig) { breakerTripTestWith(t, c, true) }},
		{"trip-reset", func(t *testing.T, c *it.TestConfig) { breakerTripTestWith(t, c, false) }},
	}
	for _, tc := range tests {
		tc := tc
		tc.run(t, config)
	}
}

func breakerDisableTest(t *testing.T, config *it.TestConfig) {
	// replace all backend with updated configuration which
	// disable the breaker altogether
	bkList := it.GetAdminBackendList(t, config)
	bkCfg := make([]*it.NewBackendData, len(bkList))
	for i, bk := range bkList {
		bkCfg[i] = bk.Config(t, config)
		bk.Delete(t, config)
		cfgCp := *bkCfg[i]
		cfgCp.BreakerThreshold = -1
		bk.Add(t, config, &cfgCp)
	}
	defer func() {
		for i, bk := range bkList {
			bk.Delete(t, config)
			bk.Add(t, config, bkCfg[i])
		}
	}()
	// replace the test server acceptor so that it responds with 500 internal server error
	origAcceptors := make([]func(w http.ResponseWriter, r *http.Request), len(config.Backends))
	for i, atb := range config.Backends {
		origAcceptors[i] = atb.Server.CustomAcceptor
		atb.Server.CustomAcceptor = func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, http.StatusText(http.StatusInternalServerError),
				http.StatusInternalServerError)
		}
	}
	defer func() {
		for i, atb := range config.Backends {
			atb.Server.CustomAcceptor = origAcceptors[i]
		}
	}()
	// each new connection request will be attempted with EACH available
	// backend by the balancer.
	attemptsCnt := 5
	connectWg := &sync.WaitGroup{}
	connectWg.Add(1)
	go func() {
		for i := 0; i < attemptsCnt; i++ {
			config.Clients[0].GetConnected(t, nil, true, http.StatusBadGateway)
		}
		connectWg.Done()
	}()
	// Wait for all clients attempts to complete
	connectWg.Wait()
	// verify all backends are still reported as NOT tripped
	newBkList := it.GetAdminBackendList(t, config)
	for _, bk := range newBkList {
		if bk.TotalFailures != attemptsCnt {
			t.Errorf("Expected %v failures on backend %v, found %v",
				attemptsCnt, bk.ID, bk.TotalFailures)
		}
		if bk.Tripped {
			t.Errorf("Backend %v tripped where it should NOT have been", bk.ID)
		}
	}
	// restore original acceptor so that test backend starts accepting connections normally
	for i, atb := range config.Backends {
		atb.Server.CustomAcceptor = origAcceptors[i]
	}
	connectWg.Add(len(bkList))
	for range bkList {
		go func() {
			for i := 0; i < attemptsCnt; i++ {
				cn := config.Clients[0].GetConnected(t, nil, false, 0)
				cn.SendRecvTestMessage(t, nil, false, false)
				config.Clients[0].Close(cn)
			}
			connectWg.Done()
		}()
	}
	// Wait for all clients attempts to complete
	connectWg.Wait()
}

func breakerTripTestWith(t *testing.T, config *it.TestConfig, useTimeWait bool) {
	// replace all backend with updated configuration which trips the backend
	// faster so as to make sure we are able to test this condition faster.
	bkList := it.GetAdminBackendList(t, config)
	bkCfg := make([]*it.NewBackendData, len(bkList))
	breakerTestThreshold := 2
	for i, bk := range bkList {
		bkCfg[i] = bk.Config(t, config)
		bk.Delete(t, config)
		cfgCp := *bkCfg[i]
		cfgCp.BreakerThreshold = breakerTestThreshold
		bk.Add(t, config, &cfgCp)
	}
	defer func() {
		for i, bk := range bkList {
			bk.Delete(t, config)
			bk.Add(t, config, bkCfg[i])
		}
	}()
	// replace the test server acceptor so that it responds with 500 internal server error
	origAcceptors := make([]func(w http.ResponseWriter, r *http.Request), len(config.Backends))
	for i, atb := range config.Backends {
		origAcceptors[i] = atb.Server.CustomAcceptor
		atb.Server.CustomAcceptor = func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, http.StatusText(http.StatusInternalServerError),
				http.StatusInternalServerError)
		}
	}
	defer func() {
		for i, atb := range config.Backends {
			atb.Server.CustomAcceptor = origAcceptors[i]
		}
	}()
	attemptPerBk := breakerTestThreshold + 1
	connectWg := &sync.WaitGroup{}
	connectWg.Add(len(config.Upstream.Backends))
	for range config.Upstream.Backends {
		go func() {
			for i := 0; i < attemptPerBk; i++ {
				config.Clients[0].GetConnected(t, nil, true, http.StatusBadGateway)
			}
			connectWg.Done()
		}()
	}
	// Wait for all clients attempts to complete
	connectWg.Wait()
	// verify all backends are reported as tripped
	newBkList := it.GetAdminBackendList(t, config)
	for _, bk := range newBkList {
		if bk.TotalFailures < breakerTestThreshold {
			t.Errorf("Expected minimum %v failures on backend %v, found %v",
				breakerTestThreshold, bk.ID, bk.TotalFailures)
		}
		if !bk.Tripped {
			t.Errorf("Backend %v must have tripped but it did NOT", bk.ID)
		}
		if !useTimeWait {
			// reset the backend so that it starts accepting connections normally
			bk.Reset(t, config)
		}
	}
	if useTimeWait {
		// wait for circuit to become ready by itself
		time.Sleep(time.Second)
	}
	// restore original acceptor so that test backend starts accepting connections normally
	for i, atb := range config.Backends {
		atb.Server.CustomAcceptor = origAcceptors[i]
	}
	connectWg.Add(len(config.Upstream.Backends))
	for range config.Upstream.Backends {
		go func() {
			for i := 0; i < attemptPerBk; i++ {
				cn := config.Clients[0].GetConnected(t, nil, false, 0)
				cn.SendRecvTestMessage(t, nil, false, false)
				config.Clients[0].Close(cn)
			}
			connectWg.Done()
		}()
	}
	// Wait for all clients attempts to complete
	connectWg.Wait()
}
