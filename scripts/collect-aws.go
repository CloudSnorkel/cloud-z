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
	"github.com/aws/aws-sdk-go-v2/service/servicequotas"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/smithy-go"
	"github.com/jedib0t/go-pretty/v6/progress"
	"github.com/spf13/cobra"
	"golang.org/x/sync/semaphore"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
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
	vcpus    int64
}

const noPriceCap float64 = -1

type workOrder struct {
	instanceType     types.InstanceType
	vcpus            int64
	availabilityZone string
	region           string
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

			initializeSpotQuotasForRegion(ctx, cfg, region)
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
	if regionTracker.IsErrored() {
		return
	}

	totalPriceDesc := ""
	if argSpotCap != 1 {
		totalPriceDesc = fmt.Sprintf(" for up to $%.02f", totalPrice)
	}
	regionTracker.UpdateMessage(fmt.Sprintf("Planned %v spot instances%s", len(work), totalPriceDesc))

	// request spot instances
	prefix := ""
	if argDryRun {
		prefix = "[DRY RUN] "
	}

	workTracker := &progress.Tracker{
		Message: fmt.Sprintf("%vRequesting spot instances", prefix),
		Total:   int64(len(work)),
	}
	pw.AppendTracker(workTracker)

	spotErrors := make(map[string]uint)
	totalSpotErrors := 0

	var workWaitGroup sync.WaitGroup
	var errorChannel = make(chan string, 10)

	for _, workItem := range work {
		workWaitGroup.Add(1)

		go func(workItem workOrder) {
			defer workWaitGroup.Done()
			defer workTracker.Increment(1)

			quotaReleaser, err := grabQuota(ctx, workItem)
			if err != "" {
				errorChannel <- err
				return
			}
			defer quotaReleaser()

			workTracker.UpdateMessage(fmt.Sprintf("%vRequesting spot instance %v @ %v (%v failed)",
				prefix, workItem.instanceType, workItem.availabilityZone, totalSpotErrors))

			err = requestSpotInstance(ctx, workItem)
			if err != "" {
				errorChannel <- err
			}
		}(workItem)
	}

	go func() {
		workWaitGroup.Wait()
		close(errorChannel)
	}()

	for err := range errorChannel {
		spotErrors[err] += 1
		totalSpotErrors += 1
		workTracker.UpdateMessage(fmt.Sprintf("Requesting %v spot instances, %v failed", len(work), totalSpotErrors))
	}

	workTracker.UpdateMessage(fmt.Sprintf("Requested %v spot instances, %v failed", len(work), totalSpotErrors))
	workTracker.MarkAsDone()

	// finish progress bar
	for pw.IsRenderInProgress() {
		if pw.LengthActive() == 0 {
			pw.Stop()
		}
		time.Sleep(time.Millisecond * 100)
	}

	// print spot errors
	for err, count := range spotErrors {
		fmt.Printf("[%5d] %v\n", count, err)
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
				vcpus:    int64(*instanceType.VCpuInfo.DefaultVCpus),
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
				vcpus:            it.vcpus,
				availabilityZone: az,
				region:           region,
				ami:              ami,
				priceCap:         priceCap,
				downloadUrl:      downloadUrl,
				client:           client,
			}
		}
	}
}

type regionSpotQuotaDetails struct {
	maxVcpus  int64
	semaphore *semaphore.Weighted
}

var spotQuotas = []struct {
	name    string
	code    string
	matcher string
	region  sync.Map
}{
	{name: "All DL Spot Instance Requests", code: "L-85EED4F7", matcher: "dl[0-9].*"},
	{name: "All F Spot Instance Requests", code: "L-88CF9481", matcher: "f[0-9].*"},
	{name: "All G and VT Spot Instance Requests", code: "L-3819A6DF", matcher: "(g|vt)[0-9].*"},
	{name: "All Inf Spot Instance Requests", code: "L-B5D1601B", matcher: "inf[0-9].*"},
	{name: "All P Spot Instance Requests", code: "L-7212CCBC", matcher: "p[0-9].*"},
	{name: "All Standard (A, C, D, H, I, M, R, T, Z) Spot Instance Requests", code: "L-34B43A08", matcher: "[acdhimrtz][0-9].*"},
	{name: "All X Spot Instance Requests", code: "L-E3A00192", matcher: "x[0-9].*"},
}

func initializeSpotQuotasForRegion(ctx context.Context, cfg aws.Config, region string) {
	client := servicequotas.NewFromConfig(cfg, func(o *servicequotas.Options) {
		o.Region = region
	})

	for i := range spotQuotas {
		quota := &spotQuotas[i]
		quotaDetails, err := client.GetServiceQuota(ctx, &servicequotas.GetServiceQuotaInput{
			QuotaCode:   aws.String(quota.code),
			ServiceCode: aws.String("ec2"),
		})

		var maxVcpus int64 = 128 // reasonable default value
		if err != nil {
			log.Printf("Unable to get %v spot quota for %v, defaulting to %v: %v", quota.code, region, maxVcpus, err)
		} else {
			maxVcpus = int64(*quotaDetails.Quota.Value)
		}

		if maxVcpus > 0 {
			quota.region.Store(region, regionSpotQuotaDetails{
				maxVcpus,
				semaphore.NewWeighted(maxVcpus),
			})
		} else {
			quota.region.Store(region, regionSpotQuotaDetails{
				0,
				nil,
			})
		}
	}
}

