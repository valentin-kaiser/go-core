package logging

import "io"

// MultiWriter is like io.MultiWriter but continues writing to other writers even if one fails
type MultiWriter struct {
	writers []io.Writer
}

func newMultiWriter(writers ...io.Writer) *MultiWriter {
	return &MultiWriter{writers: writers}
}

func (mw *MultiWriter) Write(p []byte) (n int, err error) {
	var lastErr error
	n = len(p)

	for _, writer := range mw.writers {
		_, writeErr := writer.Write(p)
		if writeErr != nil {
			lastErr = writeErr
		}
	}

	return n, lastErr
}
