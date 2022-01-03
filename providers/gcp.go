package providers

import (
	"cloud-z/metadata"
	"log"
)

type GcpProvider struct {
}

func (provider *GcpProvider) Detect() bool {
	flavor, err := metadata.GetMetadataHeader("Metadata-Flavor")

	if err != nil {
		log.Println(err)
	}

	return flavor == "Google"
}

func (provider *GcpProvider) getMetadata(url string) (string, error) {
	return metadata.GetMetadataText(url, "Metadata-Flavor", "Google")
}

func (provider *GcpProvider) GetData() ([][]string, error) {
	var attributes [][]string
	attributes = append(attributes, []string{"Cloud", "GCP"})

	// https://cloud.google.com/appengine/docs/standard/java/accessing-instance-metadata
	urls := [][]string{
		{"Instance id", "/computeMetadata/v1/instance/id"},
		{"Instance type", "/computeMetadata/v1/instance/machine-type"},
		{"CPU platform", "/computeMetadata/v1/instance/cpu-platform"},
		{"Zone", "/computeMetadata/v1/instance/zone"},
		{"Image", "/computeMetadata/v1/instance/image"},
	}
	for _, url := range urls {
		data, err := provider.getMetadata(url[1])
		if err != nil {
			return [][]string{}, err
		}
		attributes = append(attributes, []string{url[0], data})
	}

	return attributes, nil
}
