package flag_test

import (
	"os"
	"testing"

	"github.com/spf13/pflag"
)

const commandLineName = ""

// useFreshCommandLine replaces the global command line for the test and restores it afterwards.
func useFreshCommandLine(t *testing.T) {
	t.Helper()

	original := pflag.CommandLine
	pflag.CommandLine = pflag.NewFlagSet(commandLineName, pflag.ContinueOnError)
	t.Cleanup(func() { pflag.CommandLine = original })
}

// setArgs replaces the process arguments for the test and restores them afterwards.
func setArgs(t *testing.T, args ...string) {
	t.Helper()

	original := os.Args
	os.Args = args
	t.Cleanup(func() { os.Args = original })
}
