package main

import (
	"net/http"
	"reflect"
	"testing"
	"time"

	it "github.com/apundir/wsbalancer/internal"
)

func adminTests(t *testing.T, config *it.TestConfig) {
	tests := []functionalTest{
		{"backend-list", backendListTest},
		{"backend-disable-enable-all", backendDisableEnableAllTest},
		{"backend-failover", backendFailoverTest},
		{"backend-delete-add-one", backendDeleteAddOneTest},
		{"backend-disable-delete-one", backendDisableDeleteOneTest},
		{"session-list", sessionListTest},
		{"session-abort", sessionAbortTest},
		{"session-failover", sessionFailoverTest},
		{"frontend-pause-resume", frontendPauseResumeTest},
		{"invalid-requests", adminInvalidRequestsTest},
	}
	for _, tc := range tests {
		tc := tc
		tc.run(t, config)
	}
}

func adminInvalidRequestsTest(t *testing.T, config *it.TestConfig) {
	getRequests := make(map[string]string)
	getRequests["backend-enable"] = "/backend/enable/"
	getRequests["session-failover"] = "/session/failover/"
	getRequests["frontend-pause"] = "/frontend/pause/"
	getRequests["frontend-resume"] = "/frontend/resume/"
	for name, ep := range getRequests {
		resp := it.GetAdminActionRequest(t, "http://"+config.Server.AdminHTTP.Addr+ep+"^^^^^")
		if !resp.Failed() {
			t.Errorf("wrong id in %v request did NOT result in failure, response: %v", name, resp)
		}
	}
	resp := it.PostAdminActionRequest(t,
		"http://"+config.Server.AdminHTTP.Addr+"/backend/add", []byte("<>"))
	if !resp.Failed() {
		t.Errorf("invalid data for backend add request did NOT result in failure, response: %v", resp)
	}
}

func frontendPauseResumeTest(t *testing.T, config *it.TestConfig) {
	config.ResetStats()
	ac := config.Clients[0].GetConnected(t, nil, false, 0)
	ac.SendRecvTestMessage(t, nil, false, false)
	if t.Failed() {
		return
	}
	feList := it.GetAdminFrontendList(t, config)
	// pause all frontends
	for _, fe := range feList {
		resp := fe.Pause(t, config)
		if !resp.Successful() {
			t.Fatalf("frontend %v pause failed with response %v", fe.ID, resp)
		}
		resp = fe.Pause(t, config)
		if !resp.NoActionReqd() {
			t.Errorf("frontend %v second pause did NOT result into NoActionRequired, "+
				" response %v", fe.ID, resp)
		}
	}
	// existing session should keep working as is
	ac.SendRecvTestMessage(t, nil, false, false)
	// new connection should should result into error
	config.Clients[0].GetConnected(t, nil, true, http.StatusServiceUnavailable)
	// resume all frontends
	for _, fe := range feList {
		resp := fe.Resume(t, config)
		if !resp.Successful() {
			t.Fatalf("frontend %v resume failed with response %v", fe.ID, resp)
		}
		resp = fe.Resume(t, config)
		if !resp.NoActionReqd() {
			t.Errorf("frontend %v second resume did NOT result into NoActionRequired,"+
				" response %v", fe.ID, resp)
		}
	}
	// existing session should keep working as is
	ac.SendRecvTestMessage(t, nil, false, false)
	// new connection should NOT result into error
	config.Clients[0].GetConnected(t, nil, false, 0)
}

func sessionFailoverTest(t *testing.T, config *it.TestConfig) {
	config.ResetStats()
	ac := config.Clients[0].GetConnected(t, nil, false, 0)
	ac.SendRecvTestMessage(t, nil, false, false)
	if t.Failed() {
		return
	}
	sessions := it.GetAdminSessionList(t, config)
	if len(sessions) != 1 {
		t.Fatalf("was expecting exactly one session active, found %v", len(sessions))
	}
	config.ResetStats()
	resp := sessions[0].Failover(t, config)
	if !resp.Successful() {
		t.Fatalf("Session failover failed, response %v", resp)
	}
	// now writing/reading on existing connection should NOT result into error
	ac.SendRecvTestMessage(t, nil, false, false)
	// verify that we got reconnection header on server side
	reconHdrFound := false
	for _, ab := range config.Backends {
		if ab.Server.LastHeader() == nil {
			continue
		}
		if "1" == ab.Server.LastHeader().Get(config.Server.ReconnectHeader) {
			reconHdrFound = true
			break
		}
	}
	if !reconHdrFound {
		t.Errorf("Expected header %v not found with expected value %v on any backend server",
			config.Server.ReconnectHeader, "1")
	}
}

