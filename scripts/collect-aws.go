package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/jedib0t/go-pretty/v6/progress"
	"github.com/spf13/cobra"
	"log"
	"math/rand"
	"os"
	"time"
)

type instanceDescriptor struct {
	name     string
	type_    types.InstanceType
	platform types.ArchitectureType
}

type wordOrder struct {
	instanceType     types.InstanceType
	availabilityZone string
	ami              *string
	downloadUrl      string
	client           *ec2.Client
}

func run(requestedInstanceTypes map[string]bool) {
	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithAssumeRoleCredentialOptions(func(o *stscreds.AssumeRoleOptions) {
			o.TokenProvider = stscreds.StdinTokenProvider
		}),
	)
	if err != nil {
		log.Fatal(err)
	}

	baseClient := regionClient(cfg, "us-east-1")
	regions := allRegions(ctx, baseClient)

	var work []wordOrder

	pw := progress.NewWriter()
	pw.ShowETA(true)
	pw.SetStyle(progress.StyleCircle)
	pw.Style().Colors = progress.StyleColorsExample
	pw.Style().Options.TimeDonePrecision = time.Second
	pw.Style().Options.TimeInProgressPrecision = time.Second
	pw.Style().Options.TimeOverallPrecision = time.Second
	go pw.Render()

	// gather a collection of instance type and az combos to launch
	regionTracker := progress.Tracker{
		Message: "Collecting work",
		Total:   int64(len(regions)),
	}
	pw.AppendTracker(&regionTracker)

	for _, region := range regions {
		regionTracker.UpdateMessage(fmt.Sprintf("Collecting work %v", region))

		x64ami := findAmi(ctx, cfg, region, "x86_64", "gp2")
		arm64ami := findAmi(ctx, cfg, region, "arm64", "gp2")
		client := regionClient(cfg, region)
		azs := allAZs(ctx, client)

		for _, it := range describeInstanceTypes(ctx, client, requestedInstanceTypes) {
			for _, az := range azs {
				var ami *string
				var downloadUrl string

				if it.platform == types.ArchitectureTypeArm64 {
					ami = arm64ami
					downloadUrl = "https://weather.cloudsnorkel.com/cloud-z/download/linux/arm64"
				} else if it.platform == types.ArchitectureTypeX8664 {
					ami = x64ami
					downloadUrl = "https://weather.cloudsnorkel.com/cloud-z/download/linux/x64"
				} else {
					log.Fatal(fmt.Sprintf("Unknown platform %v", it.platform))
				}

				work = append(work, wordOrder{
					instanceType:     it.type_,
					availabilityZone: az,
					ami:              ami,
					downloadUrl:      downloadUrl,
					client:           client,
				})
			}
		}

		regionTracker.Increment(1)
	}

	regionTracker.MarkAsDone()

	// randomize work to avoid throttling
	rand.Seed(time.Now().UnixNano())
	rand.Shuffle(len(work), func(i, j int) { work[i], work[j] = work[j], work[i] })

	// request spot instances
	workTracker := &progress.Tracker{
		Message: "Requesting spot instances",
		Total:   int64(len(work)),
	}
	pw.AppendTracker(workTracker)

	var spotErrors []error

	for _, workItem := range work {
		workTracker.UpdateMessage(fmt.Sprintf("Requesting spot instance %v @ %v", workItem.instanceType, workItem.availabilityZone))

		err := requestSpotInstance(ctx, workItem)

		if err != nil {
			spotErrors = append(spotErrors, err)
			workTracker.IncrementWithError(1)
		} else {
			workTracker.Increment(1)
		}
	}

	workTracker.MarkAsDone()

	for _, err := range spotErrors {
		log.Println(err)
	}

	for pw.IsRenderInProgress() {
		if pw.LengthActive() == 0 {
			pw.Stop()
		}
		time.Sleep(time.Millisecond * 100)
	}
}

