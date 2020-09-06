package main

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/apundir/wsbalancer/common"
	it "github.com/apundir/wsbalancer/internal"
)

// headerTest tests balancer for scenarios related to headers in
// request and responses.
func headerTest(t *testing.T, config *it.TestConfig) {
	tests := []functionalTest{
		{"pass", headerPassTest},
		{"ignore", ignoreHeaderTest},
		{"subproto", subprotocolTest},
		{"xff", xForwardedForTest},
	}
	for _, tc := range tests {
		tc := tc
		tc.run(t, config)
	}
}

// headerTest sends a header from client side and expects the header
// to reach backend server AS-IS. In addition, it also verifies that
// a header sent from backend server reaches client AS-IS.
func headerPassTest(t *testing.T, config *it.TestConfig) {
	origHeaders := make(map[*it.TestBackend]http.Header)
	svrPassHdr := config.Upstream.Backends[0].PassHeaders[0]
	origUsHdrs := config.Upstream.PassHeaders
	config.Upstream.PassHeaders = []string{svrPassHdr}
	for _, bk := range config.Backends {
		origHeaders[bk.Server] = bk.Server.ResponseHeader
		bk.Server.ResponseHeader = http.Header{}
		bk.Server.ResponseHeader.Add(svrPassHdr, time.Now().String())
	}
	defer func() {
		config.Upstream.PassHeaders = origUsHdrs
		for bk, hd := range origHeaders {
			bk.ResponseHeader = hd
		}
	}()
	testHdr := "X-Test-Header"
	clients := make([]*it.TestClient, 0)
	for range config.Backends {
		hdrs := http.Header{}
		hVal := time.Now().String()
		hdrs.Add(testHdr, hVal)
		ac := config.Clients[0].GetClient()
		resp, _ := ac.ConnectWithHeaders(hdrs)
		clients = append(clients, ac)
		if resp.Header.Get(svrPassHdr) != config.GetLatestBackend().ResponseHeader.Get(svrPassHdr) {
			t.Errorf("expected %v header with value %v, found %v", svrPassHdr,
				config.GetLatestBackend().ResponseHeader.Get(svrPassHdr), resp.Header.Get(svrPassHdr))
		}
		rVal := config.GetLatestBackend().LastHeader().Get(testHdr)
		if rVal != hVal {
			t.Errorf("%v Failed, expected %v, found %v", t.Name(), hVal, rVal)
		}
	}
}

// ignoreHeaderTest sends a header which is configured to be ignored by the
// balancer. This is expected that the header does not reach the backend.
func ignoreHeaderTest(t *testing.T, config *it.TestConfig) {
	hdrs := http.Header{}
	hVal := time.Now().String()
	hdrs.Add(config.Server.IgnoreHeaders[0], hVal)
	clients := make([]*it.TestClient, 0)
	for range config.Backends {
		ac := config.Clients[0].GetConnected(t, hdrs, false, 0)
		clients = append(clients, ac)
	}
	if t.Failed() {
		return
	}
	for _, bk := range config.Backends {
		rVal := bk.Server.LastHeader().Get(config.Server.IgnoreHeaders[0])
		if rVal != "" {
			t.Errorf("Expected blank, found %v", rVal)
		}
	}
}

// subprotocolTest sends different sub-protocol(s) in request and expects server
// to respond in accordance with what's responded by the backend server
func subprotocolTest(t *testing.T, config *it.TestConfig) {
	name := []string{"none", "nonmatch", "first", "second", "multi"}
	protoSent := [][]string{{}, {"clientp0"}, {"server-sub-one"},
		{"server-sub-two"}, {"clientp0", "server-sub-two"}}
	expectedProto := []string{"", "", "server-sub-one", "server-sub-two", "server-sub-two"}
	tests := make([]functionalTest, len(name))
	for i, nm := range name {
		tests[i] = functionalTest{nm, func(t *testing.T, config *it.TestConfig) {
			subprotocolTestWith(t, config, protoSent[i], expectedProto[i])
		}}
	}
	for _, tc := range tests {
		tc := tc
		tc.run(t, config)
	}
}

// subprotocolTestWith sends one combination of subprotocol(s) in request
// and expects given protocol as the negotiated protocol.
func subprotocolTestWith(t *testing.T, config *it.TestConfig,
	sentProtocol []string, expectedProtocol string) {
	clients := make([]*it.TestClient, 0)
	for range config.Backends {
		ac := config.Clients[0].GetClient()
		ac.Dialer.Subprotocols = sentProtocol
		ac.Connect()
		if t.Failed() {
			return
		}
		rVal := ac.Subprotocol()
		if rVal != expectedProtocol {
			t.Errorf("Failed, expected \"%v\", found \"%v\"", expectedProtocol, rVal)
		}
		clients = append(clients, ac)
	}

	for _, bk := range config.Backends {
		sProto := bk.Server.LastConnection().Subprotocol()
		if sProto != expectedProtocol {
			t.Errorf("Failed on B/E, expected \"%v\", found \"%v\"", expectedProtocol, sProto)
		}
		sRecvVal := bk.Server.LastHeader().Values(common.SubprotocolHeader)
		if len(sentProtocol) < 1 && len(sRecvVal) < 1 {
			return
		}
		sentAsStr := strings.Join(sentProtocol, ", ")
		sRecvAsStr := strings.Join(sRecvVal, ", ")
		if sRecvAsStr != sentAsStr {
			t.Errorf("Failed, expected on server \"%v\", found \"%v\"", sentProtocol, sRecvVal)
		}
	}
}

// xForwardedForTest verifies that the server adds it's own X-Forwarded-For header in
// outgoing request to the backend server. IF an existing header is present than the
// existing header value is appended with the current connection ip (which will be
// always be localhost in this test)
func xForwardedForTest(t *testing.T, config *it.TestConfig) {
	hdrs := http.Header{}
	ip := "1.2.3.4"
	hdrs.Add(common.ForwardedForHeader, ip)
	origTxff := config.Server.TrustReceivedXff
	defer func() { config.Server.TrustReceivedXff = origTxff }()
	// with TrustReceivedXff turned on first
	config.Server.TrustReceivedXff = true
	for range config.Backends {
		config.Clients[0].GetConnected(t, hdrs, false, 0)
	}
	for _, bk := range config.Backends {
		rXff := bk.Server.LastHeader().Get(common.ForwardedForHeader)
		if !strings.HasPrefix(rXff, ip) {
			t.Errorf("Expected forwarded ip %v not found in current value of %v: %v",
				ip, common.ForwardedForHeader, rXff)
		}
		// verify that localhost ip is present in xff
		if !strings.HasSuffix(rXff, "127.0.0.1") && !strings.HasSuffix(rXff, "[::1]") {
			t.Errorf("Expected localhost IP not found in current value of %v: %v",
				common.ForwardedForHeader, rXff)
		}
	}
	// with TrustReceivedXff turned off
	config.Server.TrustReceivedXff = false
	for range config.Backends {
		config.Clients[0].GetConnected(t, hdrs, false, 0)
	}
	for _, bk := range config.Backends {
		rXff := bk.Server.LastHeader().Get(common.ForwardedForHeader)
		if strings.HasPrefix(rXff, ip) {
			t.Errorf("Forwarded ip %v not expected but found in current value of %v: %v",
				ip, common.ForwardedForHeader, rXff)
		}
		if rXff != "127.0.0.1" && rXff != "[::1]" {
			t.Errorf("%v is NOT equal localhost IP, received value: %v",
				common.ForwardedForHeader, rXff)
		}
	}
}