func sessionListTest(t *testing.T, config *it.TestConfig) {
	config.ResetStats()
	ac1 := config.Clients[0].GetConnected(t, nil, false, 0)
	ac2 := config.Clients[0].GetConnected(t, nil, false, 0)
	ac1.SendRecvTestMessage(t, nil, false, false)
	ac2.SendRecvTestMessage(t, nil, false, false)
	if t.Failed() {
		return
	}
	sessions := it.GetAdminSessionList(t, config)
	if len(sessions) != 2 {
		t.Fatalf("was expecting exactly two active sessions, found %v", len(sessions))
	}
}

func sessionAbortTest(t *testing.T, config *it.TestConfig) {
	config.ResetStats()
	ac := config.Clients[0].GetConnected(t, nil, false, 0)
	ac.SendRecvTestMessage(t, nil, false, false)
	if t.Failed() {
		return
	}
	sessions := it.GetAdminSessionList(t, config)
	if len(sessions) != 1 {
		t.Fatalf("was expecting exactly one session active, found %v", len(sessions))
	}
	resp := sessions[0].Abort(t, config)
	if !resp.Successful() {
		t.Fatalf("Session abort failed, response %v", resp)
	}
	resp = sessions[0].Abort(t, config)
	if !resp.NoActionReqd() {
		t.Errorf("Aborted session abort request didn't result into NoActionRequired response, response %v", resp)
	}
	// now reading should result into error
	ac.SendRecvTestMessage(t, nil, true, false)
	// new connection should still go through and work fine
	ac = config.Clients[0].GetConnected(t, nil, false, 0)
	ac.SendRecvTestMessage(t, nil, false, false)
}

func backendDeleteAddOneTest(t *testing.T, config *it.TestConfig) {
	config.ResetStats()
	bkListBeforeConnect := it.GetAdminBackendListAsMap(t, config)
	ac := config.Clients[0].GetConnected(t, nil, false, 0)
	ac.SendRecvTestMessage(t, nil, false, false)
	if t.Failed() {
		return
	}
	bkListAfterConnect := it.GetAdminBackendListAsMap(t, config)
	connectedBkID := ""
	for bk := range bkListAfterConnect {
		if bkListAfterConnect[bk].TotalConnections > bkListBeforeConnect[bk].TotalConnections {
			connectedBkID = bk
			break
		}
	}
	if connectedBkID == "" {
		t.Fatal("unable to find connected backend")
	}
	connectedBk := bkListAfterConnect[connectedBkID]
	origConfig := connectedBk.Config(t, config)
	delResp := connectedBk.Delete(t, config)
	if !delResp.Successful() {
		t.Fatalf("Unable to delete backend %v", connectedBkID)
	}
	delResp = connectedBk.Delete(t, config)
	if !delResp.NoActionReqd() {
		t.Errorf("Expected NoActionRequired not found in %v for backend %v in delete request",
			delResp, connectedBkID)
	}
	afterDelete := it.GetAdminBackendListAsMap(t, config)
	if len(afterDelete) != len(bkListAfterConnect)-1 {
		t.Fatalf("delete backend %v but backend list still has all backends", connectedBkID)
	}
	isSingleBK := len(bkListAfterConnect) == 1
	if isSingleBK {
		// wait for session to complete
		sList := it.GetAdminSessionList(t, config)
		for i := 0; i < 10; i++ {
			if len(sList) < 1 {
				break
			}
			time.Sleep(100 * time.Millisecond)
			sList = it.GetAdminSessionList(t, config)
		}
		if len(sList) > 0 {
			t.Errorf("%v sessions still active after deleting backend %v", len(sList), connectedBkID)
		}
		// there was only single backend, our current connection should have been terminated
		ac.SendRecvTestMessage(t, nil, true, false)
		ac = config.Clients[0].GetConnected(t, nil, true, http.StatusBadGateway)
	} else {
		// we have multiple backends, our existing connection should
		// have failed over to new backend and work AS-IS and be fully functional
		ac.SendRecvTestMessage(t, nil, false, false)
	}
	// add deleted backend again
	addResp := connectedBk.Add(t, config, origConfig)
	if !addResp.Successful() {
		t.Fatalf("Unable to add backend %v with data %v, response: %v",
			connectedBkID, connectedBk, addResp)
	}
	if isSingleBK {
		// need to initiate new connection still old would have been closed
		ac = config.Clients[0].GetConnected(t, nil, false, 0)
	}
	ac.SendRecvTestMessage(t, nil, false, false)
	afterAddingBK := it.GetAdminBackendListAsMap(t, config)
	if len(afterAddingBK) != len(bkListAfterConnect) {
		t.Errorf("count of backend not matching, before delete %v, after adding %v",
			len(bkListAfterConnect), len(afterAddingBK))
	}
	// verify that all the parameters are as they should be
	bc := connectedBk.Config(t, config)
	if !reflect.DeepEqual(origConfig, bc) {
		t.Errorf("Configuration mismatch for new backend. Expected: %v, found: %v", origConfig, bc)
	}
}

