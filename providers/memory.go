package providers

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"github.com/cloudfoundry/gosigar"
	"github.com/digitalocean/go-smbios/smbios"
	"io"
	"log"
)

// https://www.dmtf.org/sites/default/files/standards/documents/DSP0134_3.1.1.pdf
type MemoryDevice struct {
	Location                     string
	PhysicalMemoryArrayHandle    uint16
	MemoryErrorInformationHandle uint16
	TotalWidth                   uint16
	DataWidth                    uint16
	Size                         uint16
	FormFactor                   uint8
	DeviceSet                    uint8
	DeviceLocator                string
	BankLocator                  string
	MemoryType                   uint8
	TypeDetail                   uint16
	Speed                        uint16
	Manufacturer                 string
	SerialNumber                 string
	AssetTag                     string
	PartNumber                   string
	Attributes                   uint8
	ExtendedSize                 uint32
	ConfiguredMemoryClockSpeed   uint16
	MinimumVoltage               uint16
	MaximumVoltage               uint16
	ConfiguredVoltage            uint16
}

func (mem *MemoryDevice) formFactor() string {
	factors := []string{
		"<BAD VALUE>",
		"Other",
		"Unknown",
		"SIMM",
		"SIP",
		"Chip",
		"DIP",
		"ZIP",
		"Proprietary Card",
		"DIMM",
		"TSOP",
		"Row of chips",
		"RIMM",
		"SODIMM",
		"SRIMM",
		"FB-DIMM",
	}

	if int(mem.FormFactor) > len(factors) {
		return "<BAD VALUE>"
	}

	return factors[mem.FormFactor]
}

func (mem *MemoryDevice) memoryType() string {
	types := []string{
		"<BAD VALUE>",
		"Other",
		"Unknown",
		"DRAM",
		"EDRAM",
		"VRAM",
		"SRAM",
		"RAM",
		"ROM",
		"FLASH",
		"EEPROM",
		"FEPROM",
		"EPROM",
		"CDRAM",
		"3DRAM",
		"SDRAM",
		"SGRAM",
		"RDRAM",
		"DDR",
		"DDR2",
		"DDR2 FB-DIMM",
		"Reserved",
		"Reserved",
		"Reserved",
		"DDR3",
		"FBD2",
		"DDR4",
		"LPDDR",
		"LPDDR2",
		"LPDDR3",
		"LPDDR4",
	}

	if int(mem.MemoryType) > len(types) {
		return "<BAD VALUE>"
	}

	return types[mem.MemoryType]
}

func (mem *MemoryDevice) size() string {
	if mem.Size != 0x7fff {
		if mem.Size&0x8000 != 0 {
			return fmt.Sprintf("%vKB", mem.Size&0x7fff)
		}
		return fmt.Sprintf("%vMB", mem.Size&0x7fff)
	}

	return fmt.Sprintf("%vMB", mem.ExtendedSize)
}

func readString(reader io.Reader, strings []string) string {
	var stringId uint8
	binary.Read(reader, binary.LittleEndian, &stringId)
	if int(stringId) < len(strings) {
		return strings[stringId]
	}
	return ""
}

func GetMemoryInfo() [][]string {
	mem := sigar.Mem{}
	mem.Get()
	result := [][]string{
		{"Total RAM", sigar.FormatSize(mem.Total) + "B"},
	}

	// Find SMBIOS data in operating system-specific location.
	rc, _, err := smbios.Stream()
	if err != nil {
		log.Printf("Failed to open SMBIOS stream: %v\n", err)
		return result
	}
	// Be sure to close the stream!
	defer rc.Close()

	// Decode SMBIOS structures from the stream.
	d := smbios.NewDecoder(rc)
	records, err := d.Decode()
	if err != nil {
		log.Printf("Failed to decode SMBIOS structures: %v\n", err)
		return result
	}

	i := 1
	for _, record := range records {
		if record.Header.Type == 17 {
			memDevice := readMemoryDevice(record)
			prefix := fmt.Sprintf("Stick #%v: ", i)

			result = append(result, []string{prefix + "location", memDevice.Location})
			result = append(result, []string{prefix + "type", memDevice.memoryType() + " " + memDevice.formFactor()})
			result = append(result, []string{prefix + "size", memDevice.size()})
			result = append(result, []string{prefix + "data width", fmt.Sprintf("%v-bit", memDevice.DataWidth)})
			result = append(result, []string{prefix + "total width", fmt.Sprintf("%v-bit", memDevice.TotalWidth)})
			result = append(result, []string{prefix + "speed", fmt.Sprintf("%v MHz", memDevice.Speed)})

			i += 1
		}
	}

	return result
}

func readMemoryDevice(record *smbios.Structure) MemoryDevice {
	memDevice := MemoryDevice{}
	recordBytes := bytes.NewReader(record.Formatted)

	binary.Read(recordBytes, binary.LittleEndian, &memDevice.MemoryErrorInformationHandle)
	binary.Read(recordBytes, binary.LittleEndian, &memDevice.PhysicalMemoryArrayHandle)
	binary.Read(recordBytes, binary.LittleEndian, &memDevice.TotalWidth)
	binary.Read(recordBytes, binary.LittleEndian, &memDevice.DataWidth)
	binary.Read(recordBytes, binary.LittleEndian, &memDevice.Size)
	binary.Read(recordBytes, binary.LittleEndian, &memDevice.FormFactor)
	binary.Read(recordBytes, binary.LittleEndian, &memDevice.DeviceSet)
	memDevice.DeviceLocator = readString(recordBytes, record.Strings)
	memDevice.BankLocator = readString(recordBytes, record.Strings)
	binary.Read(recordBytes, binary.LittleEndian, &memDevice.MemoryType)
	binary.Read(recordBytes, binary.LittleEndian, &memDevice.TypeDetail)
	binary.Read(recordBytes, binary.LittleEndian, &memDevice.Speed)
	memDevice.Manufacturer = readString(recordBytes, record.Strings)
	memDevice.SerialNumber = readString(recordBytes, record.Strings)
	memDevice.AssetTag = readString(recordBytes, record.Strings)
	memDevice.PartNumber = readString(recordBytes, record.Strings)
	binary.Read(recordBytes, binary.LittleEndian, &memDevice.Attributes)
	binary.Read(recordBytes, binary.LittleEndian, &memDevice.ExtendedSize)
	binary.Read(recordBytes, binary.LittleEndian, &memDevice.ConfiguredMemoryClockSpeed)
	binary.Read(recordBytes, binary.LittleEndian, &memDevice.MinimumVoltage)
	binary.Read(recordBytes, binary.LittleEndian, &memDevice.MaximumVoltage)
	binary.Read(recordBytes, binary.LittleEndian, &memDevice.ConfiguredVoltage)

	memDevice.Location = record.Strings[0]

	return memDevice
}
