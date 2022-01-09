package providers

import (
	"cloud-z/metadata"
	"cloud-z/reporting"
	"fmt"
	"strings"
)

type AzureProvider struct {
}

func (provider *AzureProvider) Detect() bool {
	server, err := metadata.GetMetadataHeader("Server")

	if err != nil {
		return false
	}

	return strings.HasPrefix(server, "Microsoft-IIS")
}

func (provider *AzureProvider) getMetadata(url string) (string, error) {
	return metadata.GetMetadataText(url, "Metadata", "true")
}

func (provider *AzureProvider) GetData(report *reporting.Report) {
	report.Cloud = "Azure"

	var attributes [][]string
	attributes = append(attributes, []string{"Cloud", "Azure"})

	// https://docs.microsoft.com/en-us/azure/virtual-machines/windows/instance-metadata-service?tabs=linux#instance-metadata
	urls := map[*string]string{
		&report.InstanceId:       "/metadata/instance/compute/vmId?api-version=2017-08-01&format=text",
		&report.InstanceType:     "/metadata/instance/compute/vmSize?api-version=2017-08-01&format=text",
		&report.AvailabilityZone: "/metadata/instance/compute/zone?api-version=2017-08-01&format=text",
	}

	for target, url := range urls {
		data, err := provider.getMetadata(url)
		if err != nil {
			report.AddError(fmt.Sprintf("Failed to download: %v", url))
			continue
		}
		*target = data
	}
}