func backendDisableDeleteOneTest(t *testing.T, config *it.TestConfig) {
	config.ResetStats()
	bkListBeforeConnect := it.GetAdminBackendListAsMap(t, config)
	ac := config.Clients[0].GetConnected(t, nil, false, 0)
	ac.SendRecvTestMessage(t, nil, false, false)
	if t.Failed() {
		return
	}
	bkListAfterConnect := it.GetAdminBackendListAsMap(t, config)
	connectedBkID := ""
	for bk := range bkListAfterConnect {
		if bkListAfterConnect[bk].TotalConnections > bkListBeforeConnect[bk].TotalConnections {
			connectedBkID = bk
			break
		}
	}
	if connectedBkID == "" {
		t.Fatal("unable to find connected backend")
	}
	connectedBk := bkListAfterConnect[connectedBkID]
	origConfig := connectedBk.Config(t, config)
	// disable this backend before delete
	disResp := connectedBk.Disable(t, config)
	if !disResp.Successful() {
		t.Fatalf("Unable to DISABLE backend %v", connectedBkID)
	}
	delResp := connectedBk.Delete(t, config)
	if !delResp.Successful() {
		t.Fatalf("Unable to DELETE backend %v", connectedBkID)
	}
	delResp = connectedBk.Delete(t, config)
	if !delResp.NoActionReqd() {
		t.Errorf("Expected NoActionRequired not found in %v for backend %v in delete request",
			delResp, connectedBkID)
	}
	afterDelete := it.GetAdminBackendListAsMap(t, config)
	if len(afterDelete) != len(bkListAfterConnect)-1 {
		t.Fatalf("delete backend %v but backend list still has all backends", connectedBkID)
	}
	isSingleBK := len(bkListAfterConnect) == 1
	if isSingleBK {
		// wait for session to complete
		sList := it.GetAdminSessionList(t, config)
		for i := 0; i < 10; i++ {
			if len(sList) < 1 {
				break
			}
			time.Sleep(100 * time.Millisecond)
			sList = it.GetAdminSessionList(t, config)
		}
		if len(sList) > 0 {
			t.Errorf("%v sessions still active after deleting backend %v", len(sList), connectedBkID)
		}
		// there was only single backend, our current connection should have been terminated
		ac.SendRecvTestMessage(t, nil, true, false)
		ac = config.Clients[0].GetConnected(t, nil, true, http.StatusBadGateway)
	} else {
		// we have multiple backends, our existing connection should
		// have failed over to new backend and work AS-IS and be fully functional
		ac.SendRecvTestMessage(t, nil, false, false)
	}
	// add deleted backend again
	addResp := connectedBk.Add(t, config, origConfig)
	if !addResp.Successful() {
		t.Fatalf("Unable to add backend %v with data %v, response: %v",
			connectedBkID, connectedBk, addResp)
	}
	if isSingleBK {
		// need to initiate new connection since old would have been closed
		ac = config.Clients[0].GetConnected(t, nil, false, 0)
	}
	ac.SendRecvTestMessage(t, nil, false, false)
	afterAddingBK := it.GetAdminBackendListAsMap(t, config)
	if len(afterAddingBK) != len(bkListAfterConnect) {
		t.Errorf("count of backend not matching, before delete %v, after adding %v",
			len(bkListAfterConnect), len(afterAddingBK))
	}
	// verify that all the parameters are as per original configuration
	bc := connectedBk.Config(t, config)
	if !reflect.DeepEqual(origConfig, bc) {
		t.Errorf("Configuration mismatch for new backend. Expected: %v, found: %v", origConfig, bc)
	}
}

