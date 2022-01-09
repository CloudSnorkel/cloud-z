package benchmarks

import (
	"cloud-z/reporting"
)

func AllBenchmarks(report *reporting.Report) {
	// TODO single and multi thread
	report.Benchmarks = map[string]reporting.BenchmarkReport{
		"fbench": {
			Version: 1,
			Result:  fbench(),
			Unit:    reporting.Seconds,
		},
	}
}
