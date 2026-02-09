// jambo is a command-line tool for attaching to running JVM processes.
// It provides a simple interface to execute various JVM diagnostic commands.
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/cosmorse/jambo"
)

// version will be set by ldflags during build
var version = "dev"

// printUsage prints the help message with command descriptions and examples.
func printUsage() {
	fmt.Printf("jambo %s - JVM Dynamic Attach Utility (Go version)\n", version)
	fmt.Println()
	fmt.Println("Usage: jambo <pid> <cmd> [args ...]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("    load            : load agent library")
	fmt.Println("                      Args: <agentPath> [isAbsolute] [options]")
	fmt.Println("    properties      : print system properties")
	fmt.Println("    agentProperties : print agent properties")
	fmt.Println("    datadump        : show heap and thread summary (OpenJ9)")
	fmt.Println("    threaddump      : dump all stack traces (like jstack)")
	fmt.Println("    dumpheap        : dump heap to file (like jmap)")
	fmt.Println("                      Args: [fileName]")
	fmt.Println("    inspectheap     : heap histogram (like jmap -histo)")
	fmt.Println("    setflag         : modify manageable VM flag")
	fmt.Println("                      Args: <flagName> <value>")
	fmt.Println("    printflag       : print VM flag value")
	fmt.Println("                      Args: <flagName>")
	fmt.Println("    jcmd            : execute arbitrary jcmd command")
	fmt.Println("                      Args: <command> [args...]")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("    # Get thread dump")
	fmt.Println("    jambo <pid> threaddump")
	fmt.Println()
	fmt.Println("    # Load Java agent")
	fmt.Println("    jambo <pid> load /path/to/agent.jar true options=value")
	fmt.Println()
	fmt.Println("    # Execute jcmd commands")
	fmt.Println("    jambo <pid> jcmd help")
	fmt.Println("    jambo <pid> jcmd VM.version")
	fmt.Println("    jambo <pid> jcmd GC.heap_info")
	fmt.Println()
	fmt.Println("    # Get system properties")
	fmt.Println("    jambo <pid> properties")
	fmt.Println()
	fmt.Println("    # Heap dump")
	fmt.Println("    jambo <pid> dumpheap /tmp/heap.hprof")
	fmt.Println()
	fmt.Println("    # Heap histogram")
	fmt.Println("    jambo <pid> inspectheap")
	fmt.Println()
	fmt.Println("Platform Support:")
	fmt.Println("    Linux   : Full support (HotSpot + OpenJ9, container-aware)")
	fmt.Println("    Windows : HotSpot support (requires Administrator privileges)")
	fmt.Println()
	fmt.Println("Environment Variables:")
	fmt.Println("    JAMBO_ATTACH_PATH : Override default temporary path for attach files")
}

func main() {
	if len(os.Args) < 3 {
		printUsage()
		os.Exit(1)
	}

	pidStr := os.Args[1]
	command := os.Args[2]
	args := os.Args[3:]

	pid, err := jambo.ParsePID(pidStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s is not a valid process ID\n", pidStr)
		os.Exit(1)
	}

	output, err := jambo.Attach(pid, command, args, true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)

		if strings.Contains(err.Error(), "process not found") {
			fmt.Fprintf(os.Stderr, "Process %d not found or not accessible\n", pid)
		} else if strings.Contains(err.Error(), "permission denied") {
			fmt.Fprintf(os.Stderr, "Permission denied. Try running with sudo\n")
		}

		os.Exit(1)
	}

	if output != "" {
		fmt.Print(output)
	}
}
