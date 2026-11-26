package machine_test

import (
	"strings"
	"testing"

	"github.com/valentin-kaiser/go-core/v2/machine"
)

// idLength is the length of the hex encoded SHA-256 digest.
const idLength = 64

func TestGeneratorBasic(t *testing.T) {
	g := machine.New().WithCPU().WithSystemUUID().WithMotherboard().WithMAC().WithDisk()

	id, err := g.ID()
	if err != nil {
		t.Fatalf("ID() error = %v", err)
	}

	if len(id) != idLength {
		t.Errorf("ID() returned ID of length %d, expected %d", len(id), idLength)
	}

	// Test consistency
	id2, err := g.ID()
	if err != nil {
		t.Fatalf("ID() second call error = %v", err)
	}

	if id != id2 {
		t.Error("ID() returned different IDs on consecutive calls")
	}
}

func TestGeneratorWithSalt(t *testing.T) {
	const (
		salt          = "test-salt"
		differentSalt = "different-salt"
	)
	g := machine.New().WithCPU().WithSystemUUID().WithSalt(salt)

	id, err := g.ID()
	if err != nil {
		t.Fatalf("ID() with salt error = %v", err)
	}

	if len(id) != idLength {
		t.Errorf("ID() returned ID of length %d, expected %d", len(id), idLength)
	}

	// Different salts should produce different IDs
	g2 := machine.New().WithCPU().WithSystemUUID().WithSalt(differentSalt)
	id2, err := g2.ID()
	if err != nil {
		t.Fatalf("ID() with different salt error = %v", err)
	}

	if id == id2 {
		t.Error("ID() should return different IDs for different salts")
	}
}

func TestGeneratorValidate(t *testing.T) {
	g := machine.New().WithCPU().WithSystemUUID()

	id, err := g.ID()
	if err != nil {
		t.Fatalf("ID() error = %v", err)
	}

	valid, err := g.Validate(id)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	if !valid {
		t.Error("Validate() returned false for valid ID")
	}

	const invalidID = "invalid-id"
	valid, err = g.Validate(invalidID)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	if valid {
		t.Error("Validate() returned true for invalid ID")
	}
}

func TestVMFriendly(t *testing.T) {
	const vmSalt = "vm-test"

	g := machine.New().VMFriendly().WithSalt(vmSalt)

	id, err := g.ID()
	if err != nil {
		t.Fatalf("VMFriendly().ID() error = %v", err)
	}

	if len(id) != idLength {
		t.Errorf("VMFriendly().ID() returned ID of length %d, expected %d", len(id), idLength)
	}

	// Test that it's different from full hardware
	g2 := machine.New().WithCPU().WithSystemUUID().WithMotherboard().WithMAC().WithDisk().WithSalt(vmSalt)
	id2, err := g2.ID()
	if err != nil {
		t.Fatalf("Full hardware ID() error = %v", err)
	}

	if id == id2 {
		t.Error("VMFriendly() should produce different ID from full hardware")
	}
}

func TestNoIdentifiersError(t *testing.T) {
	g := machine.New() // No identifiers enabled

	_, err := g.ID()
	if err == nil {
		t.Fatal("ID() should return error when no identifiers are enabled")
	}

	const expectedError = "no hardware identifiers found with current configuration"
	if !strings.Contains(err.Error(), expectedError) {
		t.Errorf("ID() error should contain %q, got %q", expectedError, err.Error())
	}
}

func TestGeneratorChaining(t *testing.T) {
	const chainSalt = "chain-test"

	// Test that method chaining works
	g := machine.New().
		WithSalt(chainSalt).
		WithCPU().
		WithSystemUUID().
		WithMotherboard()

	id, err := g.ID()
	if err != nil {
		t.Fatalf("Chained generator ID() error = %v", err)
	}

	if len(id) != idLength {
		t.Errorf("Chained generator ID() returned ID of length %d, expected %d", len(id), idLength)
	}

	// Verify it validates correctly
	valid, err := g.Validate(id)
	if err != nil {
		t.Fatalf("Chained generator Validate() error = %v", err)
	}

	if !valid {
		t.Error("Chained generator Validate() returned false for valid ID")
	}
}
