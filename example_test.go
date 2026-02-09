// Package jambo provides examples of how to use the jambo JVM attach library.
package jambo_test

import (
	"fmt"
	"log"

	"github.com/cosmorse/jambo"
)

// Example_simpleAttach demonstrates the simplest way to attach to a JVM process.
func Example_simpleAttach() {
	// Attach to JVM process 12345 and get a thread dump
	output, err := jambo.Attach(12345, "threaddump", nil, true)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(output)
}

// Example_advancedAttach demonstrates using Process for more control.
func Example_advancedAttach() {
	// Create a Process instance
	proc, err := jambo.NewProcess(12345)
	if err != nil {
		log.Fatal(err)
	}

	// Get JVM type information
	fmt.Printf("JVM Type: %v\n", proc.JVM().Type())
	fmt.Printf("Process PID: %d\n", proc.Pid())
	fmt.Printf("Namespace PID: %d\n", proc.NsPid())

	// Configure attach options
	opts := &jambo.Options{
		PrintOutput: true,
		Timeout:     5000, // 5 seconds
	}

	// Execute jcmd command
	output, err := proc.Attach("jcmd", []string{"VM.version"}, opts)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(output)
}

// Example_loadAgent demonstrates how to load a Java agent.
func Example_loadAgent() {
	proc, err := jambo.NewProcess(12345)
	if err != nil {
		log.Fatal(err)
	}

	// Load agent with absolute path
	// Args: [agentPath, isAbsolutePath, options]
	output, err := proc.Attach("load", []string{
		"/path/to/agent.jar",
		"true",                    // absolute path
		"key1=value1,key2=value2", // agent options
	}, nil)

	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Agent loaded:", output)
}

// Example_inspectHeap demonstrates heap inspection.
func Example_inspectHeap() {
	proc, err := jambo.NewProcess(12345)
	if err != nil {
		log.Fatal(err)
	}

	// Get heap histogram
	output, err := proc.Attach("inspectheap", nil, &jambo.Options{
		PrintOutput: true,
	})

	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(output)
}

// Example_getProperties demonstrates getting system properties.
func Example_getProperties() {
	proc, err := jambo.NewProcess(12345)
	if err != nil {
		log.Fatal(err)
	}

	// Get all system properties
	output, err := proc.Attach("properties", nil, &jambo.Options{
		PrintOutput: false, // Don't print to stdout
	})

	if err != nil {
		log.Fatal(err)
	}

	// Parse and use properties as needed
	fmt.Println(output)
}

// Example_jcmd demonstrates executing various jcmd commands.
func Example_jcmd() {
	proc, err := jambo.NewProcess(12345)
	if err != nil {
		log.Fatal(err)
	}

	// Example 1: Get VM version
	output, _ := proc.Attach("jcmd", []string{"VM.version"}, nil)
	fmt.Println("VM Version:", output)

	// Example 2: Get VM flags
	output, _ = proc.Attach("jcmd", []string{"VM.flags"}, nil)
	fmt.Println("VM Flags:", output)

	// Example 3: Run GC
	output, _ = proc.Attach("jcmd", []string{"GC.run"}, nil)
	fmt.Println("GC Result:", output)

	// Example 4: Get thread info
	output, _ = proc.Attach("jcmd", []string{"Thread.print"}, nil)
	fmt.Println(output)
}

// Example_errorHandling demonstrates proper error handling.
func Example_errorHandling() {
	// Parse PID from string
	parsedPID, err := jambo.ParsePID("12345")
	if err != nil {
		log.Fatal("Invalid PID:", err)
	}

	// Create process
	proc, err := jambo.NewProcess(parsedPID)
	if err != nil {
		switch err {
		case jambo.ErrInvalidPID:
			log.Fatal("Invalid process ID")
		case jambo.ErrProcessNotFound:
			log.Fatal("Process not found or not accessible")
		default:
			log.Fatal("Error:", err)
		}
	}

	// Execute command
	output, err := proc.Attach("threaddump", nil, nil)
	if err != nil {
		switch {
		case err == jambo.ErrPermission:
			log.Fatal("Permission denied - try running with sudo")
		case err == jambo.ErrCommandFailed:
			log.Fatal("Command execution failed in JVM")
		default:
			log.Fatal("Error:", err)
		}
	}

	fmt.Println(output)
}

// Example_containerSupport demonstrates attaching to JVMs in containers.
func Example_containerSupport() {
	// When attaching to a containerized JVM, jambo automatically
	// handles namespace switching and uses the correct namespace PID

	proc, err := jambo.NewProcess(12345) // Host PID
	if err != nil {
		log.Fatal(err)
	}

	// Check if process is in a container
	if proc.NsPid() != proc.Pid() {
		fmt.Printf("Container detected - Host PID: %d, NS PID: %d\n",
			proc.Pid(), proc.NsPid())
	}

	// Attach works the same way for containerized processes
	output, err := proc.Attach("threaddump", nil, nil)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(output)
}

// Example_multipleCommands demonstrates executing multiple commands.
func Example_multipleCommands() {
	proc, err := jambo.NewProcess(12345)
	if err != nil {
		log.Fatal(err)
	}

	commands := []struct {
		name string
		cmd  string
		args []string
	}{
		{"Thread Dump", "threaddump", nil},
		{"Heap Summary", "jcmd", []string{"GC.heap_info"}},
		{"VM Info", "jcmd", []string{"VM.info"}},
	}

	for _, c := range commands {
		fmt.Printf("\\n=== %s ===\\n", c.name)
		output, err := proc.Attach(c.cmd, c.args, &jambo.Options{
			PrintOutput: false,
		})
		if err != nil {
			log.Printf("Warning: %s failed: %v", c.name, err)
			continue
		}
		fmt.Println(output)
	}
}
