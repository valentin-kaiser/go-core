//go:build linux

package machine

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/valentin-kaiser/go-core/v2/apperror"
)

const (
	machineIDPrefix = "machine:"

	procCPUInfoPath           = "/proc/cpuinfo"
	dmiProductUUIDPath        = "/sys/class/dmi/id/product_uuid"
	virtualDMIProductUUIDPath = "/sys/devices/virtual/dmi/id/product_uuid"
	dmiBoardSerialPath        = "/sys/class/dmi/id/board_serial"
	virtualDMIBoardSerialPath = "/sys/devices/virtual/dmi/id/board_serial"
	machineIDPath             = "/etc/machine-id"
	dbusMachineIDPath         = "/var/lib/dbus/machine-id"
	sysClassNetPath           = "/sys/class/net"
	sysBlockPath              = "/sys/block"

	cpuInfoSeparator = ":"
	cpuInfoParts     = 2
	cpuInfoProcessor = "processor"
	cpuInfoVendorID  = "vendor_id"
	cpuInfoModelName = "model name"
	cpuInfoFlags     = "flags"
	cpuIDFormat      = "%s:%s:%s:%s"

	nullUUID          = "00000000-0000-0000-0000-000000000000"
	loopbackInterface = "lo"
	macAddressFile    = "address"
	loopDevicePrefix  = "loop"
	blockDeviceDir    = "device"
	blockSerialFile   = "serial"

	lsblkCommand      = "lsblk"
	lsblkNoPartitions = "-d"
	lsblkNoHeadings   = "-n"
	lsblkOutputFlag   = "-o"
	lsblkSerialColumn = "SERIAL"
)

// collectHardwareIdentifiersWithOptions gathers Linux-specific hardware identifiers based on generator config
func collectHardwareIdentifiersWithOptions(g *generator) ([]string, error) {
	if g == nil {
		return nil, apperror.NewError("generator cannot be nil")
	}

	var collectors []func() []string

	if g.includeCPU {
		collectors = append(collectors, func() []string { return collectIfValid(getLinuxCPUID, cpuPrefix) })
	}
	if g.includeSystemUUID {
		collectors = append(collectors, func() []string { return collectIfValid(getLinuxSystemUUID, uuidPrefix) })
		collectors = append(collectors, func() []string { return collectIfValid(getLinuxMachineID, machineIDPrefix) })
	}
	if g.includeMotherboard {
		collectors = append(collectors, func() []string { return collectIfValid(getLinuxMotherboardSerial, motherboardPrefix) })
	}
	if g.includeMAC {
		collectors = append(collectors, func() []string { return collectMultipleIfValid(getLinuxMACAddresses, macPrefix) })
	}
	if g.includeDisk {
		collectors = append(collectors, func() []string { return collectMultipleIfValid(getLinuxDiskSerials, diskPrefix) })
	}

	return collectFromAll(collectors), nil
}

// collectIfValid adds single identifier with prefix if valid
func collectIfValid(getValue func() (string, error), prefix string) []string {
	value, err := getValue()
	switch {
	case err != nil, value == "":
		return nil
	default:
		return []string{prefix + value}
	}
}

// collectMultipleIfValid adds multiple identifiers with prefix if valid
func collectMultipleIfValid(getValues func() ([]string, error), prefix string) []string {
	values, err := getValues()
	switch {
	case err != nil, len(values) == 0:
		return nil
	default:
		return prefixSlice(values, prefix)
	}
}

// prefixSlice adds prefix to each string in slice
func prefixSlice(values []string, prefix string) []string {
	result := make([]string, len(values))
	for i, value := range values {
		result[i] = prefix + value
	}
	return result
}

// collectFromAll runs all collectors and combines results
func collectFromAll(collectors []func() []string) []string {
	var identifiers []string
	for _, collector := range collectors {
		identifiers = append(identifiers, collector()...)
	}
	return identifiers
}

// getLinuxCPUID retrieves CPU information from /proc/cpuinfo
func getLinuxCPUID() (string, error) {
	data, err := os.ReadFile(procCPUInfoPath)
	switch err {
	case nil:
		return parseCPUInfo(string(data)), nil
	default:
		return "", err
	}
}

// parseCPUInfo extracts CPU information from /proc/cpuinfo content
func parseCPUInfo(content string) string {
	lines := strings.Split(content, lineSeparator)
	var processor, vendorID, modelName, flags string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		parts := strings.SplitN(line, cpuInfoSeparator, cpuInfoParts)
		switch {
		case len(parts) != cpuInfoParts:
			continue
		case strings.HasPrefix(line, cpuInfoProcessor):
			processor = strings.TrimSpace(parts[1])
		case strings.HasPrefix(line, cpuInfoVendorID):
			vendorID = strings.TrimSpace(parts[1])
		case strings.HasPrefix(line, cpuInfoModelName):
			modelName = strings.TrimSpace(parts[1])
		case strings.HasPrefix(line, cpuInfoFlags):
			flags = strings.TrimSpace(parts[1])
		}
	}

	return fmt.Sprintf(cpuIDFormat, processor, vendorID, modelName, flags)
}

