package reporting

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

var apiKey = "not-set"

func (report *Report) Send() {
	if apiKey == "not-set" {
		fmt.Println("No API key set. Skipping report.")
		return
	}

	fmt.Println("Sending report...")

	reportJson, err := json.Marshal(report)
	if err != nil {
		panic(err)
	}

	req, err := http.NewRequest("POST", "https://weather.cloudsnorkel.com/submit/", bytes.NewReader(reportJson))
	if err != nil {
		panic(err)
	}

	req.Header.Add("X-API-Key", apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		fmt.Printf("Bad status code: %v", resp.Status)
	}
}
