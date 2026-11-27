//go:build windows

package machine

import (
	"net"
	"os/exec"
	"strings"

	"github.com/valentin-kaiser/go-core/apperror"
	"golang.org/x/sys/windows/registry"
)

const (
	valueSeparator = ","

	powershellCommand        = "powershell"
	powershellNoProfile      = "-NoProfile"
	powershellNonInteractive = "-NonInteractive"
	powershellCommandFlag    = "-Command"

	wmicCommand         = "wmic"
	wmicGet             = "get"
	wmicValueFlag       = "/value"
	wmicCPU             = "cpu"
	wmicBaseboard       = "baseboard"
	wmicDiskDrive       = "diskdrive"
	wmicProcessorID     = "ProcessorId"
	wmicSerialNumber    = "SerialNumber"
	wmicProcessorIDKey  = "ProcessorId="
	wmicSerialNumberKey = "SerialNumber="

	cpuIDQuery             = "(Get-CimInstance -ClassName Win32_Processor).ProcessorId"
	motherboardSerialQuery = "(Get-CimInstance -ClassName Win32_BaseBoard).SerialNumber"
	systemUUIDQuery        = "(Get-CimInstance -ClassName Win32_ComputerSystemProduct).UUID"
	diskSerialsQuery       = "(Get-CimInstance -ClassName Win32_DiskDrive).SerialNumber -join ','"

	cryptographyRegistryKey = `SOFTWARE\Microsoft\Cryptography`
	machineGUIDValue        = "MachineGuid"
)

// collectHardwareIdentifiersWithOptions gathers Windows-specific hardware identifiers based on generator config
func collectHardwareIdentifiersWithOptions(g *generator) ([]string, error) {
	if g == nil {
		return nil, apperror.NewError("generator cannot be nil")
	}

	var collectors []func() []string

	if g.includeCPU {
		collectors = append(collectors, func() []string { return collectIfValid(getWindowsCPUID, cpuPrefix) })
	}
	if g.includeMotherboard {
		collectors = append(collectors, func() []string { return collectIfValid(getWindowsMotherboardSerial, motherboardPrefix) })
	}
	if g.includeSystemUUID {
		collectors = append(collectors, func() []string { return collectIfValid(getWindowsSystemUUID, uuidPrefix) })
	}
	if g.includeMAC {
		collectors = append(collectors, func() []string { return collectMultipleIfValid(getWindowsMACAddresses, macPrefix) })
	}
	if g.includeDisk {
		collectors = append(collectors, func() []string { return collectMultipleIfValid(getWindowsDiskSerials, diskPrefix) })
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

// parseWmicValue extracts value from wmic output with given prefix
func parseWmicValue(output, prefix string) (string, error) {
	lines := strings.SplitSeq(output, lineSeparator)
	for line := range lines {
		line = strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(line, prefix); ok {
			value := strings.TrimSpace(after)
			switch value {
			case "", placeholderSerial:
				continue
			default:
				return value, nil
			}
		}
	}
	return "", apperror.NewErrorf("value with prefix %s not found", prefix)
}

// parseWmicMultipleValues extracts all values from wmic output with given prefix
func parseWmicMultipleValues(output, prefix string) []string {
	var values []string
	lines := strings.SplitSeq(output, lineSeparator)
	for line := range lines {
		line = strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(line, prefix); ok {
			value := strings.TrimSpace(after)
			switch value {
			case "", placeholderSerial:
			default:
				values = append(values, value)
			}
		}
	}
	return values
}

// runPowerShellSingle executes a PowerShell command and returns the trimmed single-line result.
func runPowerShellSingle(query string) (string, error) {
	cmd := exec.Command(powershellCommand, powershellNoProfile, powershellNonInteractive, powershellCommandFlag, query) //nolint:gosec // query is always a constant literal from this package
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(output))
	switch value {
	case "", placeholderSerial:
		return "", apperror.NewErrorf("powershell returned empty or placeholder value for query: %s", query)
	default:
		return value, nil
	}
}

// runPowerShellMultiple executes a PowerShell command that returns multiple values joined by comma,
// then splits and filters the result.
func runPowerShellMultiple(query string) ([]string, error) {
	cmd := exec.Command(powershellCommand, powershellNoProfile, powershellNonInteractive, powershellCommandFlag, query) //nolint:gosec // query is always a constant literal from this package
	output, err := cmd.Output()
	if err != nil {
		return nil, apperror.NewError("running powershell failed").AddError(err)
	}
	raw := strings.TrimSpace(string(output))
	if raw == "" {
		return nil, apperror.NewErrorf("powershell returned no values for query: %s", query)
	}
	var values []string
	for v := range strings.SplitSeq(raw, valueSeparator) {
		v = strings.TrimSpace(v)
		if v != "" && v != placeholderSerial {
			values = append(values, v)
		}
	}
	return values, nil
}

// getWindowsCPUID retrieves CPU processor ID using wmic, falling back to PowerShell Get-CimInstance.
func getWindowsCPUID() (string, error) {
	cmd := exec.Command(wmicCommand, wmicCPU, wmicGet, wmicProcessorID, wmicValueFlag)
	output, err := cmd.Output()
	if err == nil {
		val, err := parseWmicValue(string(output), wmicProcessorIDKey)
		if err == nil {
			return val, nil
		}
	}
	return runPowerShellSingle(cpuIDQuery)
}

// getWindowsMotherboardSerial retrieves motherboard serial number using wmic, falling back to PowerShell Get-CimInstance.
func getWindowsMotherboardSerial() (string, error) {
	cmd := exec.Command(wmicCommand, wmicBaseboard, wmicGet, wmicSerialNumber, wmicValueFlag)
	output, err := cmd.Output()
	if err == nil {
		val, err := parseWmicValue(string(output), wmicSerialNumberKey)
		if err == nil {
			return val, nil
		}
	}
	return runPowerShellSingle(motherboardSerialQuery)
}

// getWindowsSystemUUID retrieves system UUID from the registry, falling back to PowerShell Get-CimInstance.
func getWindowsSystemUUID() (string, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, cryptographyRegistryKey, registry.QUERY_VALUE)
	if err == nil {
		defer apperror.Catch(k.Close, "closing registry key failed")
		val, _, err := k.GetStringValue(machineGUIDValue)
		if err == nil && val != "" {
			return val, nil
		}
	}
	return runPowerShellSingle(systemUUIDQuery)
}

// getWindowsMACAddresses retrieves MAC addresses using net.Interfaces (stdlib).
func getWindowsMACAddresses() ([]string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, apperror.NewError("listing network interfaces failed").AddError(err)
	}
	var macs []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		if mac := iface.HardwareAddr.String(); mac != "" {
			macs = append(macs, mac)
		}
	}
	return macs, nil
}

// getWindowsDiskSerials retrieves disk serial numbers using wmic, falling back to PowerShell Get-CimInstance.
func getWindowsDiskSerials() ([]string, error) {
	cmd := exec.Command(wmicCommand, wmicDiskDrive, wmicGet, wmicSerialNumber, wmicValueFlag)
	output, err := cmd.Output()
	if err == nil {
		if vals := parseWmicMultipleValues(string(output), wmicSerialNumberKey); len(vals) > 0 {
			return vals, nil
		}
	}
	return runPowerShellMultiple(diskSerialsQuery)
}
