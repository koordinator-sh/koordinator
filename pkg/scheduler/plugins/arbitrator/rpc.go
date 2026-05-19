package arbitrator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"
	k8sfwk "k8s.io/kubernetes/pkg/scheduler/framework"
)

type ReserveRequest struct {
	Pod           *corev1.Pod `json:"pod"`
	NodeName      string      `json:"nodeName"`
	SchedulerName string      `json:"schedulerName"`
}

type UnreserveRequest struct {
	PodUID types.UID `json:"podUID"`
}

type PreBindRequest struct {
	PodUID        types.UID `json:"podUID"`
	SchedulerName string    `json:"schedulerName"`
}

type ArbitratorRPCClient struct {
	serverURL  string
	httpClient *http.Client
}

func NewArbitratorRPCClient(serverURL string) *ArbitratorRPCClient {
	return &ArbitratorRPCClient{
		serverURL: serverURL,
		httpClient: &http.Client{
			Timeout: 2 * time.Second, // Fast timeout for scheduling cycles
		},
	}
}

func (c *ArbitratorRPCClient) Reserve(pod *corev1.Pod, nodeName, schedulerName string) error {
	reqData := ReserveRequest{
		Pod:           pod,
		NodeName:      nodeName,
		SchedulerName: schedulerName,
	}
	body, _ := json.Marshal(reqData)
	resp, err := c.httpClient.Post(fmt.Sprintf("%s/reserve", c.serverURL), "application/json", bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("rpc reserve failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := ioutil.ReadAll(resp.Body)
		return fmt.Errorf("arbitrator rejected reservation: %s", string(respBody))
	}
	return nil
}

func (c *ArbitratorRPCClient) Unreserve(podUID types.UID) {
	reqData := UnreserveRequest{PodUID: podUID}
	body, _ := json.Marshal(reqData)
	resp, err := c.httpClient.Post(fmt.Sprintf("%s/unreserve", c.serverURL), "application/json", bytes.NewBuffer(body))
	if err != nil {
		klog.Errorf("rpc unreserve failed: %v", err)
		return
	}
	resp.Body.Close()
}

func (c *ArbitratorRPCClient) PreBind(podUID types.UID, schedulerName string) error {
	reqData := PreBindRequest{PodUID: podUID, SchedulerName: schedulerName}
	body, _ := json.Marshal(reqData)
	resp, err := c.httpClient.Post(fmt.Sprintf("%s/prebind", c.serverURL), "application/json", bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("rpc prebind failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := ioutil.ReadAll(resp.Body)
		return fmt.Errorf("arbitrator rejected prebind: %s", string(respBody))
	}
	return nil
}

// ---------------------------------------------------------
// RPC Server implementation
// ---------------------------------------------------------

type ArbitratorRPCServer struct {
	handle fwktype.Handle
	cache  *ArbitratorCache
}

func StartArbitratorRPCServer(port string, handle fwktype.Handle, cache *ArbitratorCache) {
	server := &ArbitratorRPCServer{
		handle: handle,
		cache:  cache,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/reserve", server.handleReserve)
	mux.HandleFunc("/unreserve", server.handleUnreserve)
	mux.HandleFunc("/prebind", server.handlePreBind)

	addr := fmt.Sprintf("0.0.0.0:%s", port)
	klog.Infof("Starting ConflictArbitrator RPC Server on %s", addr)
	go func() {
		if err := http.ListenAndServe(addr, mux); err != nil {
			klog.Errorf("Arbitrator RPC Server failed: %v", err)
		}
	}()
}

func (s *ArbitratorRPCServer) handleReserve(w http.ResponseWriter, r *http.Request) {
	var req ReserveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	nodeInfoSnapshot, err := s.handle.SnapshotSharedLister().NodeInfos().Get(req.NodeName)
	if err != nil {
		http.Error(w, fmt.Sprintf("node not found in snapshot: %v", err), http.StatusBadRequest)
		return
	}
	nodeInfoStruct, ok := nodeInfoSnapshot.(*k8sfwk.NodeInfo)
	if !ok {
		http.Error(w, "nodeInfoSnapshot is not *k8sfwk.NodeInfo", http.StatusInternalServerError)
		return
	}

	err = s.cache.ReserveProposal(req.Pod, nodeInfoStruct, req.SchedulerName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *ArbitratorRPCServer) handleUnreserve(w http.ResponseWriter, r *http.Request) {
	var req UnreserveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.cache.ReleaseProposal(req.PodUID)
	w.WriteHeader(http.StatusOK)
}

func (s *ArbitratorRPCServer) handlePreBind(w http.ResponseWriter, r *http.Request) {
	var req PreBindRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	err := s.cache.CheckPreBind(req.PodUID, req.SchedulerName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	w.WriteHeader(http.StatusOK)
}
