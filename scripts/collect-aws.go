package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/smithy-go"
	"github.com/jedib0t/go-pretty/v6/progress"
	"github.com/spf13/cobra"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

var (
	argDryRun                  = false
	argIncludeTypes            []string
	argExcludeTypes            []string
	argSpotCap                 float64
	argUseLocalInstancePricing = false
)

type instanceDescriptor struct {
	name     string
	type_    types.InstanceType
	platform types.ArchitectureType
}

const noPriceCap float64 = -1

type workOrder struct {
	instanceType     types.InstanceType
	availabilityZone string
	ami              *string
	priceCap         float64 // or noPriceCap
	downloadUrl      string
	client           *ec2.Client
}

type instancePricingData struct {
	InstanceType string `json:"instance_type"`
	Pricing      map[string]struct {
		Linux struct {
			OnDemand interface{} `json:"ondemand"`
		} `json:"linux"`
	} `json:"pricing"`
}

type pricing map[string]map[string]float64

func getPricing() map[string]map[string]float64 {
	// TODO get from AWS API
	var rawPricing []instancePricingData

	if argUseLocalInstancePricing {
		pf, err := os.Open("instances.json")
		if err != nil {
			log.Fatal("Unable to open instances.json")
		}

		defer pf.Close()
		pd, err := io.ReadAll(pf)
		if err != nil {
			log.Fatal("Unable to read instances.json")
		}

		err = json.Unmarshal(pd, &rawPricing)
		if err != nil {
			log.Fatal(fmt.Sprintf("Unable to parse instances.json: %v", err))
		}
	} else {
		hf, err := http.Get("https://github.com/vantage-sh/ec2instances.info/raw/master/www/instances.json")
		if err != nil {
			log.Fatal("Unable to download instances.json")
		}

		defer hf.Body.Close()
		hd, err := io.ReadAll(hf.Body)
		if err != nil {
			log.Fatal("Unable to read remote instances.json")
		}

		err = json.Unmarshal(hd, &rawPricing)
		if err != nil {
			log.Fatal(fmt.Sprintf("Unable to parse remote instances.json: %v", err))
		}
	}

	allPricing := make(pricing)
	for _, instance := range rawPricing {
		allPricing[instance.InstanceType] = make(map[string]float64)

		for region, pricing := range instance.Pricing {
			switch v := pricing.Linux.OnDemand.(type) {
			case string:
				price, err := strconv.ParseFloat(v, 64)
				if err != nil {
					log.Printf("Unable to parse price for %v @ %v [%v]", instance.InstanceType, region, v)
				} else {
					allPricing[instance.InstanceType][region] = price
				}
			case float64:
				allPricing[instance.InstanceType][region] = v
			default:
				log.Printf("Unable to parse non-string price for %v @ %v [%v]", instance.InstanceType, region, v)
			}
		}
	}

	return allPricing
}

