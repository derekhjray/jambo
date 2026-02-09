// Package jambo provides a Go implementation of JVM Dynamic Attach mechanism.
//
// This package allows you to dynamically attach to running JVM processes and execute
// various commands such as loading agents, dumping threads, inspecting heap, and more.
// It supports both HotSpot and OpenJ9 JVMs on Linux and Windows platforms.
//
// # Basic Usage
//
// Simple attach example:
//
//	output, err := jambo.Attach(12345, "threaddump", nil, true)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	fmt.Println(output)
//
// # Advanced Usage
//
// Using Process for more control:
//
//	proc, err := jambo.NewProcess(12345)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	opts := &jambo.Options{
//	    PrintOutput: true,
//	    Timeout:     5000,
//	}
//
//	output, err := proc.Attach("jcmd", []string{"VM.version"}, opts)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
// # Supported Commands
//
//   - load: Load a Java agent into the JVM
//   - properties: Print system properties
//   - agentProperties: Print agent properties
//   - threaddump: Dump all thread stack traces
//   - dumpheap: Dump heap to file
//   - inspectheap: Print heap histogram
//   - datadump: Show heap and thread summary
//   - jcmd: Execute arbitrary jcmd command
//
// # Platform Support
//
//   - Linux: Full support (HotSpot + OpenJ9, with container namespace support)
//   - Windows: HotSpot support via remote thread injection
//   - Other platforms: Basic stub implementation
package jambo

import (
	"errors"
	"fmt"
	"os"
	"strconv"
)

var (
	// ErrProcessNotFound indicates the target process does not exist or cannot be accessed.
	ErrProcessNotFound = errors.New("process not found")

	// ErrInvalidPID indicates the provided process ID is invalid (zero or negative).
	ErrInvalidPID = errors.New("invalid process ID")

	// ErrPermission indicates insufficient permissions to attach to the target process.
	ErrPermission = errors.New("permission denied")

	// ErrCommandFailed indicates the attach command execution failed in the target JVM.
	ErrCommandFailed = errors.New("command execution failed")
)

// JVMType represents the type of JVM implementation.
type JVMType int

const (
	// HotSpot represents Oracle HotSpot or OpenJDK JVM.
	HotSpot JVMType = iota

	// OpenJ9 represents Eclipse OpenJ9 JVM (formerly IBM J9).
	OpenJ9

	// Unknown represents an unidentified JVM type.
	Unknown
)

// Options configures the behavior of attach operations.
type Options struct {
	// PrintOutput determines whether command output should be printed to stdout.
	PrintOutput bool

	// Timeout specifies the maximum time in milliseconds to wait for command completion.
	// A value of 0 means no timeout (not yet implemented).
	Timeout int
}

// JVM defines the interface for JVM attach operations.
// Different JVM implementations (HotSpot, OpenJ9) provide their own implementations.
type JVM interface {
	// Attach performs the actual attach operation to the target JVM process.
	// It sends the command with arguments and returns the output.
	//
	// Parameters:
	//   - pid: The process ID of the target JVM
	//   - nspid: The namespace PID (for container support, same as pid if not in container)
	//   - args: Command and its arguments
	//   - printOutput: Whether to print output to stdout
	//   - tmpPath: Temporary directory path for attach files
	//
	// Returns:
	//   - string: Command output from the JVM
	//   - error: Any error that occurred during attach
	Attach(pid, nspid int, args []string, printOutput bool, tmpPath string) (string, error)

	// Detect checks if the target process is running this JVM type.
	// Returns true if this JVM implementation is detected.
	Detect(nspid int) bool

	// Type returns the JVM implementation type.
	Type() JVMType
}

// NewProcess creates a new Process instance for the given process ID.
// It automatically detects the JVM type and initializes the appropriate JVM instance.
//
// The function performs the following:
//   - Validates the PID
//   - Retrieves process information (UID, GID, namespace PID)
//   - Detects the JVM type (HotSpot or OpenJ9)
//
// Example:
//
//	proc, err := jambo.NewProcess(12345)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	fmt.Printf("JVM Type: %v\n", proc.JVM().Type())
//
// Returns ErrInvalidPID if pid <= 0.
// Returns ErrProcessNotFound if the process doesn't exist or can't be accessed.

func NewProcess(pid int) (*Process, error) {
	if pid <= 0 {
		return nil, ErrInvalidPID
	}

	proc := &Process{
		pid: pid,
	}

	if err := proc.getProcessInfo(); err != nil {
		return nil, err
	}

	proc.detectJVM()
	return proc, nil
}

// Process represents a target JVM process that can be attached to.
// It encapsulates process information and provides methods for attach operations.
//
// Process instances should be created using NewProcess() to ensure proper initialization.
type Process struct {
	pid   int // Process ID
	uid   int // User ID of the process owner
	gid   int // Group ID of the process owner
	nsPid int // Namespace PID (for container support)
	jvm   JVM // JVM implementation instance (HotSpot or OpenJ9)
}

// Pid returns the process ID of the target JVM process.
func (proc *Process) Pid() int {
	return proc.pid
}

// Uid returns the user ID of the process owner.
// This is used for credential switching when attaching.
func (proc *Process) Uid() int {
	return proc.uid
}

// Gid returns the group ID of the process owner.
// This is used for credential switching when attaching.
func (proc *Process) Gid() int {
	return proc.gid
}

// NsPid returns the namespace process ID.
// In containers, this may differ from Pid(). Otherwise, it's the same as Pid().
func (proc *Process) NsPid() int {
	return proc.nsPid
}

