package providers

import (
	"cloud-z/reporting"
	"github.com/klauspost/cpuid/v2"
)

func GetCPUInfo(report *reporting.Report) {
	report.CPU.Description = cpuid.CPU.BrandName
	report.CPU.Vendor = cpuid.CPU.VendorString
	report.CPU.VendorId = cpuid.CPU.VendorID.String()
	report.CPU.Family = cpuid.CPU.Family
	report.CPU.MHz = int(cpuid.CPU.Hz / 1_000_000)
	report.CPU.LogicalCores = cpuid.CPU.LogicalCores
	report.CPU.PhysicalCores = cpuid.CPU.PhysicalCores
	report.CPU.ThreadsPerCore = cpuid.CPU.ThreadsPerCore
	report.CPU.BoostFrequency = int(cpuid.CPU.BoostFreq)
	report.CPU.CacheL1Instruction = cpuid.CPU.Cache.L1I
	report.CPU.CacheL1Data = cpuid.CPU.Cache.L1D
	report.CPU.CacheL2 = cpuid.CPU.Cache.L2
	report.CPU.CacheL3 = cpuid.CPU.Cache.L3
	report.CPU.CacheLine = cpuid.CPU.CacheLine
	report.CPU.Features = cpuid.CPU.FeatureSet()
}
