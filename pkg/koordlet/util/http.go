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
		return result, fmt.Errorf("http reponse statue code %v, body %v", res.StatusCode, string(result))
	}
	return result, nil
}