func getQuotaSemaphore(workItem workOrder) (int64, *semaphore.Weighted) {
	for i := range spotQuotas {
		quota := &spotQuotas[i]
		match, _ := regexp.MatchString(quota.matcher, string(workItem.instanceType))
		if match {
			_regionQuota, ok := quota.region.Load(workItem.region)
			if !ok {
				log.Fatalf("No semaphore for %+v", workItem)
			}
			regionQuota, ok := _regionQuota.(regionSpotQuotaDetails)
			if !ok {
				log.Fatalf("Bad region quota type %+v", _regionQuota)
			}
			return regionQuota.maxVcpus, regionQuota.semaphore
		}
	}

	// instance doesn't match any quota, YOLO!!!
	// this happens with u-* and is4gen.* instances
	return 1000, semaphore.NewWeighted(1000)
}

func grabQuota(ctx context.Context, workItem workOrder) (func(), string) {
	max, quotaSemaphore := getQuotaSemaphore(workItem)
	if quotaSemaphore == nil {
		return nil, fmt.Sprintf("No quota for %v", workItem.instanceType)
	}
	if workItem.vcpus > max {
		return nil, fmt.Sprintf("Quota too small for %v", workItem.instanceType)
	}
	if err := quotaSemaphore.Acquire(ctx, workItem.vcpus); err != nil {
		return nil, err.Error()
	}

	return func() {
		quotaSemaphore.Release(workItem.vcpus)
	}, ""
}

func requestSpotInstance(ctx context.Context, workItem workOrder) string {
	var instances int32 = 1

	requestParams := &ec2.RequestSpotInstancesInput{
		DryRun:        aws.Bool(argDryRun),
		InstanceCount: &instances,
		// leave enough time for even metal instances to start and finish
		ValidUntil: aws.Time(time.Now().UTC().Add(time.Hour)),
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

	spotResponse, err := workItem.client.RequestSpotInstances(ctx, requestParams)

	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			if apiErr.ErrorCode() == "MaxSpotInstanceCountExceeded" {
				// retry? hopefully the semaphores protect us enough so this is not needed
			}
			if argDryRun && apiErr.ErrorCode() == "DryRunOperation" {
				return ""
			}
			return apiErr.ErrorMessage()
		}

		return err.Error()
	}

	spotRequest := spotResponse.SpotInstanceRequests[0]
	maxOpenTime := time.Now().Add(20 * time.Second)
	var maxActiveTime time.Time
	var maxActiveTerminated bool

	for {
		time.Sleep(30 * time.Second)

		// TODO batch multiple describe requests together?
		updatedStatus, err := workItem.client.DescribeSpotInstanceRequests(ctx, &ec2.DescribeSpotInstanceRequestsInput{
			SpotInstanceRequestIds: []string{*spotRequest.SpotInstanceRequestId},
		})

		if err != nil {
			return fmt.Sprintf("Error describing spot instance: %v", err)
		}

		spotRequest = updatedStatus.SpotInstanceRequests[0]

		switch spotRequest.State {
		case types.SpotInstanceStateOpen:
			if time.Now().After(maxOpenTime) {
				_, err := workItem.client.CancelSpotInstanceRequests(ctx, &ec2.CancelSpotInstanceRequestsInput{
					SpotInstanceRequestIds: []string{*spotRequest.SpotInstanceRequestId},
				})
				if err != nil {
					return fmt.Sprintf("Unable to cancel: %v", err)
				}
				return "Not fulfilled, probably due to spot cap is being too low"
			}
		case types.SpotInstanceStateActive:
			if !maxActiveTerminated {
				if maxActiveTime.IsZero() {
					if strings.Contains(string(workItem.instanceType), "metal") {
						// metal instances can take a long time to boot
						maxActiveTime = time.Now().Add(20 * time.Minute)
					} else {
						// give normal instances no more than three minutes
						maxActiveTime = time.Now().Add(3 * time.Minute)
					}
				} else {
					if time.Now().After(maxActiveTime) {
						_, err := workItem.client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
							InstanceIds: []string{*spotRequest.InstanceId},
						})
						if err != nil {
							return fmt.Sprintf("Unable to cancel: %v", err)
						}
						maxActiveTerminated = true
					}
				}
			}
		case types.SpotInstanceStateClosed:
			if *spotRequest.Status.Code != "instance-terminated-by-user" {
				return fmt.Sprintf("closed: %v", *spotRequest.Status.Code)
			}
			return ""
		case types.SpotInstanceStateCancelled:
			if *spotRequest.Status.Code != "instance-terminated-by-user" {
				return fmt.Sprintf("cancelled: %v", *spotRequest.Status.Code)
			}
			return ""
		case types.SpotInstanceStateFailed:
			return fmt.Sprintf("failed: %v", *spotRequest.Status.Code)
		}
	}
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
