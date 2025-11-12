// Package flag provides a simple API for defining and parsing command-line flags
// in Go applications.
//
// It is built on top of the pflag library and includes support
// for a set of common default flags.
//
// Default flags:
//   - `--path`    (string): Sets the application’s default path (default: "./data")
//   - `--help`    (bool): Displays the help message
//   - `--version` (bool): Prints the application version
//   - `--debug`   (bool): Enables debug mode
//
// The `Init` function parses all registered flags and should be called early,
// typically in the `main` function of the application. If the `--help` flag is
// set, it prints usage information and exits.
//
// Additional flags can be registered using `Register`, which accepts the flag
// name, a pointer to the variable to populate, and a usage description.
// Existing flags can be overridden using `Override`, which allows changing the
// variable, default value, and description of an already registered flag.
// Flags can be removed using `Unregister`, which removes a previously
// registered flag from the command line.
// Supported types include strings, booleans, integers, unsigned integers, and floats.
//
// Example:
//
//	package main
//
//	import (
//		"fmt"
//		"github.com/valentin-kaiser/go-core/flag"
//	)
//
//	var CustomFlag string
//
//	func main() {
//		flag.Register("custom", &CustomFlag, "A custom flag for demonstration")
//
//		// Override the default path flag
//		flag.Path = "/new/default/path"
//		flag.Override("path", &flag.Path, "Updated application working directory")
//
//		// Unregister a flag if no longer needed
//		flag.Unregister("custom")
//
//		flag.Init()
//
//		fmt.Println("Custom Flag Value:", CustomFlag)
//		fmt.Println("Path:", flag.Path)
//	}
package flag

import (
	"fmt"
	"os"
	"reflect"

	"github.com/spf13/pflag"
)

var (
	// Path is the default path for the application data
	Path string
	// Help indicates whether the help message should be printed
	Help bool
	// Version indicates whether the version information should be printed
	Version bool
	// Debug indicates whether debug mode is enabled
	Debug bool
)

func init() {
	pflag.StringVar(&Path, "path", "./data", "Sets the application working directory")
	pflag.BoolVar(&Help, "help", false, "Prints the help page")
	pflag.BoolVar(&Version, "version", false, "Prints the software version")
	pflag.BoolVar(&Debug, "debug", false, "Enables debug mode")
}

// Init initializes the flags and parses them
// It should be called in the main package of the application
func Init() {
	pflag.Parse()
}

// PrintHelp prints the help message to standard error output
func PrintHelp() {
	fmt.Fprintln(os.Stderr, "Usage:")
	pflag.PrintDefaults()
}

// Arguments returns the non-flag command-line arguments
func Arguments() []string {
	return pflag.Args()
}

// Register registers a new flag with the given name, value and usage
// It panics if the flag is already registered or if the value is not a pointer
func Register(name string, value interface{}, usage string) {
	if pflag.Lookup(name) != nil {
		panic(fmt.Sprintf("flag %s already registered", name))
	}

	val := reflect.ValueOf(value)
	if val.Kind() != reflect.Ptr {
		panic(fmt.Sprintf("flag %s must be a pointer", name))
	}

	if val.IsNil() {
		panic(fmt.Sprintf("flag %s must not be nil", name))
	}

	switch v := value.(type) {
	case *string:
		pflag.StringVar(v, name, *v, usage)
	case *bool:
		pflag.BoolVar(v, name, *v, usage)
	case *int:
		pflag.IntVar(v, name, *v, usage)
	case *int8:
		pflag.Int8Var(v, name, *v, usage)
	case *int16:
		pflag.Int16Var(v, name, *v, usage)
	case *int32:
		pflag.Int32Var(v, name, *v, usage)
	case *int64:
		pflag.Int64Var(v, name, *v, usage)
	case *uint:
		pflag.UintVar(v, name, *v, usage)
	case *uint8:
		pflag.Uint8Var(v, name, *v, usage)
	case *uint16:
		pflag.Uint16Var(v, name, *v, usage)
	case *uint32:
		pflag.Uint32Var(v, name, *v, usage)
	case *uint64:
		pflag.Uint64Var(v, name, *v, usage)
	case *float32:
		pflag.Float32Var(v, name, *v, usage)
	case *float64:
		pflag.Float64Var(v, name, *v, usage)
	default:
		panic(fmt.Sprintf("unsupported type %T", v))
	}
}

// Override allows changing an existing flag's variable, default value and description
// It panics if the flag is not already registered or if the value is not a pointer
// Note: The flag must not have been parsed yet for this to work properly
func Override(name string, value interface{}, usage string) {
	if pflag.Lookup(name) == nil {
		panic(fmt.Sprintf("flag %s is not registered", name))
	}

	val := reflect.ValueOf(value)
	if val.Kind() != reflect.Ptr {
		panic(fmt.Sprintf("flag %s value must be a pointer", name))
	}

	if val.IsNil() {
		panic(fmt.Sprintf("flag %s value must not be nil", name))
	}

	if pflag.Parsed() {
		panic(fmt.Sprintf("cannot override flag %s after flags have been parsed", name))
	}

	newCommandLine := pflag.NewFlagSet("", pflag.ContinueOnError)

	// Copy all flags except the one we're overriding
	pflag.CommandLine.VisitAll(func(flag *pflag.Flag) {
		if flag.Name != name {
			newCommandLine.AddFlag(flag)
		}
	})

	switch v := value.(type) {
	case *string:
		newCommandLine.StringVar(v, name, *v, usage)
	case *bool:
		newCommandLine.BoolVar(v, name, *v, usage)
	case *int:
		newCommandLine.IntVar(v, name, *v, usage)
	case *int8:
		newCommandLine.Int8Var(v, name, *v, usage)
	case *int16:
		newCommandLine.Int16Var(v, name, *v, usage)
	case *int32:
		newCommandLine.Int32Var(v, name, *v, usage)
	case *int64:
		newCommandLine.Int64Var(v, name, *v, usage)
	case *uint:
		newCommandLine.UintVar(v, name, *v, usage)
	case *uint8:
		newCommandLine.Uint8Var(v, name, *v, usage)
	case *uint16:
		newCommandLine.Uint16Var(v, name, *v, usage)
	case *uint32:
		newCommandLine.Uint32Var(v, name, *v, usage)
	case *uint64:
		newCommandLine.Uint64Var(v, name, *v, usage)
	case *float32:
		newCommandLine.Float32Var(v, name, *v, usage)
	case *float64:
		newCommandLine.Float64Var(v, name, *v, usage)
	default:
		panic(fmt.Sprintf("unsupported type %T", v))
	}

	pflag.CommandLine = newCommandLine
}

// Unregister removes a previously registered flag
// It panics if the flag is not registered or if flags have already been parsed
func Unregister(name string) {
	if pflag.Lookup(name) == nil {
		panic(fmt.Sprintf("flag %s is not registered", name))
	}

	if pflag.Parsed() {
		panic(fmt.Sprintf("cannot unregister flag %s after flags have been parsed", name))
	}

	newCommandLine := pflag.NewFlagSet("", pflag.ContinueOnError)

	// Copy all flags except the one we're unregistering
	pflag.CommandLine.VisitAll(func(flag *pflag.Flag) {
		if flag.Name != name {
			newCommandLine.AddFlag(flag)
		}
	})

	pflag.CommandLine = newCommandLine
}