// JVM returns the JVM implementation instance for this process.
// The instance type (HotSpot or OpenJ9) is automatically detected during Process creation.
func (proc *Process) JVM() JVM {
	return proc.jvm
}

func (p *Process) getProcessInfo() error {
	uid, gid, nsPid, err := getProcessInfo(p.pid)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrProcessNotFound, err)
	}

	p.uid = uid
	p.gid = gid
	p.nsPid = nsPid

	return nil
}

// detectJVM automatically detects the JVM type and creates the appropriate JVM instance.
// It tries OpenJ9 detection first, then falls back to HotSpot.
// This method is called automatically by NewProcess().
func (p *Process) detectJVM() {
	// Try to detect JVM type and create appropriate JVM instance
	openJ9 := &openJ9{}
	hotSpot := &hotSpot{}

	if openJ9.Detect(p.nsPid) {
		p.jvm = openJ9
	} else {
		p.jvm = hotSpot
	}
}

// Attach performs an attach operation with the specified command and arguments.
// This is the main method for executing commands in the target JVM.
//
// The method performs the following steps:
//  1. Enters target process namespaces (for container support)
//  2. Switches to target process credentials (if needed)
//  3. Determines the appropriate temp path
//  4. Delegates to the JVM-specific implementation
//
// Parameters:
//   - command: The attach command to execute (e.g., "threaddump", "jcmd", "load")
//   - args: Additional arguments for the command
//   - options: Attach options (nil for defaults)
//
// Example:
//
//	proc, _ := jambo.NewProcess(12345)
//	output, err := proc.Attach("threaddump", nil, &jambo.Options{
//	    PrintOutput: true,
//	})
//	if err != nil {
//	    log.Fatal(err)
//	}
//
// Example with jcmd:
//
//	output, err := proc.Attach("jcmd", []string{"VM.version"}, nil)
//
// Example loading an agent:
//
//	output, err := proc.Attach("load", []string{"/path/to/agent.so", "false", "options"}, nil)
//
// Returns ErrCommandFailed if the command execution fails in the JVM.
func (p *Process) Attach(command string, args []string, options *Options) (string, error) {
	if options == nil {
		options = &Options{PrintOutput: true}
	}

	if err := p.enterNamespaces(); err != nil {
		return "", err
	}

	if err := p.setCredentials(); err != nil {
		return "", err
	}

	tmpPath, err := p.getTempPath()
	if err != nil {
		return "", err
	}

	allArgs := append([]string{command}, args...)

	// Use the JVM instance to perform the attach operation
	if p.jvm == nil {
		return "", errors.New("JVM not initialized")
	}

	output, err := p.jvm.Attach(p.pid, p.nsPid, allArgs, options.PrintOutput, tmpPath)
	if err != nil {
		return output, fmt.Errorf("%w: %v", ErrCommandFailed, err)
	}

	return output, nil
}

// enterNamespaces enters the target process's Linux namespaces.
// This is necessary when attaching to JVMs running in containers.
// Enters net, ipc, and mnt namespaces.
//
// On non-Linux platforms or when namespace support is not available,
// this is a no-op that returns nil.
func (p *Process) enterNamespaces() error {
	if err := enterNamespace(p.pid, "net"); err != nil {
		return err
	}
	if err := enterNamespace(p.pid, "ipc"); err != nil {
		return err
	}
	if err := enterNamespace(p.pid, "mnt"); err != nil {
		return err
	}
	return nil
}

// setCredentials switches to the target process's user and group IDs.
// This is necessary when the current process has different credentials
// than the target JVM process.
//
// Requires appropriate permissions (typically root or CAP_SETUID/CAP_SETGID).
// Returns ErrPermission if the credential switch fails.
func (p *Process) setCredentials() error {
	myUID := os.Geteuid()
	myGID := os.Getegid()

	if myUID != p.uid || myGID != p.gid {
		if err := setCredentials(p.uid, p.gid); err != nil {
			return fmt.Errorf("%w: %v", ErrPermission, err)
		}
	}

	return nil
}

// getTempPath returns the appropriate temporary directory path for attach files.
// For containerized processes (nsPid != pid), uses the namespace PID.
// Otherwise, uses the regular PID.
//
// The path can be overridden using the JAMBO_ATTACH_PATH environment variable.
func (p *Process) getTempPath() (string, error) {
	// Always use nsPid for temp path in containers
	if p.nsPid != p.pid {
		return getTempPath(p.nsPid)
	}
	return getTempPath(p.pid)
}

// Attach is a convenience function that creates a Process and performs an attach operation.
// This is the simplest way to attach to a JVM process.
//
// Parameters:
//   - pid: Target process ID
//   - command: Attach command to execute
//   - args: Command arguments (can be nil)
//   - printOutput: Whether to print output to stdout
//
// Example:
//
//	output, err := jambo.Attach(12345, "threaddump", nil, true)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	fmt.Println(output)
//
// For more control, use NewProcess() and call Attach() on the Process instance.
func Attach(pid int, command string, args []string, printOutput bool) (string, error) {
	proc, err := NewProcess(pid)
	if err != nil {
		return "", err
	}

	options := &Options{
		PrintOutput: printOutput,
	}
	return proc.Attach(command, args, options)
}

// ParsePID parses a string into a valid process ID.
// Returns ErrInvalidPID if the string is not a valid positive integer.
//
// Example:
//
//	pid, err := jambo.ParsePID(os.Args[1])
//	if err != nil {
//	    log.Fatal("Invalid PID:", err)
//	}
func ParsePID(pidStr string) (int, error) {
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return 0, ErrInvalidPID
	}
	if pid <= 0 {
		return 0, ErrInvalidPID
	}
	return pid, nil
}
