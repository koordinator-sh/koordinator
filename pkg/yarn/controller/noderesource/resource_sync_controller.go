/*
 Copyright 2023 The Koordinator Authors.

 Licensed under the Apache License, Version 2.0 (the "License");
 you may not use this file except in compliance with the License.
 You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

 Unless required by applicable law or agreed to in writing, software
 distributed under the License is distributed on an "AS IS" BASIS,
 WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 See the License for the specific language governing permissions and
 limitations under the License.
*/

package noderesource

import (
	"context"
	"fmt"
	"os"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/cri-api/pkg/errors"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"strconv"
	"strings"

	"github.com/koordinator-sh/goyarn/apis/proto/hadoopyarn"
	yarnserver "github.com/koordinator-sh/goyarn/apis/proto/hadoopyarn/server"
	yarnclient "github.com/koordinator-sh/goyarn/client"
	yarnconf "github.com/koordinator-sh/goyarn/config"
	"github.com/koordinator-sh/koordinator/apis/extension"
)

const (
	Name = "yarn-resources-sync"

	yarnNodeNameAnnotation = "node.yarn.koordinator.sh"
)

type YARNResourceSyncReconciler struct {
	client.Client
}

func (r *YARNResourceSyncReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	node := &corev1.Node{}
	if err := r.Client.Get(context.TODO(), req.NamespacedName, node); err != nil {
		if errors.IsNotFound(err) {
			klog.V(3).Infof("skip for node %v not found", req.Name)
			return ctrl.Result{}, nil
		}
		klog.Warningf("failed to get node %v, error %v", req.Name, err)
		return ctrl.Result{Requeue: true}, err
	}

	yarnNodeName, yarnNodePort, err := getYARNNodeID(node)
	if err != nil {
		klog.Warningf("fail to parse yarn node name for %v, error %v", node.Name, err)
		return ctrl.Result{}, nil
	}
	if yarnNodeName == "" || yarnNodePort == 0 {
		klog.V(3).Infof("skip for yarn node id not exist in node %v annotation %v", req.Name, yarnNodeNameAnnotation)
		return ctrl.Result{}, nil
	}

	// TODO exclude batch pod requested
	batchCPU, cpuExist := node.Status.Allocatable[extension.BatchCPU]
	batchMemory, memExist := node.Status.Allocatable[extension.BatchMemory]
	if !cpuExist || !memExist {
		klog.V(3).Infof("skip sync node %v, since batch cpu or memory not exist in allocatable %v", node.Name, node.Status.Allocatable)
		return ctrl.Result{}, nil
	}

	// TODO control update frequency
	if err := updateYARNNodeResource(yarnNodeName, yarnNodePort, batchCPU, batchMemory); err != nil {
		klog.Warningf("update batch resource to yarn node %v:%v failed, error %v", yarnNodeName, yarnNodePort, err)
		return ctrl.Result{Requeue: true}, err
	}
	return ctrl.Result{}, nil
}

func Add(mgr ctrl.Manager) error {
	r := &YARNResourceSyncReconciler{
		Client: mgr.GetClient(),
	}
	return r.SetupWithManager(mgr)
}

func (r *YARNResourceSyncReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Node{}).
		Named(Name).
		Complete(r)
}

func getYARNNodeID(node *corev1.Node) (string, int32, error) {
	if node == nil || node.Annotations == nil {
		return "", 0, nil
	}
	// TODO get yarn node name from node manager pod attr
	nodeID, exist := node.Annotations[yarnNodeNameAnnotation]
	if !exist {
		return "", 0, nil
	}
	attrs := strings.Split(nodeID, ":")
	if len(attrs) != 2 {
		return "", 0, fmt.Errorf("illegal format during parse yarn node name %v for node %v", nodeID, node.Name)
	}
	nodeName := attrs[0]
	nodePort, err := strconv.ParseInt(attrs[1], 10, 64)
	if err != nil {
		return "", 0, fmt.Errorf("illegal format during parse yarn node port %v for node %v", nodeID, node.Name)
	}
	return nodeName, int32(nodePort), nil
}

func updateYARNNodeResource(yarnNodeName string, yarnNodePort int32, cpuMilli, memory resource.Quantity) error {
	// TODO use flags for conf dir config
	conf, _ := yarnconf.NewYarnConfiguration(os.Getenv("HADOOP_CONF_DIR"))
	// TODO keep client alive instead of create every time
	yarnAdminClient, _ := yarnclient.CreateYarnAdminClient(conf)

	// convert to yarn format
	vcores := int32(cpuMilli.ScaledValue(resource.Kilo))
	memoryMB := memory.ScaledValue(resource.Mega)

	request := &yarnserver.UpdateNodeResourceRequestProto{
		NodeResourceMap: []*hadoopyarn.NodeResourceMapProto{
			{
				NodeId: &hadoopyarn.NodeIdProto{
					Host: pointer.String(yarnNodeName),
					Port: pointer.Int32(yarnNodePort),
				},
				ResourceOption: &hadoopyarn.ResourceOptionProto{
					Resource: &hadoopyarn.ResourceProto{
						Memory:       &memoryMB,
						VirtualCores: &vcores,
					},
				},
			},
		},
	}
	_, err := yarnAdminClient.UpdateNodeResource(request)
	return err
}
