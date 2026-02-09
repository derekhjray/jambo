package jambo

import (
	"testing"
)

func TestParsePID(t *testing.T) {
	tests := []struct {
		input    string
		expected int
		hasError bool
	}{
		{"123", 123, false},
		{"0", 0, true},
		{"-1", 0, true},
		{"abc", 0, true},
		{"", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			pid, err := ParsePID(tt.input)
			if tt.hasError {
				if err == nil {
					t.Errorf("ParsePID(%q) expected error, got nil", tt.input)
				}
			} else {
				if err != nil {
					t.Errorf("ParsePID(%q) unexpected error: %v", tt.input, err)
				}
				if pid != tt.expected {
					t.Errorf("ParsePID(%q) = %d, want %d", tt.input, pid, tt.expected)
				}
			}
		})
	}
}

func TestNewProcess_InvalidPID(t *testing.T) {
	tests := []struct {
		pid int
	}{
		{0},
		{-1},
		{-100},
	}

	for _, tt := range tests {
		t.Run(string(rune(tt.pid)), func(t *testing.T) {
			_, err := NewProcess(tt.pid)
			if err == nil {
				t.Errorf("NewProcess(%d) expected error, got nil", tt.pid)
			}
			if err != ErrInvalidPID && err.Error() != ErrInvalidPID.Error() {
				t.Errorf("NewProcess(%d) error = %v, want ErrInvalidPID", tt.pid, err)
			}
		})
	}
}

func TestProcess_DetectJVM(t *testing.T) {
	// This is a basic test that doesn't require actual processes
	// In real tests, we would mock the platform-specific functions
	t.Run("MockProcess", func(t *testing.T) {
		proc := &Process{
			pid:   12345,
			nsPid: 12345,
		}

		// This will call the actual platform-specific function
		// On non-Linux platforms, it will return false
		// On Linux, it will try to read from /proc
		proc.detectJVM()

		// We can't assert the exact type since it depends on the system
		// Just ensure it doesn't panic and jvm is set
		if proc.jvm == nil {
			t.Error("JVM should be initialized")
		}

		// Verify Type() method works
		jvmType := proc.jvm.Type()
		if jvmType < HotSpot || jvmType > Unknown {
			t.Errorf("Invalid JVM type: %v", jvmType)
		}
	})
}

func TestAttachOptions_Default(t *testing.T) {
	opts := &Options{}
	if opts.PrintOutput != false {
		t.Errorf("Default PrintOutput = %v, want false", opts.PrintOutput)
	}
	if opts.Timeout != 0 {
		t.Errorf("Default Timeout = %d, want 0", opts.Timeout)
	}
}

func TestErrorMessages(t *testing.T) {
	if ErrProcessNotFound.Error() == "" {
		t.Error("ErrProcessNotFound should have an error message")
	}
	if ErrInvalidPID.Error() == "" {
		t.Error("ErrInvalidPID should have an error message")
	}
	if ErrPermission.Error() == "" {
		t.Error("ErrPermission should have an error message")
	}
	if ErrCommandFailed.Error() == "" {
		t.Error("ErrCommandFailed should have an error message")
	}
}

func TestOpenJ9CommandTranslation(t *testing.T) {
	o9 := &openJ9{}

	tests := []struct {
		name     string
		args     []string
		expected string
	}{
		{
			name:     "load with absolute path",
			args:     []string{"load", "/path/to/agent.so", "true", "option=value"},
			expected: "ATTACH_LOADAGENTPATH(/path/to/agent.so,option=value)",
		},
		{
			name:     "load with relative path",
			args:     []string{"load", "agent.so", "false", "option=value"},
			expected: "ATTACH_LOADAGENT(agent.so,option=value)",
		},
		{
			name:     "load without options",
			args:     []string{"load", "agent.so"},
			expected: "ATTACH_LOADAGENT(agent.so,)",
		},
		{
			name:     "jcmd with arguments",
			args:     []string{"jcmd", "VM.version"},
			expected: "ATTACH_DIAGNOSTICS:VM.version",
		},
		{
			name:     "jcmd with multiple arguments",
			args:     []string{"jcmd", "GC.heap_info", "arg1", "arg2"},
			expected: "ATTACH_DIAGNOSTICS:GC.heap_info,arg1,arg2",
		},
		{
			name:     "jcmd without arguments",
			args:     []string{"jcmd"},
			expected: "ATTACH_DIAGNOSTICS:help",
		},
		{
			name:     "threaddump",
			args:     []string{"threaddump"},
			expected: "ATTACH_DIAGNOSTICS:Thread.print,",
		},
		{
			name:     "threaddump with options",
			args:     []string{"threaddump", "options"},
			expected: "ATTACH_DIAGNOSTICS:Thread.print,options",
		},
		{
			name:     "dumpheap",
			args:     []string{"dumpheap"},
			expected: "ATTACH_DIAGNOSTICS:Dump.heap,",
		},
		{
			name:     "dumpheap with file",
			args:     []string{"dumpheap", "/tmp/heap.dump"},
			expected: "ATTACH_DIAGNOSTICS:Dump.heap,/tmp/heap.dump",
		},
		{
			name:     "inspectheap",
			args:     []string{"inspectheap"},
			expected: "ATTACH_DIAGNOSTICS:GC.class_histogram,",
		},
		{
			name:     "datadump",
			args:     []string{"datadump"},
			expected: "ATTACH_DIAGNOSTICS:Dump.java,",
		},
		{
			name:     "properties",
			args:     []string{"properties"},
			expected: "ATTACH_GETSYSTEMPROPERTIES",
		},
		{
			name:     "agentProperties",
			args:     []string{"agentProperties"},
			expected: "ATTACH_GETAGENTPROPERTIES",
		},
		{
			name:     "unknown command",
			args:     []string{"unknownCommand"},
			expected: "unknownCommand",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := o9.translateCommand(tt.args)
			if result != tt.expected {
				t.Errorf("translateCommand(%v) = %q, want %q", tt.args, result, tt.expected)
			}
		})
	}
}

func TestOpenJ9UnescapeString(t *testing.T) {
	o9 := &openJ9{}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no escape sequences",
			input:    "Hello World",
			expected: "Hello World",
		},
		{
			name:     "newline escape",
			input:    "Line1\\nLine2",
			expected: "Line1\nLine2",
		},
		{
			name:     "tab escape",
			input:    "Column1\\tColumn2",
			expected: "Column1\tColumn2",
		},
		{
			name:     "carriage return escape",
			input:    "Text\\rOverwrite",
			expected: "Text\rOverwrite",
		},
		{
			name:     "form feed escape",
			input:    "Page1\\fPage2",
			expected: "Page1\fPage2",
		},
		{
			name:     "other escape",
			input:    "Quote\\\"Test\\\"",
			expected: "Quote\"Test\"",
		},
		{
			name:     "trailing newline removed",
			input:    "Hello\n",
			expected: "Hello",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := o9.unescapeString(tt.input)
			if result != tt.expected {
				t.Errorf("unescapeString(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}
