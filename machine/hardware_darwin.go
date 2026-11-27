//go:build darwin

package machine

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/valentin-kaiser/go-core/apperror"
)

// collectHardwareIdentifiersWithOptions gathers macOS-specific hardware identifiers based on generator config
func collectHardwareIdentifiersWithOptions(g *generator) ([]string, error) {
	if g == nil {
		return nil, apperror.NewError("generator cannot be nil")
	}

	var collectors []func() []string

	if g.includeSystemUUID {
		collectors = append(collectors, func() []string { return collectIfValid(getMacOSHardwareUUID, "uuid:") })
	}
	if g.includeMotherboard {
		collectors = append(collectors, func() []string { return collectIfValid(getMacOSSerialNumber, "serial:") })
	}
	if g.includeCPU {
		collectors = append(collectors, func() []string { return collectIfValid(getMacOSCPUInfo, "cpu:") })
	}
	if g.includeMAC {
		collectors = append(collectors, func() []string { return collectMultipleIfValid(getMacOSMACAddresses, "mac:") })
	}
	if g.includeDisk {
		collectors = append(collectors, func() []string { return collectMultipleIfValid(getMacOSDiskInfo, "disk:") })
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

// getMacOSHardwareUUID retrieves hardware UUID using system_profiler
func getMacOSHardwareUUID() (string, error) {
	cmd := exec.Command("system_profiler", "SPHardwareDataType", "-json")
	output, err := cmd.Output()
	switch err {
	case nil:
		return extractMacOSValue(string(output), "platform_UUID")
	default:
		// Fallback to ioreg
		return getMacOSHardwareUUIDIOReg()
	}
}

// extractMacOSValue extracts a value from macOS command output
func extractMacOSValue(output, key string) (string, error) {
	switch {
	case strings.Contains(output, key):
		lines := strings.Split(output, "\n")
		for _, line := range lines {
			switch {
			case strings.Contains(line, key):
				parts := strings.Split(line, ":")
				switch {
				case len(parts) >= 2:
					value := strings.Trim(strings.TrimSpace(parts[1]), `",`)
					switch value {
					case "":
						continue
					default:
						return value, nil
					}
				}
			}
		}
	}
	return "", apperror.NewErrorf("%s not found in output", key)
}

// getMacOSHardwareUUIDIOReg retrieves hardware UUID using ioreg as fallback
func getMacOSHardwareUUIDIOReg() (string, error) {
	cmd := exec.Command("ioreg", "-d2", "-c", "IOPlatformExpertDevice")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.Contains(line, "IOPlatformUUID") {
			parts := strings.Split(line, "=")
			if len(parts) >= 2 {
				uuid := strings.Trim(strings.TrimSpace(parts[1]), `" `)
				if uuid != "" {
					return uuid, nil
				}
			}
		}
	}

	return "", apperror.NewError("hardware UUID not found in ioreg output")
}

// getMacOSSerialNumber retrieves system serial number
func getMacOSSerialNumber() (string, error) {
	cmd := exec.Command("system_profiler", "SPHardwareDataType", "-json")
	output, err := cmd.Output()
	switch err {
	case nil:
		return extractMacOSValue(string(output), "serial_number")
	default:
		// Fallback to ioreg
		return getMacOSSerialNumberIOReg()
	}
}

// getMacOSSerialNumberIOReg retrieves serial number using ioreg as fallback
func getMacOSSerialNumberIOReg() (string, error) {
	cmd := exec.Command("ioreg", "-c", "IOPlatformExpertDevice", "-d", "2")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.Contains(line, "IOPlatformSerialNumber") {
			parts := strings.Split(line, "=")
			if len(parts) >= 2 {
				serial := strings.Trim(strings.TrimSpace(parts[1]), `" `)
				if serial != "" {
					return serial, nil
				}
			}
		}
	}

	return "", apperror.NewError("serial number not found in ioreg output")
}

// getMacOSCPUInfo retrieves CPU information
func getMacOSCPUInfo() (string, error) {
	cmd := exec.Command("sysctl", "-n", "machdep.cpu.brand_string")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	cpuBrand := strings.TrimSpace(string(output))

	// Get CPU features
	cmd = exec.Command("sysctl", "-n", "machdep.cpu.features")
	output, err = cmd.Output()
	if err == nil {
		features := strings.TrimSpace(string(output))
		return fmt.Sprintf("%s:%s", cpuBrand, features), nil
	}

	return cpuBrand, nil
}

// getMacOSMACAddresses retrieves MAC addresses using ifconfig
func getMacOSMACAddresses() ([]string, error) {
	cmd := exec.Command("ifconfig")
	output, err := cmd.Output()
	if err != nil {
		return nil, apperror.NewError("running ifconfig failed").AddError(err)
	}

	var macs []string
	lines := strings.Split(string(output), "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "ether ") {
			parts := strings.Fields(line)
			for i, part := range parts {
				if part == "ether" && i+1 < len(parts) {
					mac := parts[i+1]
					if mac != "" && mac != "00:00:00:00:00:00" {
						macs = append(macs, mac)
					}
					break
				}
			}
		}
	}

	return macs, nil
}

// getMacOSDiskInfo retrieves disk information
func getMacOSDiskInfo() ([]string, error) {
	var diskInfo []string

	// Get disk information using diskutil
	cmd := exec.Command("diskutil", "list", "-plist")
	output, err := cmd.Output()
	if err != nil {
		// Fallback to system_profiler
		return getMacOSDiskInfoSystemProfiler()
	}

	// For simplicity, we'll extract basic disk identifiers
	// In a full implementation, you'd parse the plist format
	outputStr := string(output)
	lines := strings.Split(outputStr, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "<string>/dev/disk") {
			diskInfo = append(diskInfo, line)
		}
	}

	return diskInfo, nil
}

// getMacOSDiskInfoSystemProfiler retrieves disk info using system_profiler as fallback
func getMacOSDiskInfoSystemProfiler() ([]string, error) {
	cmd := exec.Command("system_profiler", "SPStorageDataType", "-json")
	output, err := cmd.Output()
	if err != nil {
		return nil, apperror.NewError("running system_profiler failed").AddError(err)
	}

	var diskInfo []string
	outputStr := string(output)

	// Simple extraction of volume information
	if strings.Contains(outputStr, "physical_drive") {
		lines := strings.Split(outputStr, "\n")
		for _, line := range lines {
			if strings.Contains(line, "physical_drive") {
				diskInfo = append(diskInfo, strings.TrimSpace(line))
			}
		}
	}

	return diskInfo, nil
}
