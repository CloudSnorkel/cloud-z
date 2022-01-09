package cmd

import (
	"cloud-z/benchmarks"
	"cloud-z/providers"
	"cloud-z/reporting"
	"fmt"
	"github.com/spf13/cobra"
	"os"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
	builtBy = "unknown"
)

var rootCmd = &cobra.Command{
	Use:     "cloud-z",
	Short:   "Cloud-Z gathers information on cloud instances",
	Version: fmt.Sprintf("%s, commit %s, built at %s by %s", version, commit, date, builtBy),
	Run: func(cmd *cobra.Command, args []string) {
		report := &reporting.Report{}

		allCloudProviders := []providers.CloudProvider{
			&providers.AwsProvider{},
			&providers.GcpProvider{},
			&providers.AzureProvider{},
		}

		detectedCloud := false
		for _, provider := range allCloudProviders {
			// TODO detect faster with goroutines?
			if provider.Detect() {
				provider.GetData(report)
				detectedCloud = true
			}
		}

		if !detectedCloud {
			report.AddError("Unable to detect cloud provider")
		}

		providers.GetCPUInfo(report)
		providers.GetMemoryInfo(report)
		benchmarks.AllBenchmarks(report)

		report.Print()

		fmt.Println()

		submitOrViewOrNo := ask("Would you like to anonymously contribute this data to https://z.cloudsnorkel.com/? Your IP address may be logged, but instance id and other PII will not be sent.", map[rune]string{'y': "yes", 'n': "no", 'v': "view JSON"}, 'n')
		if submitOrViewOrNo == 'v' {
			report.PrintJson()
			submitOrViewOrNo = ask("Ok to submit?", map[rune]string{'y': "yes", 'n': "no"}, 'n')
		}
		if submitOrViewOrNo == 'y' {
			report.Send()
		}
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
