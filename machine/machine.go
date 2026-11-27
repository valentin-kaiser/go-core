// Package machine provides utilities to generate unique machine IDs
// for licensing and identification purposes. The generated IDs are based on
// hardware characteristics that are difficult for users to modify.
//
// The package collects hardware information such as:
//   - CPU information (processor ID, features)
//   - Motherboard serial number and UUID
//   - Network interface MAC addresses
//   - System UUID from BIOS/UEFI
//   - Disk serial numbers
//
// These hardware identifiers are combined and hashed to create a unique machine ID
// that remains consistent across reboots and software changes, but will change if
// significant hardware modifications are made to the system.
//
// Example usage:
//
//	// Generate machine ID with default settings
//	id, err := machine.New().WithCPU().WithMotherboard().WithSystemUUID().WithMAC().WithDisk().ID()
//	if err != nil {
//	    log.Fatal(err)
//	}
//	fmt.Printf("Machine ID: %s\n", id)
//
//	// Generate machine ID with custom options
//	id, err := machine.New().WithSalt("my-app-salt").WithCPU().WithSystemUUID().ID()
//	if err != nil {
//	    log.Fatal(err)
//	}
//	fmt.Printf("Machine ID: %s\n", id)
//
//	// Generate VM-friendly machine ID
//	id, err := machine.New().VMFriendly().WithSalt("my-app").ID()
//	if err != nil {
//	    log.Fatal(err)
//	}
//	fmt.Printf("Machine ID: %s\n", id)
package machine

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	"github.com/valentin-kaiser/go-core/apperror"
)

type generator struct {
	salt               string
	includeCPU         bool
	includeMotherboard bool
	includeSystemUUID  bool
	includeMAC         bool
	includeDisk        bool
}

// New creates a new machine ID generator with default settings
func New() *generator {
	return &generator{}
}

// WithSalt sets a custom salt for additional entropy
func (g *generator) WithSalt(salt string) *generator {
	g.salt = salt
	return g
}

// WithCPU explicitly includes CPU identifier (enabled by default)
func (g *generator) WithCPU() *generator {
	g.includeCPU = true
	return g
}

// WithMotherboard explicitly includes motherboard serial (enabled by default)
func (g *generator) WithMotherboard() *generator {
	g.includeMotherboard = true
	return g
}

// WithSystemUUID explicitly includes system UUID (enabled by default)
func (g *generator) WithSystemUUID() *generator {
	g.includeSystemUUID = true
	return g
}

// WithMAC explicitly includes MAC addresses (enabled by default)
func (g *generator) WithMAC() *generator {
	g.includeMAC = true
	return g
}

// WithDisk explicitly includes disk serial numbers (enabled by default)
func (g *generator) WithDisk() *generator {
	g.includeDisk = true
	return g
}

// VMFriendly returns options suitable for virtual machines (CPU + UUID only)
func (g *generator) VMFriendly() *generator {
	g.includeCPU = true
	g.includeSystemUUID = true
	g.includeMotherboard = false
	g.includeMAC = false
	g.includeDisk = false
	return g
}

// ID generates a machine ID using the specified options
func (g *generator) ID() (string, error) {
	identifiers, err := collectHardwareIdentifiersWithOptions(g)
	switch {
	case err != nil:
		return "", apperror.NewError("collecting hardware identifiers failed").AddError(err)
	case len(identifiers) == 0:
		return "", apperror.NewError("no hardware identifiers found with current configuration")
	default:
		return hashIdentifiers(identifiers, g.salt), nil
	}
}

// Validate checks if the provided ID matches the current machine ID using the specified options
func (g *generator) Validate(id string) (bool, error) {
	currentID, err := g.ID()
	switch err {
	case nil:
		return currentID == id, nil
	default:
		return false, err
	}
}

// hashIdentifiers processes and hashes the hardware identifiers with optional salt
func hashIdentifiers(identifiers []string, salt string) string {
	sort.Strings(identifiers)
	combined := strings.Join(identifiers, "|")
	if salt != "" {
		combined = salt + "|" + combined
	}

	// Generate SHA256 hash
	hash := sha256.Sum256([]byte(combined))
	return hex.EncodeToString(hash[:])
}
