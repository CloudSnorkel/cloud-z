package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/ratelimit"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
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
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	argDryRun                  = false
	argIncludeTypes            []string
	argExcludeTypes            []string
	argSpotCap                 float64
	argUseLocalInstancePricing = false
	argMaxPerRegion            int64
)

// regionSlots limits how many spot instances run at once in each region. The vCPU quota
// semaphores don't cover the account's spot instance count limit, so without this most
// requests fail with MaxSpotInstanceCountExceeded.
var regionSlots sync.Map // region -> *semaphore.Weighted

func grabRegionSlot(ctx context.Context, region string) (func(), error) {
	s, _ := regionSlots.LoadOrStore(region, semaphore.NewWeighted(argMaxPerRegion))
	sem := s.(*semaphore.Weighted)
	if err := sem.Acquire(ctx, 1); err != nil {
		return nil, err
	}
	return func() { sem.Release(1) }, nil
}

// ec2Retryer retries throttling (RequestLimitExceeded is already in the SDK's defaults) and
// MaxSpotInstanceCountExceeded, which clears up as other collector instances shut down. Both
// can take minutes, so allow more and longer attempts than the default 3 attempts / 20s.
// The SDK's retry token bucket is turned off: with hundreds of requests per region, it runs
// dry and fails requests with "retry quota exceeded" instead of waiting.
func ec2Retryer() aws.Retryer {
	return retry.AddWithErrorCodes(retry.NewStandard(func(o *retry.StandardOptions) {
		o.MaxAttempts = 6
		o.MaxBackoff = 3 * time.Minute
		o.RateLimiter = ratelimit.None
	}), "MaxSpotInstanceCountExceeded")
}

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
		hf, err := http.Get("https://instances.vantage.sh/instances.json")
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
			case nil:
				// No on-demand Linux price means the type isn't sold in that region (like m1.large
				// in GovCloud). Nothing to warn about: types without a price are skipped later anyway.
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
	pw.SetStyle(progress.StyleCircle)
	pw.Style().Colors = progress.StyleColorsExample
	pw.Style().Options.TimeDonePrecision = time.Second
	pw.Style().Options.TimeInProgressPrecision = time.Second
	pw.Style().Options.TimeOverallPrecision = time.Second
	if os.Getenv("GITHUB_ACTIONS") != "true" {
		pw.Style().Visibility.ETA = true
	}

	// The progress bar redraws itself several times a second. That only works on a terminal.
	// Anywhere else (like GitHub Actions) every redraw ends up as more lines in the log, so
	// print a plain status line every now and then instead.
	interactive := isTerminal(os.Stdout)
	var status atomic.Value // func() string
	stopStatus := make(chan struct{})
	if interactive {
		go pw.Render()
	} else {
		go func() {
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					if f, ok := status.Load().(func() string); ok {
						log.Println(f())
					}
				case <-stopStatus:
					return
				}
			}
		}()
	}

	// gather a collection of instance type and az combos to launch
	regionTracker := progress.Tracker{
		Message: "Planning work",
		Total:   int64(len(regions)),
	}
	pw.AppendTracker(&regionTracker)
	status.Store(func() string {
		return fmt.Sprintf("Planning work: %v/%v regions", regionTracker.Value(), regionTracker.Total)
	})

	var regionWaitGroup sync.WaitGroup
	var workChannel = make(chan workOrder, 10)
	for _, region := range regions {
		regionWaitGroup.Add(1)
		go func(region string) {
			defer regionWaitGroup.Done()
			defer regionTracker.Increment(1)

			// A region whose endpoints don't answer (like me-south-1 in Nov 2026) shouldn't
			// stall or fail the whole run, so give each region a few minutes and skip it on error.
			regionCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
			defer cancel()

			initializeSpotQuotasForRegion(regionCtx, cfg, region)
			if err := collectWork(regionCtx, cfg, region, allPricing, workChannel); err != nil {
				log.Printf("Skipping region %v: %v", region, err)
			}
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
	if !interactive {
		log.Printf("Planned %v spot instances%s", len(work), totalPriceDesc)
	}

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

	spotErrors := newMessageGroups()
	var totalSpotErrors int32 = 0
	var totalRunning int32 = 0

	status.Store(func() string {
		return fmt.Sprintf("%vRequesting spot instances: %v/%v done (%v running, %v failed)",
			prefix, workTracker.Value(), workTracker.Total, atomic.LoadInt32(&totalRunning), atomic.LoadInt32(&totalSpotErrors))
	})

	var workWaitGroup sync.WaitGroup
	var errorChannel = make(chan string, 10)

	for _, workItem := range work {
		workWaitGroup.Add(1)

		go func(workItem workOrder) {
			defer workWaitGroup.Done()
			defer workTracker.Increment(1)

			slotReleaser, slotErr := grabRegionSlot(ctx, workItem.region)
			if slotErr != nil {
				errorChannel <- workItem.describeError(slotErr.Error())
				return
			}
			defer slotReleaser()

			quotaReleaser, err := grabQuota(ctx, workItem)
			if err != "" {
				errorChannel <- workItem.describeError(err)
				return
			}
			defer quotaReleaser()

			atomic.AddInt32(&totalRunning, 1)
			defer atomic.AddInt32(&totalRunning, -1)

			workTracker.UpdateMessage(fmt.Sprintf("%vRequesting spot instance (%v running, %v failed)", prefix, totalRunning, totalSpotErrors))

			err = requestSpotInstance(ctx, workItem)
			if err != "" {
				errorChannel <- workItem.describeError(err)
			}
		}(workItem)
	}

	go func() {
		workWaitGroup.Wait()
		close(errorChannel)
	}()

	for err := range errorChannel {
		spotErrors.Add(err)
		atomic.AddInt32(&totalSpotErrors, 1)
		workTracker.UpdateMessage(fmt.Sprintf("%vRequesting spot instance (%v running, %v failed)", prefix, totalRunning, totalSpotErrors))
	}

	workTracker.UpdateMessage(fmt.Sprintf("%vRequested %v spot instances, %v failed", prefix, len(work), totalSpotErrors))
	workTracker.MarkAsDone()
	close(stopStatus)
	if !interactive {
		log.Printf("%vRequested %v spot instances, %v failed", prefix, len(work), totalSpotErrors)
	}

	// finish progress bar
	for pw.IsRenderInProgress() {
		if pw.LengthActive() == 0 {
			pw.Stop()
		}
		time.Sleep(time.Millisecond * 100)
	}

	// TODO print errors even if ctrl+c is hit
	if planningWarnings.Len() > 0 {
		fmt.Println("Skipped while planning:")
		planningWarnings.Write(os.Stdout)
	}
	if spotErrors.Len() > 0 {
		fmt.Println("Spot request errors:")
		spotErrors.Write(os.Stdout)
	}
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func regionClient(cfg aws.Config, region string) *ec2.Client {
	return ec2.NewFromConfig(cfg, func(o *ec2.Options) {
		o.Region = region
		o.Retryer = ec2Retryer()
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

func describeInstanceTypes(ctx context.Context, ec2Client *ec2.Client) ([]instanceDescriptor, error) {
	var result []instanceDescriptor

	paginator := ec2.NewDescribeInstanceTypesPaginator(ec2Client, &ec2.DescribeInstanceTypesInput{})
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
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

	return result, nil
}

func allAZs(ctx context.Context, ec2Client *ec2.Client) ([]string, error) {
	output, err := ec2Client.DescribeAvailabilityZones(ctx, &ec2.DescribeAvailabilityZonesInput{})
	if err != nil {
		return nil, err
	}

	var result []string
	for _, az := range output.AvailabilityZones {
		result = append(result, *az.ZoneName)
	}

	return result, nil
}

// findAmi returns the latest Amazon Linux 2023 AMI for the given architecture (x86_64 or arm64)
// from the public SSM parameters AWS maintains in every region.
func findAmi(ctx context.Context, cfg aws.Config, region string, arch string) (*string, error) {
	client := ssm.NewFromConfig(cfg, func(o *ssm.Options) {
		o.Region = region
	})

	output, err := client.GetParameter(ctx, &ssm.GetParameterInput{
		Name: aws.String(fmt.Sprintf("/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-%v", arch)),
	})
	if err != nil {
		return nil, err
	}

	return output.Parameter.Value, nil
}

// collectWork sends a work order for every matching instance type and AZ in the region. It only
// sends anything once every lookup succeeded, so a region that fails is skipped as a whole.
func collectWork(ctx context.Context, cfg aws.Config, region string, allPricing pricing, workChannel chan<- workOrder) error {
	x64ami, err := findAmi(ctx, cfg, region, "x86_64")
	if err != nil {
		return err
	}
	arm64ami, err := findAmi(ctx, cfg, region, "arm64")
	if err != nil {
		return err
	}
	client := regionClient(cfg, region)
	azs, err := allAZs(ctx, client)
	if err != nil {
		return err
	}
	instanceTypes, err := describeInstanceTypes(ctx, client)
	if err != nil {
		return err
	}

	// TODO combine with describe_instance_type_offerings so we don't start types where they are not available
	for _, it := range instanceTypes {
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
					planningWarnings.Add(fmt.Sprintf("Skipping %v @ %v as price is missing", it.type_, az))
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

	return nil
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
		if err != nil || quotaDetails.Quota.Value == nil {
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

// describeError adds the instance type and AZ to an error, since many AWS errors (like "Max spot
// instance count exceeded") don't say which request they belong to. The error summary groups
// errors by everything else and lists the types and AZs that hit each one.
func (w workOrder) describeError(err string) string {
	return fmt.Sprintf("%v @ %v: %v", w.instanceType, w.availabilityZone, err)
}

func requestSpotInstance(ctx context.Context, workItem workOrder) string {
	var instances int32 = 1

	requestParams := &ec2.RunInstancesInput{
		DryRun:                            aws.Bool(argDryRun),
		MinCount:                          &instances,
		MaxCount:                          &instances,
		ImageId:                           workItem.ami,
		InstanceType:                      workItem.instanceType,
		InstanceInitiatedShutdownBehavior: types.ShutdownBehaviorTerminate,
		InstanceMarketOptions: &types.InstanceMarketOptionsRequest{
			MarketType: types.MarketTypeSpot,
			SpotOptions: &types.SpotMarketOptions{
				SpotInstanceType: types.SpotInstanceTypeOneTime,
			},
		},
		Placement: &types.Placement{
			AvailabilityZone: aws.String(workItem.availabilityZone),
		},
		// Require IMDSv2. Cloud-Z asks for a session token before reading instance metadata.
		MetadataOptions: &types.InstanceMetadataOptionsRequest{
			HttpEndpoint:            types.InstanceMetadataEndpointStateEnabled,
			HttpTokens:              types.HttpTokensStateRequired,
			HttpPutResponseHopLimit: aws.Int32(1),
		},
		UserData: aws.String(userData(workItem.downloadUrl)),
		TagSpecifications: []types.TagSpecification{
			{
				ResourceType: types.ResourceTypeInstance,
				Tags: []types.Tag{
					{
						Key:   aws.String("Name"),
						Value: aws.String("Cloud-Z collector"),
					},
				},
			},
		},
	}

	if workItem.priceCap != noPriceCap {
		requestParams.InstanceMarketOptions.SpotOptions.MaxPrice = aws.String(fmt.Sprintf("%.05f", workItem.priceCap))
	}

	runResponse, err := workItem.client.RunInstances(ctx, requestParams)

	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			if argDryRun && apiErr.ErrorCode() == "DryRunOperation" {
				return ""
			}
			return apiErr.ErrorMessage()
		}

		return err.Error()
	}

	instanceId := runResponse.Instances[0].InstanceId
	var maxActiveTime time.Time
	var maxActiveTerminated bool

	for {
		time.Sleep(30 * time.Second)

		// TODO batch multiple describe requests together?
		updatedStatus, err := workItem.client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
			InstanceIds: []string{*instanceId},
		})

		if err != nil {
			return fmt.Sprintf("Error describing spot instance: %v", err)
		}

		state := updatedStatus.Reservations[0].Instances[0].State.Name

		switch state {
		case types.InstanceStateNameRunning:
			fallthrough
		case types.InstanceStateNamePending:
			fallthrough
		case types.InstanceStateNameStopping:
			fallthrough
		case types.InstanceStateNameStopped:
			fallthrough
		case types.InstanceStateNameShuttingDown:
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
							InstanceIds: []string{*instanceId},
						})
						if err != nil {
							return fmt.Sprintf("Unable to cancel: %v", err)
						}
						maxActiveTerminated = true
					}
				}
			}
		case types.InstanceStateNameTerminated:
			stateReason := *updatedStatus.Reservations[0].Instances[0].StateReason.Code
			if stateReason != "Client.InstanceInitiatedShutdown" {
				return fmt.Sprintf("Terminated: %v", stateReason)
			}
			return ""
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
	rootCmd.Flags().Int64VarP(&argMaxPerRegion, "max-per-region", "m", 20, "Max spot instances running at once in each region")
	rootCmd.Flags().BoolVarP(&argUseLocalInstancePricing, "local-pricing", "l", false, "Use local copy of instances.json")
	if err := rootCmd.Execute(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// messageGroups counts messages that only differ in small details, so thousands of errors like
// "Your Spot request price of 0.01736 is lower than ... price of 0.0182." print as one line.
//
// Any word with a digit in it is treated as a detail: prices, instance types (c6i.xlarge),
// availability zones (us-east-1a), account and request IDs, quota codes and so on. Messages with
// the same text around their details share a group. Comma separated lists of details (like a list
// of AZs) count as one detail, so lists of different lengths still group together.
type messageGroups struct {
	mu     sync.Mutex
	groups map[string]*messageGroup
}

type messageGroup struct {
	template string     // the message with detailMarker in place of each detail
	count    uint       // number of messages in the group
	details  [][]string // details[i][m] is the i-th detail of the m-th message
}

const detailMarker = "\x00"

// planningWarnings collects types skipped while planning, so they print once at the end as a
// short summary instead of a line for every AZ.
var planningWarnings = newMessageGroups()

var detailPattern = regexp.MustCompile(`[\w.:/-]*\d[\w.:/-]*`)

func newMessageGroups() *messageGroups {
	return &messageGroups{groups: make(map[string]*messageGroup)}
}

// splitDetails returns msg with every detail replaced by detailMarker, and the details in order.
func splitDetails(msg string) (string, []string) {
	var template strings.Builder
	var details []string
	last, detailStart := 0, 0
	for _, loc := range detailPattern.FindAllStringIndex(msg, -1) {
		start, end := loc[0], loc[1]
		// punctuation at the end belongs to the sentence ("price of 0.0182."), not the detail
		for strings.ContainsRune(".:/-", rune(msg[end-1])) {
			end--
		}
		between := msg[last:start]
		if len(details) > 0 && (between == ", " || between == ",") {
			// the next item in a list of details
			details[len(details)-1] = msg[detailStart:end]
		} else {
			template.WriteString(between)
			template.WriteString(detailMarker)
			details = append(details, msg[start:end])
			detailStart = start
		}
		last = end
	}
	template.WriteString(msg[last:])
	return template.String(), details
}

func (mg *messageGroups) Add(msg string) {
	template, details := splitDetails(msg)

	mg.mu.Lock()
	defer mg.mu.Unlock()

	g, ok := mg.groups[template]
	if !ok {
		g = &messageGroup{template: template, details: make([][]string, len(details))}
		mg.groups[template] = g
	}
	g.count++
	for i, d := range details {
		g.details[i] = append(g.details[i], d)
	}
}

func (mg *messageGroups) Len() int {
	mg.mu.Lock()
	defer mg.mu.Unlock()
	return len(mg.groups)
}

// Write prints one line per group, most common first. A detail that is the same in every message
// of the group is printed as is, so a group of one prints exactly like the original message.
// Details that vary are shown as <A>, <B>, ... with a summary of their values below: the range for
// numbers, or the most common values for anything else.
//
//	[ 2212] Your Spot request price of <A> is lower than the minimum required Spot request fulfillment price of <B>.
//	        <A>: 0.00123 to 4.512
//	        <B>: 0.0015 to 5.01
func (mg *messageGroups) Write(w io.Writer) {
	mg.mu.Lock()
	defer mg.mu.Unlock()

	groups := make([]*messageGroup, 0, len(mg.groups))
	for _, g := range mg.groups {
		groups = append(groups, g)
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].count != groups[j].count {
			return groups[i].count > groups[j].count
		}
		return groups[i].template < groups[j].template
	})

	for _, g := range groups {
		line, notes := g.describe()
		fmt.Fprintf(w, "[%5d] %v\n", g.count, line)
		for _, note := range notes {
			fmt.Fprintf(w, "        %v\n", note)
		}
	}
}

func (g *messageGroup) describe() (string, []string) {
	labels := make([]string, len(g.details))
	var notes []string
	nextLabel := 'A'

	for i, values := range g.details {
		if allSame(values) {
			labels[i] = values[0]
			continue
		}
		// the same detail often shows up twice ("sufficient c6i.xlarge capacity ... get c6i.xlarge capacity")
		for j := 0; j < i; j++ {
			if slices.Equal(values, g.details[j]) {
				labels[i] = labels[j]
				break
			}
		}
		if labels[i] != "" {
			continue
		}
		labels[i] = fmt.Sprintf("<%c>", nextLabel)
		if nextLabel < 'Z' {
			nextLabel++
		}
		notes = append(notes, fmt.Sprintf("%v: %v", labels[i], summarizeValues(values)))
	}

	parts := strings.Split(g.template, detailMarker)
	var line strings.Builder
	for i, part := range parts {
		line.WriteString(part)
		if i < len(labels) {
			line.WriteString(labels[i])
		}
	}
	return line.String(), notes
}

// summarizeValues describes the values of one detail: "min to max" when they are all numbers,
// otherwise the most common values with their counts.
func summarizeValues(values []string) string {
	lo, hi, numeric := 0.0, 0.0, true
	for i, v := range values {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			numeric = false
			break
		}
		if i == 0 || f < lo {
			lo = f
		}
		if i == 0 || f > hi {
			hi = f
		}
	}
	if numeric {
		return fmt.Sprintf("%v to %v", lo, hi)
	}

	counts := make(map[string]int)
	for _, v := range values {
		counts[v]++
	}
	distinct := make([]string, 0, len(counts))
	for v := range counts {
		distinct = append(distinct, v)
	}
	sort.Slice(distinct, func(i, j int) bool {
		if counts[distinct[i]] != counts[distinct[j]] {
			return counts[distinct[i]] > counts[distinct[j]]
		}
		return distinct[i] < distinct[j]
	})

	const shown = 5
	if len(distinct) > shown && counts[distinct[0]] == 1 {
		// nothing repeats (request IDs and such), so a list of examples wouldn't tell much
		return fmt.Sprintf("%v different values, like %v", len(distinct), distinct[0])
	}
	var examples []string
	for i, v := range distinct {
		if i == shown {
			break
		}
		if counts[v] > 1 {
			v = fmt.Sprintf("%v (%v)", v, counts[v])
		}
		examples = append(examples, v)
	}
	summary := strings.Join(examples, " | ")
	if len(distinct) > shown {
		summary += fmt.Sprintf(" | ...and %v more", len(distinct)-shown)
	}
	return summary
}

func allSame(values []string) bool {
	for _, v := range values[1:] {
		if v != values[0] {
			return false
		}
	}
	return true
}
