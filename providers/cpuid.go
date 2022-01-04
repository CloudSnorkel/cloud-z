package providers

import (
	"fmt"
	"github.com/inhies/go-bytesize"
	"github.com/klauspost/cpuid/v2"
	"strings"
)

func bytes(b int) string {
	//return bytesize.New(float64(b)).Format("%d", "", false)
	return bytesize.New(float64(b)).String()
}

func GetCPUInfo() [][]string {
	return [][]string{
		{"CPU", cpuid.CPU.BrandName},
		{"Vendor", cpuid.CPU.VendorString},
		{"Vendor ID", fmt.Sprintf("%v", cpuid.CPU.VendorID.String())},
		{"Family", fmt.Sprintf("%v", cpuid.CPU.Family)},
		{"MHz", fmt.Sprintf("%v", cpuid.CPU.Hz/1_000_000)},
		{"Logical cores", fmt.Sprintf("%v", cpuid.CPU.LogicalCores)},
		{"Physical cores", fmt.Sprintf("%v", cpuid.CPU.PhysicalCores)},
		{"Thread per core", fmt.Sprintf("%v", cpuid.CPU.ThreadsPerCore)},
		{"Boost frequency", fmt.Sprintf("%v", cpuid.CPU.BoostFreq)},
		{"L1 Cache", fmt.Sprintf("%v instruction, %v data", bytes(cpuid.CPU.Cache.L1I), bytes(cpuid.CPU.Cache.L1D))},
		{"L2 Cache", bytes(cpuid.CPU.Cache.L2)},
		{"L2 Cache", bytes(cpuid.CPU.Cache.L2)},
		{"L3 Cache", bytes(cpuid.CPU.Cache.L3)},
		{"Cache line", fmt.Sprintf("%v", cpuid.CPU.CacheLine)},
		{"Features", strings.Join(cpuid.CPU.FeatureSet(), ", ")},
	}
}
