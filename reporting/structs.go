package reporting

type Report struct {
	Cloud            string                     `json:"cloud"`
	InstanceId       string                     `json:"-"`
	InstanceType     string                     `json:"instanceType"`
	ImageId          string                     `json:"-"`
	Region           string                     `json:"region"`
	AvailabilityZone string                     `json:"availabilityZone"`
	CPU              CpuReport                  `json:"cpu"`
	Memory           MemoryReport               `json:"memory"`
	Benchmarks       map[string]BenchmarkReport `json:"benchmarks"`
	Errors           []string                   `json:"errors,omitempty"`
}

type CpuReport struct {
	Description        string   `json:"description"`
	Vendor             string   `json:"vendor"`
	VendorId           string   `json:"vendorId"`
	Family             int      `json:"family"`
	MHz                int      `json:"mhz"`
	LogicalCores       int      `json:"logicalCores"`
	PhysicalCores      int      `json:"physicalCores"`
	ThreadsPerCore     int      `json:"threadsPerCore"`
	BoostFrequency     int      `json:"boostFrequency"`
	CacheL1Instruction int      `json:"cacheL1Instruction"`
	CacheL1Data        int      `json:"cacheL1Data"`
	CacheL2            int      `json:"cacheL2"`
	CacheL3            int      `json:"cacheL3"`
	CacheLine          int      `json:"cacheLine"`
	Features           []string `json:"features"`
}

type MemoryReport struct {
	Total  uint64              `json:"total"`
	Sticks []MemoryStickReport `json:"sticks"`
}

type MemoryStickReport struct {
	Location   string `json:"location"`
	Type       string `json:"type"`
	Size       uint16 `json:"size"`
	DataWidth  uint16 `json:"dataWidth"`
	TotalWidth uint16 `json:"totalWidth"`
	MHz        uint16 `json:"mhz"`
}

type UnitType string

const (
	Seconds UnitType = "seconds"
)

type BenchmarkReport struct {
	Version int      `json:"version"`
	Result  float64  `json:"result"`
	Unit    UnitType `json:"unit"`
}

func (report *Report) AddError(error string) {
	report.Errors = append(report.Errors, error)
}
