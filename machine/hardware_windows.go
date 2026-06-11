//go:build windows

package machine

import (
	"fmt"
	"net"
	"os/exec"
	"strings"

	"github.com/valentin-kaiser/go-core/apperror"
	"golang.org/x/sys/windows/registry"
)

// collectHardwareIdentifiersWithOptions gathers Windows-specific hardware identifiers based on generator config
func collectHardwareIdentifiersWithOptions(g *generator) ([]string, error) {
	if g == nil {
		return nil, apperror.NewError("generator cannot be nil")
	}

	var collectors []func() []string

	if g.includeCPU {
		collectors = append(collectors, func() []string { return collectIfValid(getWindowsCPUID, "cpu:") })
	}
	if g.includeMotherboard {
		collectors = append(collectors, func() []string { return collectIfValid(getWindowsMotherboardSerial, "mb:") })
	}
	if g.includeSystemUUID {
		collectors = append(collectors, func() []string { return collectIfValid(getWindowsSystemUUID, "uuid:") })
	}
	if g.includeMAC {
		collectors = append(collectors, func() []string { return collectMultipleIfValid(getWindowsMACAddresses, "mac:") })
	}
	if g.includeDisk {
		collectors = append(collectors, func() []string { return collectMultipleIfValid(getWindowsDiskSerials, "disk:") })
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
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			value := strings.TrimSpace(strings.TrimPrefix(line, prefix))
			switch value {
			case "", "To be filled by O.E.M.":
				continue
			default:
				return value, nil
			}
		}
	}
	return "", fmt.Errorf("value with prefix %s not found", prefix)
}

// parseWmicMultipleValues extracts all values from wmic output with given prefix
func parseWmicMultipleValues(output, prefix string) []string {
	var values []string
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			value := strings.TrimSpace(strings.TrimPrefix(line, prefix))
			switch value {
			case "", "To be filled by O.E.M.":
				// Skip empty or placeholder values
			default:
				values = append(values, value)
			}
		}
	}
	return values
}

// runPowerShellSingle executes a PowerShell command and returns the trimmed single-line result.
func runPowerShellSingle(query string) (string, error) {
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", query)
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(output))
	switch value {
	case "", "To be filled by O.E.M.":
		return "", fmt.Errorf("powershell returned empty or placeholder value for query: %s", query)
	default:
		return value, nil
	}
}

// runPowerShellMultiple executes a PowerShell command that returns multiple values joined by comma,
// then splits and filters the result.
func runPowerShellMultiple(query string) ([]string, error) {
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", query)
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	raw := strings.TrimSpace(string(output))
	if raw == "" {
		return nil, fmt.Errorf("powershell returned no values for query: %s", query)
	}
	var values []string
	for _, v := range strings.Split(raw, ",") {
		v = strings.TrimSpace(v)
		if v != "" && v != "To be filled by O.E.M." {
			values = append(values, v)
		}
	}
	return values, nil
}

// getWindowsCPUID retrieves CPU processor ID using wmic, falling back to PowerShell Get-CimInstance.
func getWindowsCPUID() (string, error) {
	cmd := exec.Command("wmic", "cpu", "get", "ProcessorId", "/value")
	if output, err := cmd.Output(); err == nil {
		if val, err := parseWmicValue(string(output), "ProcessorId="); err == nil {
			return val, nil
		}
	}
	return runPowerShellSingle("(Get-CimInstance -ClassName Win32_Processor).ProcessorId")
}

// getWindowsMotherboardSerial retrieves motherboard serial number using wmic, falling back to PowerShell Get-CimInstance.
func getWindowsMotherboardSerial() (string, error) {
	cmd := exec.Command("wmic", "baseboard", "get", "SerialNumber", "/value")
	if output, err := cmd.Output(); err == nil {
		if val, err := parseWmicValue(string(output), "SerialNumber="); err == nil {
			return val, nil
		}
	}
	return runPowerShellSingle("(Get-CimInstance -ClassName Win32_BaseBoard).SerialNumber")
}

// getWindowsSystemUUID retrieves system UUID from the registry, falling back to PowerShell Get-CimInstance.
func getWindowsSystemUUID() (string, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Cryptography`, registry.QUERY_VALUE)
	if err == nil {
		defer k.Close()
		if val, _, err := k.GetStringValue("MachineGuid"); err == nil && val != "" {
			return val, nil
		}
	}
	return runPowerShellSingle("(Get-CimInstance -ClassName Win32_ComputerSystemProduct).UUID")
}

// getWindowsMACAddresses retrieves MAC addresses using net.Interfaces (stdlib).
func getWindowsMACAddresses() ([]string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
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
	cmd := exec.Command("wmic", "diskdrive", "get", "SerialNumber", "/value")
	if output, err := cmd.Output(); err == nil {
		if vals := parseWmicMultipleValues(string(output), "SerialNumber="); len(vals) > 0 {
			return vals, nil
		}
	}
	return runPowerShellMultiple("(Get-CimInstance -ClassName Win32_DiskDrive).SerialNumber -join ','")
}
