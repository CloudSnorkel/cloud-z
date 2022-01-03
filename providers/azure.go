package providers

import (
	"cloud-z/metadata"
	"log"
	"strings"
)

type AzureProvider struct {
}

func (provider *AzureProvider) Detect() bool {
	server, err := metadata.GetMetadataHeader("Server")

	if err != nil {
		log.Println(err)
	}

	return strings.HasPrefix(server, "Microsoft-IIS")
}

func (provider *AzureProvider) getMetadata(url string) (string, error) {
	return metadata.GetMetadataText(url, "Metadata", "true")
}

func (provider *AzureProvider) GetData() ([][]string, error) {
	var attributes [][]string
	attributes = append(attributes, []string{"Cloud", "Azure"})

	// https://docs.microsoft.com/en-us/azure/virtual-machines/windows/instance-metadata-service?tabs=linux#instance-metadata
	urls := [][]string{
		{"Instance id", "/metadata/instance/compute/vmId?api-version=2017-08-01&format=text"},
		{"Instance type", "/metadata/instance/compute/vmSize?api-version=2017-08-01&format=text"},
		{"Zone", "/metadata/instance/compute/zone?api-version=2017-08-01&format=text"},
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