// getLinuxSystemUUID retrieves system UUID from DMI
func getLinuxSystemUUID() (string, error) {
	locations := []string{
		dmiProductUUIDPath,
		virtualDMIProductUUIDPath,
	}

	return readFirstValidFromLocations(locations, isValidUUID)
}

// getLinuxMotherboardSerial retrieves motherboard serial number from DMI
func getLinuxMotherboardSerial() (string, error) {
	locations := []string{
		dmiBoardSerialPath,
		virtualDMIBoardSerialPath,
	}

	return readFirstValidFromLocations(locations, isValidSerial)
}

// getLinuxMachineID retrieves systemd machine ID
func getLinuxMachineID() (string, error) {
	locations := []string{
		machineIDPath,
		dbusMachineIDPath,
	}

	return readFirstValidFromLocations(locations, isNonEmpty)
}

// readFirstValidFromLocations reads from multiple locations until valid value found
func readFirstValidFromLocations(locations []string, validator func(string) bool) (string, error) {
	for _, location := range locations {
		data, err := os.ReadFile(location)
		switch err {
		case nil:
			value := strings.TrimSpace(string(data))
			switch validator(value) {
			case true:
				return value, nil
			}
		}
	}
	return "", apperror.NewError("valid value not found in any location")
}

// isValidUUID checks if UUID is valid (not empty or null)
func isValidUUID(uuid string) bool {
	switch uuid {
	case "", nullUUID:
		return false
	default:
		return true
	}
}

// isValidSerial checks if serial is valid (not empty or placeholder)
func isValidSerial(serial string) bool {
	switch serial {
	case "", placeholderSerial:
		return false
	default:
		return true
	}
}

// isNonEmpty checks if value is not empty
func isNonEmpty(value string) bool {
	return value != ""
}

// getLinuxMACAddresses retrieves MAC addresses from network interfaces
func getLinuxMACAddresses() ([]string, error) {
	var macs []string

	netDir := sysClassNetPath
	entries, err := os.ReadDir(netDir)
	if err != nil {
		return nil, apperror.NewErrorf("reading %s failed", netDir).AddError(err)
	}

	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == loopbackInterface {
			continue
		}
		data, err := os.ReadFile(filepath.Join(netDir, entry.Name(), macAddressFile))
		if err != nil {
			continue
		}
		mac := strings.TrimSpace(string(data))
		if mac != "" && mac != zeroMAC {
			macs = append(macs, mac)
		}
	}

	return macs, nil
}

// getLinuxDiskSerials retrieves disk serial numbers using various methods
func getLinuxDiskSerials() ([]string, error) {
	var serials []string

	if lsblkSerials, err := getLinuxDiskSerialsLSBLK(); err == nil && len(lsblkSerials) > 0 {
		serials = append(serials, lsblkSerials...)
	}

	if sysSerials, err := getLinuxDiskSerialsSys(); err == nil && len(sysSerials) > 0 {
		serials = append(serials, sysSerials...)
	}

	return serials, nil
}

// getLinuxDiskSerialsLSBLK retrieves disk serials using lsblk command
func getLinuxDiskSerialsLSBLK() ([]string, error) {
	cmd := exec.Command(lsblkCommand, lsblkNoPartitions, lsblkNoHeadings, lsblkOutputFlag, lsblkSerialColumn)
	output, err := cmd.Output()
	if err != nil {
		return nil, apperror.NewError("running lsblk failed").AddError(err)
	}

	var serials []string
	lines := strings.Split(string(output), lineSeparator)
	for _, line := range lines {
		serial := strings.TrimSpace(line)
		if serial != "" {
			serials = append(serials, serial)
		}
	}

	return serials, nil
}

// getLinuxDiskSerialsSys retrieves disk serials from /sys/block
func getLinuxDiskSerialsSys() ([]string, error) {
	var serials []string

	blockDir := sysBlockPath
	entries, err := os.ReadDir(blockDir)
	if err != nil {
		return nil, apperror.NewErrorf("reading %s failed", blockDir).AddError(err)
	}

	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), loopDevicePrefix) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(blockDir, entry.Name(), blockDeviceDir, blockSerialFile))
		if err != nil {
			continue
		}
		serial := strings.TrimSpace(string(data))
		if serial != "" {
			serials = append(serials, serial)
		}
	}

	return serials, nil
}
