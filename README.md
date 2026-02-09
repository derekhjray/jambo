# jambo

[![Go Reference](https://pkg.go.dev/badge/github.com/cosmorse/jambo.svg)](https://pkg.go.dev/github.com/cosmorse/jambo)
[![Go Report Card](https://goreportcard.com/badge/github.com/cosmorse/jambo)](https://goreportcard.com/report/github.com/cosmorse/jambo)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)
[![GitHub release](https://img.shields.io/github/release/cosmorse/jambo.svg)](https://github.com/cosmorse/jambo/releases)
[![GitHub issues](https://img.shields.io/github/issues/cosmorse/jambo.svg)](https://github.com/cosmorse/jambo/issues)

### JVM Dynamic Attach Utility (Go version)

A Go implementation of JVM dynamic attach mechanism, inspired by [jattach](https://github.com/jattach/jattach).

## Features

- **Go API**: Use as a library in your Go applications
- **CLI Tool**: Command-line interface similar to jattach
- **Cross-platform**: 
  - ✅ Linux (full support with namespace/container support)
  - ✅ Windows (HotSpot support via remote thread injection)
  - ⚠️ Other platforms (basic stub implementation)
- **JVM Support**: 
  - ✅ HotSpot JVM (Linux & Windows)
  - ✅ OpenJ9 JVM (Linux only)
- **Container Support**: Linux container namespace support (net, ipc, mnt, pid)
- **Comprehensive Documentation**: See [Documentation](#documentation) section for technical details

## Compatibility

### HotSpot JVM

| JDK Version | Status | Notes |
|-------------|--------|-------|
| JDK 6+ | ✅ Supported | Initial attach protocol |
| JDK 8 | ✅ Supported | Enhanced agent loading |
| JDK 9+ | ✅ Supported | Enhanced load command response |
| JDK 11+ | ✅ Supported | LTS release, fully tested |
| JDK 17+ | ✅ Supported | LTS release, fully tested |
| JDK 21+ | ✅ Supported | LTS release, load error reporting changes |

**Minimum Version**: JDK 6  
**Recommended**: JDK 8 or later

### OpenJ9 JVM

| JDK Version | Status | Notes |
|-------------|--------|-------|
| JDK 8+ | ✅ Supported | Requires `-Dcom.ibm.tools.attach.enable=yes` |
| JDK 11+ | ✅ Supported | LTS release, fully tested |
| JDK 17+ | ✅ Supported | LTS release |
| JDK 21+ | ✅ Supported | LTS release, fully tested |

**Minimum Version**: JDK 8 (with OpenJ9 VM)  
**Recommended**: JDK 11 or later  
**Required JVM Option**: `-Dcom.ibm.tools.attach.enable=yes`

**Note**: OpenJ9 attach mechanism differs from HotSpot - it uses TCP sockets and semaphores instead of Unix domain sockets. See [docs/OPENJ9.md](docs/OPENJ9.md) for details.

## Installation

### As a library

```bash
go get github.com/cosmorse/jambo
```

### Build from source

```bash
git clone https://github.com/cosmorse/jambo
cd jambo
go build ./cmd/jambo
```

## Usage

### Command Line

```bash
jambo <pid> <cmd> [args ...]
```

### Available Commands

- **load**            : load agent library
- **properties**      : print system properties
- **agentProperties** : print agent properties
- **datadump**        : show heap and thread summary
- **threaddump**      : dump all stack traces (like jstack)
- **dumpheap**        : dump heap (like jmap)
- **inspectheap**     : heap histogram (like jmap -histo)
- **setflag**         : modify manageable VM flag
- **printflag**       : print VM flag
- **jcmd**            : execute jcmd command

### Examples

#### Load Java agent

```bash
jambo <pid> load instrument false "javaagent.jar=arguments"
```

#### List available jcmd commands

```bash
jambo <pid> jcmd help -all
```

#### Take thread dump

```bash
jambo <pid> threaddump
```

## Go API

### Basic Usage

```go
package main

import (
    "fmt"
    "github.com/cosmorse/jambo"
)

func main() {
    pid := 12345
    
    // Simple attach
    output, err := jambo.Attach(pid, "threaddump", nil, true)
    if err != nil {
        fmt.Printf("Error: %v\n", err)
        return
    }
    fmt.Println(output)
    
    // Advanced usage with options
    proc, err := jambo.NewProcess(pid)
    if err != nil {
        fmt.Printf("Error: %v\n", err)
        return
    }
    
    opts := &jambo.Options{
        PrintOutput: true,
        Timeout:     5000,
    }
    
    output, err = proc.Attach("properties", nil, opts)
    if err != nil {
        fmt.Printf("Error: %v\n", err)
        return
    }
    fmt.Println(output)
}
```

### API Reference

#### Types

```go
// Process represents a target JVM process
type Process struct {
    // Private fields, accessed via methods:
    // Pid() int      - Process ID
    // Uid() int      - User ID
    // Gid() int      - Group ID
    // NsPid() int    - Namespace PID (for containers)
    // JVM() JVM      - JVM implementation instance
}

// Options configures attach operation behavior
type Options struct {
    PrintOutput bool  // Print command output to stdout
    Timeout     int   // Timeout in milliseconds (0 = no timeout)
}

// JVMType represents the JVM implementation type
type JVMType int
const (
    HotSpot JVMType = iota  // Oracle HotSpot or OpenJDK
    OpenJ9                   // Eclipse OpenJ9 (formerly IBM J9)
    Unknown                  // Unknown JVM type
)
```

#### Functions

```go
// NewProcess creates a new Process for the given PID with auto-detection of JVM type
func NewProcess(pid int) (*Process, error)

// Attach is a convenience function for quick attach operations
func Attach(pid int, command string, args []string, printOutput bool) (string, error)

// ParsePID parses a PID string (decimal or hex with 0x prefix)
func ParsePID(pidStr string) (int, error)
```

#### Methods

```go
// Process methods
func (p *Process) Attach(command string, args []string, opts *Options) (string, error)
func (p *Process) Pid() int
func (p *Process) Uid() int
func (p *Process) Gid() int
func (p *Process) NsPid() int
func (p *Process) JVM() JVM
```

## Documentation

For detailed technical documentation, see:

- **[HotSpot Attach Mechanism](docs/HOTSPOT.md)** - How HotSpot JVM dynamic attach works on Linux and Windows
- **[OpenJ9 Attach Mechanism](docs/OPENJ9.md)** - How OpenJ9 JVM dynamic attach works
- **[Windows Shellcode](docs/WINDOWS_SHELLCODE.md)** - How Windows remote thread injection and shellcode generation works

## Platform Support

### Linux
- Full support for HotSpot and OpenJ9 JVMs
- Container namespace support (net, ipc, mnt, pid)
- Process credential switching
- Uses Unix domain sockets for HotSpot
- Uses TCP/IP sockets with semaphore notification for OpenJ9

### Windows
- HotSpot JVM support via remote thread injection
- Uses Named Pipes for communication
- Requires Administrator privileges or SeDebugPrivilege
- See [Windows Shellcode documentation](docs/WINDOWS_SHELLCODE.md) for technical details

### Other Platforms
- Basic API available
- Limited functionality due to platform constraints

## Limitations

- **Namespace switching**: Requires CAP_SYS_ADMIN capability on Linux
- **Container support**: Works in most container environments (Docker, Kubernetes, etc.)
- **Windows**: 
  - Requires Administrator privileges or SeDebugPrivilege
  - Bitness must match (32-bit jambo for 32-bit JVM, 64-bit for 64-bit)
  - May be flagged by antivirus software due to remote thread injection
- **OpenJ9 on Windows**: Not supported (OpenJ9 attach mechanism differs significantly)

## Building

```bash
# Build the CLI tool
go build -o jambo ./cmd/jambo

# Build for specific platform
GOOS=linux GOARCH=amd64 go build -o jambo-linux-amd64 ./cmd/jambo
GOOS=windows GOARCH=amd64 go build -o jambo-windows-amd64.exe ./cmd/jambo
```

## Platform-Specific Notes

### Linux

- Full support for HotSpot and OpenJ9 JVMs
- Container namespace support (net, ipc, mnt, pid)
- Requires appropriate permissions to attach to processes
- Uses Unix domain sockets for communication

### Windows

- HotSpot JVM support via remote thread injection
- Requires Administrator privileges or SeDebugPrivilege to attach to other processes
- Uses Named Pipes for communication
- Bitness must match (32-bit jambo for 32-bit JVM, 64-bit for 64-bit)
- **Note**: Remote thread injection technique may be flagged by antivirus software

**Windows Error Codes**:
- `1001`: Could not load JVM module (jvm.dll not found)
- `1002`: Could not find JVM_EnqueueOperation function

### Environment Variables

- `JAMBO_ATTACH_PATH`: Override default temp path for attach files

## Testing

### Unit Tests

```bash
# Run tests
go test ./...

# Test with coverage
go test -cover ./...
```

### Docker Integration Tests

We provide comprehensive test suites for different JVM types using Docker containers:

```bash
cd tests

# Test HotSpot only (recommended)
./run_tests.sh

# Test both HotSpot and OpenJ9
./run_all_tests.sh
```

#### Test Environment Status

| JVM Type | Status | Platform | Test Coverage | Production Ready |
|----------|--------|----------|---------------|------------------|
| **HotSpot** | ✅ **Working** | Linux (Docker) | 10/10 tests passed | ✅ **YES** |
| **OpenJ9** | ✅ **Working** | Linux | 10/10 tests passed | ✅ **YES** |
| **Windows** | ❓ Untested | Windows | Code review only | ❓ **Needs Testing** |

## License

Apache License 2.0

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## Technical References

### Attach Mechanism Documentation
- [HotSpot Attach Protocol](docs/HOTSPOT.md) - Detailed explanation of HotSpot's attach mechanism
- [OpenJ9 Attach Protocol](docs/OPENJ9.md) - Detailed explanation of OpenJ9's attach mechanism
- [Windows Shellcode](docs/WINDOWS_SHELLCODE.md) - Remote thread injection and shellcode generation

### Upstream Projects
- [jattach](https://github.com/jattach/jattach) - Original C implementation that inspired this project
- [OpenJDK HotSpot Source](https://github.com/openjdk/jdk/blob/master/src/hotspot/os/linux/attachListener_linux.cpp) - Official HotSpot attach listener implementation
- [Eclipse OpenJ9](https://github.com/eclipse-openj9/openj9) - OpenJ9 JVM source code

### API Documentation
- [Go Package Documentation](https://pkg.go.dev/github.com/cosmorse/jambo) - Full Go API reference (godoc)
