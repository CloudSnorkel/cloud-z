package providers

import (
	"cloud-z/metadata"
	"cloud-z/reporting"
	"errors"
	"fmt"
)

type AwsProvider struct {
	token                    *string
	instanceIdentityDocument *instanceIdentityDocumentType
}

func (provider *AwsProvider) Detect() bool {
	server, err := metadata.GetMetadataHeader("Server")

	if err != nil {
		return false
	}

	return server == "EC2ws"
}

func (provider *AwsProvider) getMetadataJsonWithPossibleToken(url string, target interface{}) error {
	if provider.token != nil {
		return metadata.GetMetadataJson(url, target, "X-aws-ec2-metadata-token", *provider.token)
	}

	err := metadata.GetMetadataJson(url, target, "", "")
	if errors.Is(err, metadata.UnauthorizedError) {
		tokenValue, err := metadata.PutMetadata("/latest/api/token", "X-aws-ec2-metadata-token-ttl-seconds", "120")
		if err != nil {
			return err
		}
		provider.token = &tokenValue
	} else {
		return err
	}

	return metadata.GetMetadataJson(url, target, "X-aws-ec2-metadata-token", *provider.token)
}

func (provider *AwsProvider) getMetadataTextWithPossibleToken(url string) (string, error) {
	// TODO refactor to share code with JSON function
	if provider.token != nil {
		return metadata.GetMetadataText(url, "X-aws-ec2-metadata-token", *provider.token)
	}

	result, err := metadata.GetMetadataText(url, "", "")
	if errors.Is(err, metadata.UnauthorizedError) {
		tokenValue, err := metadata.PutMetadata("/latest/api/token", "X-aws-ec2-metadata-token-ttl-seconds", "120")
		if err != nil {
			return "", err
		}
		provider.token = &tokenValue

		return metadata.GetMetadataText(url, "X-aws-ec2-metadata-token", *provider.token)
	} else if err != nil {
		return "", err
	} else {
		return result, nil
	}
}

type instanceIdentityDocumentType struct {
	MarketplaceProductCodes *[]string `json:"marketplaceProductCodes"`
	AvailabilityZone        string    `json:"availabilityZone"`
	PrivateIp               string    `json:"privateIp"`
	Version                 string    `json:"version"`
	InstanceId              string    `json:"instanceId"`
	BillingProducts         *[]string `json:"billingProducts"`
	InstanceType            string    `json:"instanceType"`
	AccountId               string    `json:"accountId"`
	ImageId                 string    `json:"imageId"`
	PendingTime             string    `json:"pendingTime"`
	Architecture            string    `json:"architecture"`
	KernelId                *string   `json:"kernelId"`
	RamdiskId               *string   `json:"ramdiskId"`
	Region                  string    `json:"region"`
}

func (provider *AwsProvider) getInstanceIdentity() error {
	if provider.instanceIdentityDocument != nil {
		return nil
	}

	provider.instanceIdentityDocument = &instanceIdentityDocumentType{}
	err := provider.getMetadataJsonWithPossibleToken("/2021-07-15/dynamic/instance-identity/document", provider.instanceIdentityDocument)
	if err != nil {
		provider.instanceIdentityDocument = nil
		return err
	}

	return nil
}

func (provider *AwsProvider) GetData(report *reporting.Report) {
	report.Cloud = "AWS"

	// https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/instance-identity-documents.html
	err := provider.getInstanceIdentity()
	if err != nil {
		report.AddError(fmt.Sprintf("Unable to get metadata: %v", err))
	}

	report.ImageId = provider.instanceIdentityDocument.ImageId
	report.InstanceId = provider.instanceIdentityDocument.InstanceId
	report.InstanceType = provider.instanceIdentityDocument.InstanceType
	report.Region = provider.instanceIdentityDocument.Region

	report.AvailabilityZone, err = provider.getMetadataTextWithPossibleToken("/2021-07-15/meta-data/placement/availability-zone-id")
	if err != nil {
		report.AddError(fmt.Sprintf("Unable to get az: %v", err))
	}
}