func backendFailoverTest(t *testing.T, config *it.TestConfig) {
	// ensure backend status are in baseline state
	for _, ab := range config.Backends {
		ab.Server.ResetStats()
	}
	ac := config.Clients[0].GetConnected(t, nil, false, 0)
	defer config.Clients[0].Close(ac)
	ac.SendRecvTestMessage(t, "Message before backend failover", false, false)
	bList := it.GetAdminBackendList(t, config)
	// this should trigger reconnection
	for _, ab := range bList {
		resp := ab.Failover(t, config)
		if !resp.Successful() {
			t.Fatalf("Unable to failover backend %v", ab.ID)
		}
	}
	ac.SendRecvTestMessage(t, "Message after backend failover", false, false)
	// verify that we got reconnection header on server side
	reconHdrFound := false
	for _, ab := range config.Backends {
		if ab.Server.LastHeader() == nil {
			continue
		}
		if "1" == ab.Server.LastHeader().Get(config.Server.ReconnectHeader) {
			reconHdrFound = true
			break
		}
	}
	if !reconHdrFound {
		t.Errorf("Expected header %v not found with expected value %v on any backend server",
			config.Server.ReconnectHeader, "1")
	}
}

func backendDisableEnableAllTest(t *testing.T, config *it.TestConfig) {
	ac := config.Clients[0].GetConnected(t, nil, false, 0)
	ac.SendRecvTestMessage(t, nil, false, false)
	bList := it.GetAdminBackendList(t, config)
	for _, ab := range bList {
		resp := ab.Disable(t, config)
		if !resp.Successful() {
			t.Fatalf("Unable to disable backend %v", ab.ID)
		}
		resp = ab.Disable(t, config)
		if !resp.NoActionReqd() {
			t.Errorf("Expected NoActionRequired not found in %v for backend %v in disable request",
				resp, ab.ID)
		}
	}
	bAfterDisable := it.GetAdminBackendList(t, config)
	for _, ab := range bAfterDisable {
		if ab.Enabled {
			t.Errorf("Backend %v is still reported as enabled after disabling it ", ab.ID)
		}
	}
	// verify existing connections are working normally
	ac.SendRecvTestMessage(t, "Message before backend failover", false, false)
	// verify that we are NOT able to connect anymore
	config.Clients[0].GetConnected(t, nil, true, http.StatusBadGateway)
	// enable all backend again
	for _, ab := range bList {
		resp := ab.Enable(t, config)
		if !resp.Successful() {
			t.Fatalf("Unable to enable backend %v", ab.ID)
		}
		resp = ab.Enable(t, config)
		if !resp.NoActionReqd() {
			t.Errorf("Expected NoActionRequirednot found in %v for backend %v in enable request",
				resp, ab.ID)
		}
	}
	config.Clients[0].GetConnected(t, nil, false, 0)
}

func backendListTest(t *testing.T, config *it.TestConfig) {
	bList := it.GetAdminBackendList(t, config)
	if len(bList) < len(config.Upstream.Backends) {
		t.Errorf("Expected %v backends, found %v, full list: %v",
			len(config.Upstream.Backends), len(bList), bList)
	}
}