func run() {
	var allPricing pricing
	var totalPrice float64
	if argSpotCap != 1 {
		allPricing = getPricing()
	}

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

	var work []workOrder

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
		Message: "Planning work",
		Total:   int64(len(regions)),
	}
	pw.AppendTracker(&regionTracker)

	var regionWaitGroup sync.WaitGroup
	var workChannel = make(chan workOrder, 10)
	for _, region := range regions {
		regionWaitGroup.Add(1)
		go func(region string) {
			defer regionWaitGroup.Done()
			defer regionTracker.Increment(1)

			collectWork(ctx, cfg, region, allPricing, workChannel)
		}(region)
	}

	go func() {
		regionWaitGroup.Wait()
		close(workChannel)
	}()

	for workItem := range workChannel {
		work = append(work, workItem)

		if argSpotCap != 1 {
			// assume instance will be up for at most the minimum of 1 minute
			totalPrice += workItem.priceCap / 60
		}
	}

	regionTracker.MarkAsDone()

	totalPriceDesc := ""
	if argSpotCap != 1 {
		totalPriceDesc = fmt.Sprintf(" for up to $%.02f", totalPrice)
	}
	regionTracker.UpdateMessage(fmt.Sprintf("Planned %v spot instances%s", len(work), totalPriceDesc))

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
		prefix := ""
		if argDryRun {
			prefix = "[DRY RUN] "
		}

		workTracker.UpdateMessage(fmt.Sprintf("%vRequesting spot instance %v @ %v (%v failed)",
			prefix, workItem.instanceType, workItem.availabilityZone, len(spotErrors)))

		err := requestSpotInstance(ctx, workItem)

		if err != nil {
			spotErrors = append(spotErrors, err)
			workTracker.IncrementWithError(1)
		} else {
			workTracker.Increment(1)
		}
	}

	workTracker.UpdateMessage(fmt.Sprintf("Requested %v spot instances, %v failed", len(work), len(spotErrors)))
	workTracker.MarkAsDone()

	for pw.IsRenderInProgress() {
		if pw.LengthActive() == 0 {
			pw.Stop()
		}
		time.Sleep(time.Millisecond * 100)
	}

	for _, err := range spotErrors {
		log.Println(err)
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

func shouldUseInstanceType(instanceType types.InstanceType) bool {
	for _, excludePattern := range argExcludeTypes {
		matched, _ := filepath.Match(excludePattern, string(instanceType))
		if matched {
			return false
		}
	}

	for _, includePattern := range argIncludeTypes {
		matched, _ := filepath.Match(includePattern, string(instanceType))
		if matched {
			return true
		}
	}

	return false
}

func describeInstanceTypes(ctx context.Context, ec2Client *ec2.Client) []instanceDescriptor {
	var result []instanceDescriptor

	paginator := ec2.NewDescribeInstanceTypesPaginator(ec2Client, &ec2.DescribeInstanceTypesInput{})
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			log.Fatal(err)
		}
		for _, instanceType := range output.InstanceTypes {
			if !shouldUseInstanceType(instanceType.InstanceType) {
				continue
			}

			arch := instanceType.ProcessorInfo.SupportedArchitectures[0]
			if arch == "i386" && len(instanceType.ProcessorInfo.SupportedArchitectures) > 1 {
				arch = instanceType.ProcessorInfo.SupportedArchitectures[1]
			}

			result = append(result, instanceDescriptor{
				name:     string(instanceType.InstanceType),
				type_:    instanceType.InstanceType,
				platform: arch,
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

func collectWork(ctx context.Context, cfg aws.Config, region string, allPricing pricing, workChannel chan<- workOrder) {
	x64ami := findAmi(ctx, cfg, region, "x86_64", "gp2")
	arm64ami := findAmi(ctx, cfg, region, "arm64", "gp2")
	client := regionClient(cfg, region)
	azs := allAZs(ctx, client)

	// TODO combine with describe_instance_type_offerings so we don't start types where they are not available
	for _, it := range describeInstanceTypes(ctx, client) {
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
				log.Println(fmt.Sprintf("Unknown platform %v for %v", it.platform, it.type_))
				continue
			}

			priceCap := noPriceCap
			if argSpotCap != 1 {
				reportedPrice := allPricing[string(it.type_)][region]

				if reportedPrice == 0 {
					log.Printf("Skipping %v @ %v as price is missing", it.type_, az)
					continue
				}

				priceCap = reportedPrice * argSpotCap
			}

			workChannel <- workOrder{
				instanceType:     it.type_,
				availabilityZone: az,
				ami:              ami,
				priceCap:         priceCap,
				downloadUrl:      downloadUrl,
				client:           client,
			}
		}
	}
}

func requestSpotInstance(ctx context.Context, workItem workOrder) error {
	var instances int32 = 1

	requestParams := &ec2.RequestSpotInstancesInput{
		DryRun:        aws.Bool(argDryRun),
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
	}

	if workItem.priceCap != noPriceCap {
		requestParams.SpotPrice = aws.String(fmt.Sprintf("%.05f", workItem.priceCap))
	}

	_, err := workItem.client.RequestSpotInstances(ctx, requestParams)

	var apiErr smithy.APIError
	if argDryRun && errors.As(err, &apiErr) && apiErr.ErrorCode() == "DryRunOperation" {
		return nil
	}

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
			run()
		},
	}
	rootCmd.Flags().BoolVarP(&argDryRun, "dry-run", "d", false, "Do not run any instances")
	rootCmd.Flags().StringSliceVarP(&argIncludeTypes, "include", "i", []string{"*"}, "List of instance types to include")
	rootCmd.Flags().StringSliceVarP(&argExcludeTypes, "exclude", "e", []string{}, "List of instance types to exclude")
	rootCmd.Flags().Float64VarP(&argSpotCap, "spot-cap", "s", 1, "Max cap of spot price as percentage of on-demand price (0 to 1)")
	rootCmd.Flags().BoolVarP(&argUseLocalInstancePricing, "local-pricing", "l", false, "Use local copy of instances.json")
	if err := rootCmd.Execute(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
