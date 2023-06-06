/*
Copyright 2022 The Koordinator Authors.

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

package main

import (
	"log"
	"os"

	"github.com/koordinator-sh/koordinator/pkg/yarn/apis/proto/hadoopyarn"
	yarnserver "github.com/koordinator-sh/koordinator/pkg/yarn/apis/proto/hadoopyarn/server"
	yarnclient "github.com/koordinator-sh/koordinator/pkg/yarn/client"
	yarnconf "github.com/koordinator-sh/koordinator/pkg/yarn/config"
)

func main() {
	// Create YarnConfiguration
	conf, _ := yarnconf.NewYarnConfiguration(os.Getenv("HADOOP_CONF_DIR"))

	// Create YarnAdminClient
	yarnAdminClient, _ := yarnclient.CreateYarnAdminClient(conf)

	host := "core-1-1.c-5eb55ff2323a90d6.cn-zhangjiakou.emr.aliyuncs.com"
	port := int32(8041)
	vCores := int32(100)
	memoryMB := int64(10240)
	request := &yarnserver.UpdateNodeResourceRequestProto{
		NodeResourceMap: []*hadoopyarn.NodeResourceMapProto{
			{
				NodeId: &hadoopyarn.NodeIdProto{
					Host: &host,
					Port: &port,
				},
				ResourceOption: &hadoopyarn.ResourceOptionProto{
					Resource: &hadoopyarn.ResourceProto{
						Memory:       &memoryMB,
						VirtualCores: &vCores,
					},
				},
			},
		},
	}
	response, err := yarnAdminClient.UpdateNodeResource(request)

	if err != nil {
		log.Fatal("yarnAdminClient.UpdateNodeResource ", err)
	}

	log.Printf("UpdateNodeResource response %v", response)
}
