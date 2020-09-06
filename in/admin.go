package in

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/apundir/wsbalancer/common"
)

type admin struct {
	manager      *Manager
	backendAdmin common.BackendAdministrator
}

type backendAction func(string) (int, error)

// registerHTTPHandlers - registers actionable endpoints to supplied mux
func (adm *admin) registerHTTPHandlers(http *http.ServeMux) {
	http.HandleFunc("/backend/list", adm.listBackendHandler)
	http.HandleFunc("/backend/add", adm.addBackendHandler)
	http.HandleFunc("/backend/config/", adm.backendConfigHandler)
	http.HandleFunc("/backend/disable/", adm.disableBackendHandler)
	http.HandleFunc("/backend/enable/", adm.enableBackendHandler)
	http.HandleFunc("/backend/failover/", adm.failoverBackendHandler)
	http.HandleFunc("/backend/reset/", adm.resetBackendHandler)
	http.HandleFunc("/backend/delete/", adm.deleteBackendHandler)
	http.HandleFunc("/session/list", adm.listSessionHandler)
	http.HandleFunc("/session/abort/", adm.abortSessionHandler)
	http.HandleFunc("/session/failover/", adm.failoverSessionHandler)
	http.HandleFunc("/frontend/list", adm.listFrontendHandler)
	http.HandleFunc("/frontend/pause/", adm.pauseFrontendHandler)
	http.HandleFunc("/frontend/resume/", adm.resumeFrontendHandler)
}

func (adm *admin) isHTTPPost(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == "POST" {
		return true
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	fmt.Fprint(w, "{\"result\":\"failed\", \"reason\":\"OnlyPostAllowed\"}")
	return false
}

func (adm *admin) sendNotImplementedResponse(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	fmt.Fprint(w, "{\"result\":\"failed\", \"reason\":\"NotImplemented\"}")
}

// listBackendHandler - list all existing backends
func (adm *admin) listBackendHandler(w http.ResponseWriter, r *http.Request) {
	if adm.backendAdmin == nil {
		adm.sendNotImplementedResponse(w)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, adm.backendAdmin.ListBackends())
}

func processBkActionRequest(w http.ResponseWriter, r *http.Request, actor backendAction) (string, int) {
	bid := beIDRe.FindStringSubmatch(r.URL.RequestURI())[3]
	w.Header().Set("Content-Type", "application/json")
	resp, err := actor(bid)
	if err != nil {
		fmt.Fprint(w, fmt.Sprintf("{\"result\":\"failed\", \"reason\":\"%v\"}", err.Error()))
	}
	if resp == common.ResultSuccess {
		fmt.Fprint(w, "{\"result\":\"success\"}")
	} else if resp == common.ResultNoActionReqd {
		fmt.Fprint(w, "{\"result\":\"success\", \"reason\":\"NoActionRequired\"}")
	} else {
		fmt.Fprint(w, "{\"result\":\"failed\"}")
	}
	return bid, resp
}

// addBackendHandler - adds new backend
func (adm *admin) addBackendHandler(w http.ResponseWriter, r *http.Request) {
	if adm.backendAdmin == nil {
		adm.sendNotImplementedResponse(w)
		return
	}
	if !adm.isHTTPPost(w, r) {
		return
	}
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		alog.Warnf("Error reading body: %v", err)
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	resp := adm.backendAdmin.AddBackend(body)
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, resp)
}

// backendConfigHandler dumps backend configuration
func (adm *admin) backendConfigHandler(w http.ResponseWriter, r *http.Request) {
	if adm.backendAdmin == nil {
		adm.sendNotImplementedResponse(w)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	bid := beIDRe.FindStringSubmatch(r.URL.RequestURI())
	fmt.Fprint(w, adm.backendAdmin.BackendConfig(bid[3]))
}

// disableBackendHandler - Disable given backend
func (adm *admin) disableBackendHandler(w http.ResponseWriter, r *http.Request) {
	if adm.backendAdmin == nil {
		adm.sendNotImplementedResponse(w)
		return
	}
	if !adm.isHTTPPost(w, r) {
		return
	}
	processBkActionRequest(w, r, adm.backendAdmin.DisableBackend)
}

// enableBackendHandler - Enable given backend
func (adm *admin) enableBackendHandler(w http.ResponseWriter, r *http.Request) {
	if adm.backendAdmin == nil {
		adm.sendNotImplementedResponse(w)
		return
	}
	if !adm.isHTTPPost(w, r) {
		return
	}
	processBkActionRequest(w, r, adm.backendAdmin.EnableBackend)
}

func (adm *admin) resetBackendHandler(w http.ResponseWriter, r *http.Request) {
	if adm.backendAdmin == nil {
		adm.sendNotImplementedResponse(w)
		return
	}
	if !adm.isHTTPPost(w, r) {
		return
	}
	processBkActionRequest(w, r, adm.backendAdmin.ResetBackend)
}

// deleteBackendHandler - Remove given backend and close all it's current connections
func (adm *admin) deleteBackendHandler(w http.ResponseWriter, r *http.Request) {
	if adm.backendAdmin == nil {
		adm.sendNotImplementedResponse(w)
		return
	}
	if !adm.isHTTPPost(w, r) {
		return
	}
	bid, res := processBkActionRequest(w, r, adm.backendAdmin.DeleteBackend)
	if bid != "" && res == common.ResultSuccess {
		go adm.manager.abortAllBackendConnections(bid)
	}
}

// failoverBackendHandler closes all backend connection thus forcing failover for
// all the sessions communicating with this backend right now
func (adm *admin) failoverBackendHandler(w http.ResponseWriter, r *http.Request) {
	if !adm.isHTTPPost(w, r) {
		return
	}
	processBkActionRequest(w, r, adm.manager.abortAllBackendConnections)
}

func (adm *admin) listSessionHandler(w http.ResponseWriter, r *http.Request) {
	adm.manager.ctsLock.RLock()
	var b strings.Builder
	b.Write([]byte{'['})
	isFirst := true
	for fc, bc := range adm.manager.clients {
		if !isFirst {
			b.Write([]byte{','})
		}
		fmt.Fprintf(&b, "{\"id\":\"%v\",\"uri\":\"%v\",\"backendId\":\"%v\""+
			",\"reconnects\":%v,\"msgsReceived\":%v,\"msgsSent\":%v}",
			fc.id, jsonEscape(fc.RequestURI()), bc.BackendID(), fc.totalReconnects,
			fc.totalReceived, fc.totalSent)
		isFirst = false
	}
	b.Write([]byte{']'})
	adm.manager.ctsLock.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, b.String())
}

func (adm *admin) abortSessionHandler(w http.ResponseWriter, r *http.Request) {
	if !adm.isHTTPPost(w, r) {
		return
	}
	processBkActionRequest(w, r, adm.manager.abortFrontendConnection)
}

func (adm *admin) failoverSessionHandler(w http.ResponseWriter, r *http.Request) {
	if !adm.isHTTPPost(w, r) {
		return
	}
	processBkActionRequest(w, r, adm.manager.abortBackendConnection)
}

func (adm *admin) pauseFrontendHandler(w http.ResponseWriter, r *http.Request) {
	if !adm.isHTTPPost(w, r) {
		return
	}
	processBkActionRequest(w, r, adm.manager.pauseFrontend)
}

func (adm *admin) resumeFrontendHandler(w http.ResponseWriter, r *http.Request) {
	if !adm.isHTTPPost(w, r) {
		return
	}
	processBkActionRequest(w, r, adm.manager.resumeFrontend)
}

func (adm *admin) listFrontendHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, "[{\"id\":\"%v\"}]", adm.manager.id)
}
