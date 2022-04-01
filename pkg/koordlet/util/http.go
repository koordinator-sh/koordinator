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

package util

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"time"
)

func DoHTTPGet(methodName string, ip string, port int, timeout int) ([]byte, error) {
	httpClient := &http.Client{
		Timeout: time.Duration(timeout) * time.Second,
	}

	result := []byte{}

	httpURL := fmt.Sprintf("http://%s:%d/%s", ip, port, methodName)
	request, err := http.NewRequest(http.MethodGet, httpURL, nil)
	if err != nil {
		return result, fmt.Errorf("failed to create http request, url: %v, err: %v", httpURL, err)
	}

	res, err := httpClient.Do(request)
	if err != nil {
		return result, fmt.Errorf("failed to connect url: %v, err: %v", httpURL, err)
	}
	defer res.Body.Close()

	result, err = ioutil.ReadAll(res.Body)
	if err != nil {
		return result, fmt.Errorf("failed to read from response, url: %v, http code: %v, err: %v",
			httpURL, res.StatusCode, err)
	}

	if res.StatusCode != http.StatusOK {
		return result, fmt.Errorf("http response statue code %v, body %v", res.StatusCode, string(result))
	}
	return result, nil
}
