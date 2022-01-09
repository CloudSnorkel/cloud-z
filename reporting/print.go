package reporting

import (
	"encoding/json"
	"fmt"
	sigar "github.com/cloudfoundry/gosigar"
	"github.com/inhies/go-bytesize"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/klauspost/cpuid/v2"
	"os"
	"strings"
)

func int2bytes(b int) string {
	//return bytesize.New(float64(b)).Format("%d", "", false)
	return bytesize.New(float64(b)).String()
}

func (report *Report) Print() {
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.SetAllowedRowLength(120)
	t.SetTitle("Instance Data")
	t.AppendRow(table.Row{"Cloud", report.Cloud})
	t.AppendRow(table.Row{"Instance type", report.InstanceType})
	t.AppendRow(table.Row{"Region", report.Region})
	t.AppendRow(table.Row{"Availability zone", report.AvailabilityZone})
	t.AppendRow(table.Row{"Instance id", report.InstanceId})
	t.AppendRow(table.Row{"Image id", report.ImageId})
	t.SetStyle(table.StyleColoredMagentaWhiteOnBlack)
	t.Render()

	report.printCPU()
	report.printMemory()
	report.printErrors()
}

func (report *Report) printCPU() {
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.SetAllowedRowLength(120)
	t.SetTitle("CPU")
	t.AppendRow(table.Row{"CPU", cpuid.CPU.BrandName})
	t.AppendRow(table.Row{"Vendor", cpuid.CPU.VendorString})
	t.AppendRow(table.Row{"Vendor ID", fmt.Sprintf("%v", cpuid.CPU.VendorID.String())})
	t.AppendRow(table.Row{"Family", fmt.Sprintf("%v", cpuid.CPU.Family)})
	t.AppendRow(table.Row{"MHz", fmt.Sprintf("%v", cpuid.CPU.Hz/1_000_000)})
	t.AppendRow(table.Row{"Logical cores", fmt.Sprintf("%v", cpuid.CPU.LogicalCores)})
	t.AppendRow(table.Row{"Physical cores", fmt.Sprintf("%v", cpuid.CPU.PhysicalCores)})
	t.AppendRow(table.Row{"Thread per core", fmt.Sprintf("%v", cpuid.CPU.ThreadsPerCore)})
	t.AppendRow(table.Row{"Boost frequency", fmt.Sprintf("%v", cpuid.CPU.BoostFreq)})
	t.AppendRow(table.Row{"L1 Cache", fmt.Sprintf("%v instruction, %v data", int2bytes(cpuid.CPU.Cache.L1I), int2bytes(cpuid.CPU.Cache.L1D))})
	t.AppendRow(table.Row{"L2 Cache", int2bytes(cpuid.CPU.Cache.L2)})
	t.AppendRow(table.Row{"L2 Cache", int2bytes(cpuid.CPU.Cache.L2)})
	t.AppendRow(table.Row{"L3 Cache", int2bytes(cpuid.CPU.Cache.L3)})
	t.AppendRow(table.Row{"Cache line", fmt.Sprintf("%v", cpuid.CPU.CacheLine)})
	t.AppendRow(table.Row{"Features", text.WrapSoft(strings.Join(cpuid.CPU.FeatureSet(), ", "), 80)})
	t.SetStyle(table.StyleColoredMagentaWhiteOnBlack)
	t.Render()
}

func (report *Report) printMemory() {
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.SetAllowedRowLength(120)
	t.SetTitle("Memory")
	t.AppendRow(table.Row{"Total RAM", sigar.FormatSize(report.Memory.Total) + "B"})
	rowConfigAutoMerge := table.RowConfig{AutoMerge: true}
	t.SetColumnConfigs([]table.ColumnConfig{
		{Number: 1, AutoMerge: true},
	})
	for i, stick := range report.Memory.Sticks {
		stickCol := fmt.Sprintf("Stick #%v", i+1)
		t.AppendRow(table.Row{stickCol, "Location", stick.Location}, rowConfigAutoMerge)
		t.AppendRow(table.Row{stickCol, "Type", stick.Type}, rowConfigAutoMerge)
		t.AppendRow(table.Row{stickCol, "Size", stick.Size}, rowConfigAutoMerge)
		t.AppendRow(table.Row{stickCol, "Data width", fmt.Sprintf("%v-bit", stick.DataWidth)}, rowConfigAutoMerge)
		t.AppendRow(table.Row{stickCol, "Total width", fmt.Sprintf("%v-bit", stick.TotalWidth)}, rowConfigAutoMerge)
		t.AppendRow(table.Row{stickCol, "Speed", fmt.Sprintf("%v MHz", stick.MHz)}, rowConfigAutoMerge)
	}
	t.SetStyle(table.StyleColoredMagentaWhiteOnBlack)
	t.Render()
}

func (report *Report) printErrors() {
	if len(report.Errors) == 0 {
		return
	}

	fmt.Println(text.Bold.Sprint("Errors:"))

	for _, err := range report.Errors {
		fmt.Println(text.FgRed.Sprintf("  %v", err))
	}
}

func (report *Report) PrintJson() {
	// TODO colors?
	result, _ := json.MarshalIndent(report, "", "  ")
	fmt.Print(string(result))
}
