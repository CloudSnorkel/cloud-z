package cmd

import (
	"cloud-z/providers"
	"fmt"
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
	"log"
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
		allCloudProviders := []providers.CloudProvider{
			&providers.AwsProvider{},
			&providers.GcpProvider{},
			&providers.AzureProvider{},
		}

		detectedCloud := false
		for _, provider := range allCloudProviders {
			// TODO detect faster with goroutines?
			if provider.Detect() {
				data, err := provider.GetData()
				if err != nil {
					log.Fatalln(err)
				}

				printTable(data)

				detectedCloud = true
			}
		}

		if !detectedCloud {
			println("Unable to detect cloud provider")
		}

		printTable(providers.GetCPUInfo())
	},
}

func printTable(data [][]string) {
	table := tablewriter.NewWriter(os.Stdout)
	for _, v := range data {
		table.Append(v)
	}
	table.Render()
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
