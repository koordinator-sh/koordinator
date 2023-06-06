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

package server

import (
	"encoding/json"
	proto "github.com/golang/protobuf/proto"
	gohadoop "github.com/koordinator-sh/koordinator/pkg/yarn/apis/auth"
	hadoop_ipc_client "github.com/koordinator-sh/koordinator/pkg/yarn/client/ipc"
	yarn_conf "github.com/koordinator-sh/koordinator/pkg/yarn/config"
	uuid "github.com/nu7hatch/gouuid"
	"math"
)

// Reference proto, json, and math imports to suppress error if they are not otherwise used.
var _ = proto.Marshal
var _ = &json.SyntaxError{}
var _ = math.Inf

var RESOURCE_MANAGER_ADMIN_PROTOCOL = "org.apache.hadoop.yarn.server.api.ResourceManagerAdministrationProtocolPB"

func init() {
}

type ResourceManagerAdministrationProtocolService interface {
	UpdateNodeResource(in *UpdateNodeResourceRequestProto, out *UpdateNodeResourceResponseProto) error
}

type ResourceManagerAdministrationProtocolServiceClient struct {
	*hadoop_ipc_client.Client
}

func (c *ResourceManagerAdministrationProtocolServiceClient) UpdateNodeResource(in *UpdateNodeResourceRequestProto, out *UpdateNodeResourceResponseProto) error {
	return c.Call(gohadoop.GetCalleeRPCRequestHeaderProto(&RESOURCE_MANAGER_ADMIN_PROTOCOL), in, out)
}

func DialResourceManagerAdministrationProtocolService(conf yarn_conf.YarnConfiguration) (ResourceManagerAdministrationProtocolService, error) {
	clientId, _ := uuid.NewV4()
	ugi, _ := gohadoop.CreateSimpleUGIProto()
	serverAddress, _ := conf.GetRMAdminAddress()
	c := &hadoop_ipc_client.Client{ClientId: clientId, Ugi: ugi, ServerAddress: serverAddress}
	return &ResourceManagerAdministrationProtocolServiceClient{c}, nil
}
