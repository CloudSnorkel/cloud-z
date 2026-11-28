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

const (
	tokenHeader    = "X-aws-ec2-metadata-token"
	tokenTtlHeader = "X-aws-ec2-metadata-token-ttl-seconds"
	tokenTtl       = "21600" // the maximum, so the token never expires mid-run
)

func (provider *AwsProvider) Detect() bool {
	server, err := metadata.GetMetadataHeader("Server")

	if err != nil {
		return false
	}

	if server == "EC2ws" {
		return true
	}

	// With IMDSv2 required, an unauthenticated request may not look like EC2. Being able to get a
	// session token is proof enough.
	return provider.refreshToken() == nil
}

// refreshToken gets a new IMDSv2 session token.
func (provider *AwsProvider) refreshToken() error {
	tokenValue, err := metadata.PutMetadata("/latest/api/token", tokenTtlHeader, tokenTtl)
	if err != nil {
		return err
	}
	provider.token = &tokenValue
	return nil
}

// withToken runs a metadata request using IMDSv2. It gets a session token first and renews it once
// if the metadata server rejects it. If no token can be had at all, it falls back to IMDSv1.
func (provider *AwsProvider) withToken(request func(headerName string, headerValue string) error) error {
	if provider.token == nil {
		if provider.refreshToken() != nil {
			return request("", "")
		}
	}

	err := request(tokenHeader, *provider.token)
	if errors.Is(err, metadata.UnauthorizedError) {
		if err := provider.refreshToken(); err != nil {
			return err
		}
		err = request(tokenHeader, *provider.token)
	}
	return err
}

func (provider *AwsProvider) getMetadataJson(url string, target interface{}) error {
	return provider.withToken(func(headerName string, headerValue string) error {
		return metadata.GetMetadataJson(url, target, headerName, headerValue)
	})
}

func (provider *AwsProvider) getMetadataText(url string) (string, error) {
	var result string
	err := provider.withToken(func(headerName string, headerValue string) error {
		var err error
		result, err = metadata.GetMetadataText(url, headerName, headerValue)
		return err
	})
	return result, err
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
	err := provider.getMetadataJson("/2021-07-15/dynamic/instance-identity/document", provider.instanceIdentityDocument)
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

	report.AvailabilityZone, err = provider.getMetadataText("/2021-07-15/meta-data/placement/availability-zone-id")
	if err != nil {
		report.AddError(fmt.Sprintf("Unable to get az: %v", err))
	}
}
