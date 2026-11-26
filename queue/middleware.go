package queue

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/valentin-kaiser/go-core/apperror"
)

// Middleware is a function that wraps a JobHandler
type Middleware func(JobHandler) JobHandler

// MiddlewareChain applies multiple middlewares to a job handler
func MiddlewareChain(handler JobHandler, middlewares ...Middleware) JobHandler {
	for _, middleware := range slices.Backward(middlewares) {
		handler = middleware(handler)
	}
	return handler
}

// LoggingMiddleware logs job execution
func LoggingMiddleware(next JobHandler) JobHandler {
	return func(ctx context.Context, job *Job) error {
		start := time.Now()
		logger.Debug().
			Field("job_id", job.ID).
			Field("job_type", job.Type).
			Field("priority", job.Priority.String()).
			Msg("job started")
		err := next(ctx, job)
		duration := time.Since(start)
		if err != nil {
			logger.Error().
				Err(err).
				Field("job_id", job.ID).
				Field("job_type", job.Type).
				Field("duration", duration).
				Msg("job failed")
			return err
		}
		logger.Debug().
			Field("job_id", job.ID).
			Field("job_type", job.Type).
			Field("duration", duration).
			Msg("job completed")
		return nil
	}
}

// TimeoutMiddleware adds timeout to job execution
func TimeoutMiddleware(timeout time.Duration) Middleware {
	return func(next JobHandler) JobHandler {
		return func(ctx context.Context, job *Job) error {
			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				done <- next(ctx, job)
			}()
			select {
			case err := <-done:
				return err
			case <-ctx.Done():
				return apperror.NewError("job execution timeout")
			}
		}
	}
}

// MetricsMiddleware tracks job metrics
func MetricsMiddleware(next JobHandler) JobHandler {
	return func(ctx context.Context, job *Job) error {
		start := time.Now()
		logger.Trace().
			Field("job_id", job.ID).
			Field("job_type", job.Type).
			Msg("job metrics: started")
		err := next(ctx, job)
		duration := time.Since(start)
		if err != nil {
			logger.Trace().
				Field("job_id", job.ID).
				Field("job_type", job.Type).
				Field("duration", duration).
				Msg("job metrics: failed")
			return err
		}
		logger.Trace().
			Field("job_id", job.ID).
			Field("job_type", job.Type).
			Field("duration", duration).
			Msg("job metrics: completed")
		return nil
	}
}

// RecoveryMiddleware recovers from panics in job handlers
func RecoveryMiddleware(next JobHandler) JobHandler {
	return func(ctx context.Context, job *Job) (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = apperror.NewError(fmt.Sprintf("job panic: %v", r))
				logger.Error().
					Field("job_id", job.ID).
					Field("job_type", job.Type).
					Field("panic", r).
					Msg("job panic recovered")
			}
		}()
		return next(ctx, job)
	}
}
