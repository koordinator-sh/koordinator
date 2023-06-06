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

package client

import (
	yarnserver "github.com/koordinator-sh/koordinator/pkg/yarn/apis/proto/hadoopyarn/server"
	yarnconf "github.com/koordinator-sh/koordinator/pkg/yarn/config"
)

type YarnAdminClient struct {
	client yarnserver.ResourceManagerAdministrationProtocolService
}

func CreateYarnAdminClient(conf yarnconf.YarnConfiguration) (*YarnAdminClient, error) {
	c, err := yarnserver.DialResourceManagerAdministrationProtocolService(conf)
	return &YarnAdminClient{client: c}, err
}

func (c *YarnAdminClient) UpdateNodeResource(request *yarnserver.UpdateNodeResourceRequestProto) (*yarnserver.UpdateNodeResourceResponseProto, error) {
	response := &yarnserver.UpdateNodeResourceResponseProto{}
	err := c.client.UpdateNodeResource(request, response)
	if err != nil {
		return nil, err
	}
	return response, nil
}
