package metadata

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

func GetMetadataHeader(header string) (string, error) {
	resp, err := http.Get("http://169.254.169.254/")

	if err != nil {
		return "", err
	}

	resp.Body.Close()

	return resp.Header.Get(header), nil
}

var UnauthorizedError = errors.New("metadata server returned 401")

func requestMetadata(action string, url string, headerName string, headerValue string) (*http.Response, error) {
	req, err := http.NewRequest(action, "http://169.254.169.254"+url, nil)
	if err != nil {
		return nil, err
	}

	if headerName != "" && headerValue != "" {
		req.Header.Add(headerName, headerValue)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return resp, err
	}

	if resp.StatusCode == 401 {
		defer resp.Body.Close()
		return resp, UnauthorizedError
	} else if resp.StatusCode != 200 {
		defer resp.Body.Close()
		return resp, errors.New(resp.Status)
	}

	return resp, nil
}

func GetMetadataJson(url string, target interface{}, headerName string, headerValue string) error {
	resp, err := requestMetadata("GET", url, headerName, headerValue)
	if err != nil {
		return err
	}

	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(target)
}

func GetMetadataText(url string, headerName string, headerValue string) (string, error) {
	resp, err := requestMetadata("GET", url, headerName, headerValue)
	if err != nil {
		return "", err
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(body), nil
}

func PutMetadata(url string, headerName string, headerValue string) (string, error) {
	resp, err := requestMetadata("PUT", url, headerName, headerValue)
	if err != nil {
		return "", err
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(body), nil
}
