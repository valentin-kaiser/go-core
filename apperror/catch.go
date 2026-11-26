package apperror

import (
	"fmt"
	"os"
)

// CatchHandler handles the errors of deferred calls passed to Catch. It writes to stderr
// because the logging package depends on this package and cannot be used here.
var CatchHandler = func(err error, msg string) {
	_, werr := fmt.Fprintf(os.Stderr, ErrorFormat+"\n", msg, err.Error())
	if werr != nil {
		return
	}
}
