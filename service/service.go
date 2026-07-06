// Package service provides a simple and robust way to create, manage, and deploy
// system services (daemons) in Go applications across different platforms.
//
// This package wraps the kardianos/service library and provides additional functionality
// for application lifecycle management, including automatic path detection, graceful
// shutdown handling, and seamless integration with other go-core packages.
//
// The package automatically detects whether the application is running in interactive
// mode (console/terminal) or as a system service, and adjusts behavior accordingly.
// When running as a service, it automatically configures the data directory in the
// platform's machine-wide application data location.
//
// Features:
//   - Cross-platform service management (Windows, Linux, macOS)
//   - Automatic interactive vs service mode detection
//   - Graceful startup and shutdown handling
//   - Integration with go-core logging and configuration systems
//   - Error handling and recovery mechanisms
//   - Support for service installation, uninstallation, and control
//
// Basic Service Example:
//
//	package main
//
//	import (
//		"context"
//		"fmt"
//		"time"
//
//		"github.com/valentin-kaiser/go-core/service"
//		"github.com/valentin-kaiser/go-core/logging"
//	)
//
//	func main() {
//		// Configure service
//		config := &service.Config{
//			Name:        "MyApp",
//			DisplayName: "My Application Service",
//			Description: "A sample Go application running as a service",
//		}
//
//		// Define start function
//		start := func(s *service.Service) error {
//			fmt.Println("Service starting...")
//			// Your application logic here
//			go func() {
//				for {
//					fmt.Println("Service is running...")
//					time.Sleep(30 * time.Second)
//				}
//			}()
//			return nil
//		}
//
//		// Define stop function
//		stop := func(s *service.Service) error {
//			fmt.Println("Service stopping...")
//			// Cleanup logic here
//			return nil
//		}
//
//		// Run the service
//		if err := service.Run(config, start, stop); err != nil {
//			panic(err)
//		}
//	}
//
// Web Server Service Example:
//
//	package main
//
//	import (
//		"context"
//		"net/http"
//		"time"
//
//		"github.com/valentin-kaiser/go-core/service"
//		"github.com/valentin-kaiser/go-core/web"
//		"github.com/valentin-kaiser/go-core/logging"
//	)
//
//	func main() {
//		config := &service.Config{
//			Name:        "WebService",
//			DisplayName: "Web Service",
//			Description: "HTTP web server running as a system service",
//		}
//
//		var server *web.Server
//
//		start := func(s *service.Service) error {
//			server = web.New().WithPort(8080)
//
//			server.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
//				w.Write([]byte("Service is running!"))
//			})
//
//			go func() {
//				if err := server.Start(); err != nil {
//					logging.GetPackageLogger("main").Error().Err(err).Msg("Server failed")
//				}
//			}()
//
//			return nil
//		}
//
//		stop := func(s *service.Service) error {
//			if server != nil {
//				return server.Shutdown()
//			}
//			return nil
//		}
//
//		if err := service.Run(config, start, stop); err != nil {
//			panic(err)
//		}
//	}
//
// Platform Support:
//   - Windows: Runs as Windows Service
//   - Linux: Runs as systemd service or SysV init script
//   - macOS: Runs as launchd service
//   - Development: Runs in interactive mode for testing
package service

import (
	"path/filepath"

	"github.com/kardianos/service"
	"github.com/valentin-kaiser/go-core/apperror"
	"github.com/valentin-kaiser/go-core/flag"
	"github.com/valentin-kaiser/go-core/logging"
)

var (
	interactive = false
	logger      = logging.GetPackageLogger("service")
)

// Config is an alias for service.Config providing system service configuration options.
// It includes service name, display name, description, dependencies, and platform-specific settings.
type Config = service.Config

func init() {
	var err error
	interactive = service.AvailableSystems()[len(service.AvailableSystems())-1].Interactive()
	if !interactive {
		flag.Path = flag.ServiceDataPath()
	}

	flag.Path, err = filepath.Abs(flag.Path)
	if err != nil {
		logger.Error().Fields(logging.F("error", err)).Msg("determining absolute path failed")
		return
	}
}

// Run starts a service with the provided configuration and start/stop functions.
// It handles signal management and graceful shutdown.
func Run(config *Config, start func(s *Service) error, stop func(s *Service) error) error {
	s := &Service{
		err:   make(chan error, 1),
		start: start,
		stop:  stop,
	}

	if config == nil {
		return apperror.NewError("service config is nil")
	}

	if start == nil {
		return apperror.NewError("service start function is nil")
	}

	if stop == nil {
		return apperror.NewError("service stop function is nil")
	}

	if interactive {
		err := s.Start(nil)
		if err != nil {
			return err
		}

		return apperror.Wrap(<-s.err)
	}

	svc, err := service.New(s, config)
	if err != nil {
		return apperror.NewError("creating service failed").AddError(err)
	}

	err = svc.Run()
	if err != nil {
		return apperror.NewError("starting service failed").AddError(err)
	}

	return apperror.Wrap(<-s.err)
}

// IsInteractive returns true if the service is running in interactive mode
func IsInteractive() bool {
	return interactive
}

// Service wraps the kardianos/service.Service interface and provides additional
// functionality for managing application lifecycle with custom start and stop handlers.
// It includes error handling and graceful shutdown capabilities.
type Service struct {
	service.Service
	err   chan error
	start func(s *Service) error
	stop  func(s *Service) error
}

// Start implements the service.Interface Start method and executes the custom start handler.
// It is called by the service manager when the service should start.
func (s *Service) Start(svc service.Service) error {
	s.Service = svc
	if s.start == nil {
		return apperror.NewError("service start function is not defined")
	}
	go func() {
		s.err <- s.start(s)
	}()
	return nil
}

// Stop implements the service.Interface Stop method and executes the custom stop handler.
// It is called by the service manager when the service should stop gracefully.
func (s *Service) Stop(svc service.Service) error {
	s.Service = svc
	if s.stop == nil {
		return apperror.NewError("service stop function is not defined")
	}

	go func() {
		s.err <- s.stop(s)
	}()
	return nil
}