func regionClient(cfg aws.Config, region string) *ec2.Client {
	return ec2.NewFromConfig(cfg, func(o *ec2.Options) {
		o.Region = region
	})
}

func allRegions(ctx context.Context, ec2Client *ec2.Client) []string {
	output, err := ec2Client.DescribeRegions(ctx, &ec2.DescribeRegionsInput{})
	if err != nil {
		log.Fatal(err)
	}

	var result []string
	for _, r := range output.Regions {
		result = append(result, *r.RegionName)
	}

	return result
}

func describeInstanceTypes(ctx context.Context, ec2Client *ec2.Client, requestedInstanceTypes map[string]bool) []instanceDescriptor {
	var result []instanceDescriptor

	paginator := ec2.NewDescribeInstanceTypesPaginator(ec2Client, &ec2.DescribeInstanceTypesInput{})
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			log.Fatal(err)
		}
		for _, instanceType := range output.InstanceTypes {
			if !requestedInstanceTypes[string(instanceType.InstanceType)] {
				continue
			}

			result = append(result, instanceDescriptor{
				name:     string(instanceType.InstanceType),
				type_:    instanceType.InstanceType,
				platform: instanceType.ProcessorInfo.SupportedArchitectures[0],
			})
		}
	}

	return result
}

func allAZs(ctx context.Context, ec2Client *ec2.Client) []string {
	output, err := ec2Client.DescribeAvailabilityZones(ctx, &ec2.DescribeAvailabilityZonesInput{})
	if err != nil {
		log.Fatal(err)
	}

	var result []string
	for _, az := range output.AvailabilityZones {
		result = append(result, *az.ZoneName)
	}

	return result
}

func findAmi(ctx context.Context, cfg aws.Config, region string, platform string, ebs string) *string {
	client := ssm.NewFromConfig(cfg, func(o *ssm.Options) {
		o.Region = region
	})

	output, err := client.GetParameter(ctx, &ssm.GetParameterInput{
		Name: aws.String(fmt.Sprintf("/aws/service/ami-amazon-linux-latest/amzn2-ami-hvm-%v-%v", platform, ebs)),
	})
	if err != nil {
		log.Fatal(err)
	}

	return output.Parameter.Value
}

func requestSpotInstance(ctx context.Context, workItem wordOrder) error {
	var instances int32 = 1

	_, err := workItem.client.RequestSpotInstances(ctx, &ec2.RequestSpotInstancesInput{
		InstanceCount: &instances,
		ValidUntil:    aws.Time(time.Now().UTC().Add(30 * time.Minute)),
		LaunchSpecification: &types.RequestSpotLaunchSpecification{
			ImageId:      workItem.ami,
			InstanceType: workItem.instanceType,
			Placement: &types.SpotPlacement{
				AvailabilityZone: aws.String(workItem.availabilityZone),
			},
			UserData: aws.String(userData(workItem.downloadUrl)),
		},
	})

	return err
}

func userData(downloadUrl string) string {
	return base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf(`#!/bin/bash
curl -sLo cloud-z.tar.gz %v
tar xzf cloud-z.tar.gz
./cloud-z --no-color --report
poweroff`, downloadUrl)))
}

func main() {
	rootCmd := &cobra.Command{
		Use:   "collect-aws",
		Short: "Runs Cloud-Z on multiple regions, azs, and instance types on AWS",
		Run: func(cmd *cobra.Command, args []string) {
			instanceTypes, err := cmd.Flags().GetStringSlice("instance-types")
			if err != nil {
				log.Fatal(err)
			}

			instanceTypesMap := make(map[string]bool)
			for _, it := range instanceTypes {
				instanceTypesMap[it] = true
			}

			run(instanceTypesMap)
		},
	}
	rootCmd.Flags().StringSliceP("instance-types", "i", []string{}, "List of instance types to use")
	_ = rootCmd.MarkFlagRequired("instance-types")
	if err := rootCmd.Execute(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
