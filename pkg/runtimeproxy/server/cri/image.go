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

package cri

import (
	"context"

	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
	runtimeapialpha "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
)

func (c *criServer) PullImage(ctx context.Context, req *runtimeapi.PullImageRequest) (*runtimeapi.PullImageResponse, error) {
	return c.backendImageServiceClient.PullImage(ctx, req)
}

func (c *criServer) ImageStatus(ctx context.Context, req *runtimeapi.ImageStatusRequest) (*runtimeapi.ImageStatusResponse, error) {
	return c.backendImageServiceClient.ImageStatus(ctx, req)
}

func (c *criServer) RemoveImage(ctx context.Context, req *runtimeapi.RemoveImageRequest) (*runtimeapi.RemoveImageResponse, error) {
	return c.backendImageServiceClient.RemoveImage(ctx, req)
}

func (c *criServer) ListImages(ctx context.Context, req *runtimeapi.ListImagesRequest) (*runtimeapi.ListImagesResponse, error) {
	return c.backendImageServiceClient.ListImages(ctx, req)
}

func (c *criServer) ImageFsInfo(ctx context.Context, req *runtimeapi.ImageFsInfoRequest) (*runtimeapi.ImageFsInfoResponse, error) {
	return c.backendImageServiceClient.ImageFsInfo(ctx, req)
}

func (c *criAlphaServer) PullImage(ctx context.Context, req *runtimeapialpha.PullImageRequest) (*runtimeapialpha.PullImageResponse, error) {
	return c.backendImageServiceClient.PullImage(ctx, req)
}

func (c *criAlphaServer) ImageStatus(ctx context.Context, req *runtimeapialpha.ImageStatusRequest) (*runtimeapialpha.ImageStatusResponse, error) {
	return c.backendImageServiceClient.ImageStatus(ctx, req)
}

func (c *criAlphaServer) RemoveImage(ctx context.Context, req *runtimeapialpha.RemoveImageRequest) (*runtimeapialpha.RemoveImageResponse, error) {
	return c.backendImageServiceClient.RemoveImage(ctx, req)
}

func (c *criAlphaServer) ListImages(ctx context.Context, req *runtimeapialpha.ListImagesRequest) (*runtimeapialpha.ListImagesResponse, error) {
	return c.backendImageServiceClient.ListImages(ctx, req)
}

func (c *criAlphaServer) ImageFsInfo(ctx context.Context, req *runtimeapialpha.ImageFsInfoRequest) (*runtimeapialpha.ImageFsInfoResponse, error) {
	return c.backendImageServiceClient.ImageFsInfo(ctx, req)
}
