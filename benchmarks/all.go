package benchmarks

import "fmt"

func AllBenchmarks() [][]string {
	return [][]string{
		// TODO single and multi thread
		{"fbench", fmt.Sprintf("%v seconds (lower is better)", fbench())},
	}
}
