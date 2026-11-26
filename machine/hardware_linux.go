//go:build linux

package machine

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/valentin-kaiser/go-core/apperror"
)

// collectHardwareIdentifiersWithOptions gathers Linux-specific hardware identifiers based on generator config
func collectHardwareIdentifiersWithOptions(g *generator) ([]string, error) {
	if g == nil {
		return nil, apperror.NewError("generator cannot be nil")
	}

	var collectors []func() []string

	if g.includeCPU {
		collectors = append(collectors, func() []string { return collectIfValid(getLinuxCPUID, "cpu:") })
	}
	if g.includeSystemUUID {
		collectors = append(collectors, func() []string { return collectIfValid(getLinuxSystemUUID, "uuid:") })
		collectors = append(collectors, func() []string { return collectIfValid(getLinuxMachineID, "machine:") })
	}
	if g.includeMotherboard {
		collectors = append(collectors, func() []string { return collectIfValid(getLinuxMotherboardSerial, "mb:") })
	}
	if g.includeMAC {
		collectors = append(collectors, func() []string { return collectMultipleIfValid(getLinuxMACAddresses, "mac:") })
	}
	if g.includeDisk {
		collectors = append(collectors, func() []string { return collectMultipleIfValid(getLinuxDiskSerials, "disk:") })
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
	data, err := os.ReadFile("/proc/cpuinfo")
	switch err {
	case nil:
		return parseCPUInfo(string(data)), nil
	default:
		return "", err
	}
}

// parseCPUInfo extracts CPU information from /proc/cpuinfo content
func parseCPUInfo(content string) string {
	lines := strings.Split(content, "\n")
	var processor, vendorID, modelName, flags string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		parts := strings.SplitN(line, ":", 2)
		switch {
		case len(parts) != 2:
			continue
		case strings.HasPrefix(line, "processor"):
			processor = strings.TrimSpace(parts[1])
		case strings.HasPrefix(line, "vendor_id"):
			vendorID = strings.TrimSpace(parts[1])
		case strings.HasPrefix(line, "model name"):
			modelName = strings.TrimSpace(parts[1])
		case strings.HasPrefix(line, "flags"):
			flags = strings.TrimSpace(parts[1])
		}
	}

	// Combine CPU information for unique identifier
	return fmt.Sprintf("%s:%s:%s:%s", processor, vendorID, modelName, flags)
}

// getLinuxSystemUUID retrieves system UUID from DMI
func getLinuxSystemUUID() (string, error) {
	// Try multiple locations for system UUID
	locations := []string{
		"/sys/class/dmi/id/product_uuid",
		"/sys/devices/virtual/dmi/id/product_uuid",
	}

	return readFirstValidFromLocations(locations, isValidUUID)
}

// getLinuxMotherboardSerial retrieves motherboard serial number from DMI
func getLinuxMotherboardSerial() (string, error) {
	locations := []string{
		"/sys/class/dmi/id/board_serial",
		"/sys/devices/virtual/dmi/id/board_serial",
	}

	return readFirstValidFromLocations(locations, isValidSerial)
}

// getLinuxMachineID retrieves systemd machine ID
func getLinuxMachineID() (string, error) {
	locations := []string{
		"/etc/machine-id",
		"/var/lib/dbus/machine-id",
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
	case "", "00000000-0000-0000-0000-000000000000":
		return false
	default:
		return true
	}
}

// isValidSerial checks if serial is valid (not empty or placeholder)
func isValidSerial(serial string) bool {
	switch serial {
	case "", "To be filled by O.E.M.":
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

	// Read from /sys/class/net
	netDir := "/sys/class/net"
	entries, err := os.ReadDir(netDir)
	if err != nil {
		return nil, apperror.NewErrorf("reading %s failed", netDir).AddError(err)
	}

	for _, entry := range entries {
		if entry.IsDir() && entry.Name() != "lo" { // Skip loopback
			macFile := filepath.Join(netDir, entry.Name(), "address")
			if data, err := os.ReadFile(macFile); err == nil {
				mac := strings.TrimSpace(string(data))
				if mac != "" && mac != "00:00:00:00:00:00" {
					macs = append(macs, mac)
				}
			}
		}
	}

	return macs, nil
}

// getLinuxDiskSerials retrieves disk serial numbers using various methods
func getLinuxDiskSerials() ([]string, error) {
	var serials []string

	// Try using lsblk command first
	if lsblkSerials, err := getLinuxDiskSerialsLSBLK(); err == nil && len(lsblkSerials) > 0 {
		serials = append(serials, lsblkSerials...)
	}

	// Try reading from /sys/block
	if sysSerials, err := getLinuxDiskSerialsSys(); err == nil && len(sysSerials) > 0 {
		serials = append(serials, sysSerials...)
	}

	return serials, nil
}

// getLinuxDiskSerialsLSBLK retrieves disk serials using lsblk command
func getLinuxDiskSerialsLSBLK() ([]string, error) {
	cmd := exec.Command("lsblk", "-d", "-n", "-o", "SERIAL")
	output, err := cmd.Output()
	if err != nil {
		return nil, apperror.NewError("running lsblk failed").AddError(err)
	}

	var serials []string
	lines := strings.Split(string(output), "\n")
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

	blockDir := "/sys/block"
	entries, err := os.ReadDir(blockDir)
	if err != nil {
		return nil, apperror.NewErrorf("reading %s failed", blockDir).AddError(err)
	}

	for _, entry := range entries {
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), "loop") {
			serialFile := filepath.Join(blockDir, entry.Name(), "device", "serial")
			if data, err := os.ReadFile(serialFile); err == nil {
				serial := strings.TrimSpace(string(data))
				if serial != "" {
					serials = append(serials, serial)
				}
			}
		}
	}

	return serials, nil
}
