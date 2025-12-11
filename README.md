# go-core

[![GoDoc](https://godoc.org/github.com/golang/gddo?status.svg)](http://godoc.org/github.com/valentin-kaiser/go-core)
[![License](https://img.shields.io/badge/License-BSD_3--Clause-blue.svg)](https://github.com/valentin-kaiser/go-core/blob/main/LICENSE)

**go-core** is a comprehensive, modular collection of Go libraries designed to accelerate professional application development. It provides robust, production-grade utilities for error handling, configuration, logging, caching, database management, security, service orchestration, and more. Each package is crafted for extensibility, reliability, and seamless integration.

> **⚠️ Breaking Changes:**
> This library is under active development and primarily maintained for personal use. The API may change without notice as new features are added and improvements are made. Backward compatibility is not guaranteed.

## Key Features

### Error Handling
- Custom error types with stack traces and context
- Utility functions for error propagation and recovery

### Caching
- In-memory, Redis, and distributed caching
- TTL, LRU eviction, serialization, and advanced cache configuration

### Configuration Management
- Structured config loading from files, flags, and environment
- Dynamic watching, validation, and default value registration

### Logging
- Flexible logging framework with adapters for zerolog, standard log, and no-op
- Global logger registry, structured logging, and zero-allocation logger
- Log rotation and custom output targets

### Database
- Abstraction over GORM with automatic connection handling
- Schema migrations, transaction management, and multi-database support

### Flags & Interruption
- Simple API for command-line flag parsing
- Graceful shutdown and panic recovery utilities

### Memory & Machine Utilities
- Estimate memory footprint of Go values
- Unique machine ID generation and hardware info (CPU, features)

### Mail
- Comprehensive email sending and SMTP server functionality
- Security features: HELO validation, IP filtering, rate limiting
- Delivery tracking, retry logic, and template management

### Queue & Task Processing
- Background job processing system
- In-memory, Redis, and RabbitMQ support
- Scheduling, middleware, job context, and distributed execution

### Security
- Cryptographic utilities: PGP, AES encryption/decryption
- Secure operations for sensitive data and communication

### Service Management
- Service lifecycle management and deployment
- Integration with kardianos/service, shutdown handling, and cross-platform support

### Web & RPC
- Configurable HTTP server with middleware support
- JSON-RPC over HTTP and WebSocket (requires Protocol Buffer definitions for server generation)
- Rate limiting, request metadata, and automatic method dispatch

### Versioning
- Build/version info embedding and management
- Precise control over application metadata

## Installation

```bash
go get github.com/valentin-kaiser/go-core
```

## Package Overview

- `apperror`: Advanced error handling
- `cache`: Multi-backend caching (memory, Redis)
- `config`: Structured configuration management
- `database`: Database abstraction and migrations
- `flag`: Command-line flag parsing
- `interruption`: Panic recovery and graceful shutdown
- `logging`: Flexible logging adapters and registry
- `machine`: Machine identification and hardware info
- `mail`: Email sending, SMTP server, and security
- `memsize`: Memory size estimation
- `queue`: Job/task queues and distributed processing
- `security`: Cryptography and secure operations
- `service`: Service lifecycle and deployment
- `version`: Build/version info management
- `web`: HTTP server and middleware
- `web/jrpc`: JSON-RPC over HTTP/WebSocket
- `zlog`: Zero-allocation logger

## Documentation

See [GoDoc](http://godoc.org/github.com/valentin-kaiser/go-core) for full API documentation and usage details.


## Contributing

Contributions, issues, and feature requests are welcome! Please see [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

## License

This project is licensed under the BSD 3-Clause License. See [LICENSE](LICENSE) for details.