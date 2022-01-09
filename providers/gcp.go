package providers

import (
	"cloud-z/metadata"
	"cloud-z/reporting"
	"fmt"
	"strings"
)

type GcpProvider struct {
}

func (provider *GcpProvider) Detect() bool {
	flavor, err := metadata.GetMetadataHeader("Metadata-Flavor")

	if err != nil {
		return false
	}

	return flavor == "Google"
}

func (provider *GcpProvider) getMetadata(url string) (string, error) {
	return metadata.GetMetadataText(url, "Metadata-Flavor", "Google")
}

func lastPartOfString(s string) string {
	parts := strings.Split(s, "/")
	if len(parts) > 1 {
		return parts[len(parts)-1]
	}
	return s
}

func (provider *GcpProvider) GetData(report *reporting.Report) {
	report.Cloud = "GCP"

	// https://cloud.google.com/appengine/docs/standard/java/accessing-instance-metadata
	urls := map[*string]string{
		&report.InstanceId:   "/computeMetadata/v1/instance/id",
		&report.InstanceType: "/computeMetadata/v1/instance/machine-type",
		//"CPU platform":           "/computeMetadata/v1/instance/cpu-platform",
		&report.AvailabilityZone: "/computeMetadata/v1/instance/zone",
		&report.ImageId:          "/computeMetadata/v1/instance/image",
	}
	for target, url := range urls {
		data, err := provider.getMetadata(url)
		if err != nil {
			report.AddError(fmt.Sprintf("Failed to download: %v", url))
			continue
		}
		*target = data
	}

	// remove project id which is PII
	report.InstanceType = lastPartOfString(report.InstanceType)
	report.AvailabilityZone = lastPartOfString(report.AvailabilityZone)
}
