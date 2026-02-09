//go:build windows

// Windows-specific implementation of JVM attach mechanism.
// This file implements HotSpot JVM attach using remote thread injection.
//
// Windows Attach Mechanism:
//   - Uses remote thread injection into target JVM process
//   - Calls JVM_EnqueueOperation exported by jvm.dll
//   - Communicates via Named Pipes
//   - Requires Administrator privileges or SeDebugPrivilege
//
// Security Notes:
//   - Remote thread injection may be flagged by antivirus software
//   - Requires appropriate permissions to access target process
//   - Bitness must match (32-bit jambo for 32-bit JVM, 64-bit for 64-bit)
package jambo

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strconv"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	// Windows DLL handles
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	advapi32 = windows.NewLazySystemDLL("advapi32.dll")

	// kernel32.dll functions
	procCreateRemoteThread = kernel32.NewProc("CreateRemoteThread")
	procVirtualAllocEx     = kernel32.NewProc("VirtualAllocEx")
	procVirtualFreeEx      = kernel32.NewProc("VirtualFreeEx")
	procWriteProcessMemory = kernel32.NewProc("WriteProcessMemory")
	procGetExitCodeThread  = kernel32.NewProc("GetExitCodeThread")
	procGetTickCount       = kernel32.NewProc("GetTickCount")

	// advapi32.dll functions
	procLookupPrivilegeValueW = advapi32.NewProc("LookupPrivilegeValueW")
	procAdjustTokenPrivileges = advapi32.NewProc("AdjustTokenPrivileges")
	procConvertStringSecurity = advapi32.NewProc("ConvertStringSecurityDescriptorToSecurityDescriptorW")
)

const (
	// Process access rights
	PROCESS_ALL_ACCESS = 0x1F0FFF

	// Memory allocation types
	MEM_COMMIT  = 0x1000
	MEM_RELEASE = 0x8000

	// Memory protection constants
	PAGE_EXECUTE_READWRITE = 0x40
	PAGE_READWRITE         = 0x04

	// Named pipe constants
	PIPE_ACCESS_INBOUND = 0x01
	PIPE_TYPE_BYTE      = 0x00
	PIPE_READMODE_BYTE  = 0x00
	PIPE_WAIT           = 0x00

	// Error codes
	ERROR_ACCESS_DENIED = 5

	// Security constants
	SE_PRIVILEGE_ENABLED    = 0x00000002
	TOKEN_ADJUST_PRIVILEGES = 0x0020
	SecurityImpersonation   = 2
	SDDL_REVISION_1         = 1
)

// callData is the structure passed to the remote thread.
// This structure is allocated in the target process's memory space
// and contains all the information needed for the remote thread to execute.
type callData struct {
	GetModuleHandleA uintptr       // Pointer to kernel32!GetModuleHandleA
	GetProcAddress   uintptr       // Pointer to kernel32!GetProcAddress
	StrJvm           [32]byte      // "jvm" string for GetModuleHandle
	StrEnqueue       [32]byte      // "_JVM_EnqueueOperation" string
	PipeName         [260]byte     // Named pipe path (MAX_PATH)
	Args             [4][1024]byte // Command arguments
}

func getProcessInfo(pid int) (uid, gid, nspid int, err error) {
	// Windows doesn't use UID/GID like Unix
	return 0, 0, pid, nil
}

func enterNamespace(pid int, nsType string) error {
	// Windows doesn't have Linux namespaces
	return nil
}

func setCredentials(uid, gid int) error {
	// Not applicable on Windows
	return nil
}

func getTempPath(pid int) (string, error) {
	path := os.Getenv("JAMBO_ATTACH_PATH")
	if path != "" {
		return path, nil
	}
	return os.TempDir(), nil
}

// hotSpot implements JVM interface for HotSpot JVM on Windows.
// Uses remote thread injection technique to call JVM_EnqueueOperation.
type hotSpot struct{}

func (h *hotSpot) Type() JVMType {
	return HotSpot
}

func (h *hotSpot) Detect(nspid int) bool {
	// On Windows, we always default to HotSpot
	// OpenJ9 detection would require more complex logic
	return true
}

// Attach performs the attach operation for HotSpot JVM on Windows.
//
// Windows attach process:
//  1. Create a Named Pipe for communication
//  2. Inject shellcode into target process memory
//  3. Allocate callData structure in target process
//  4. Create remote thread to execute shellcode
//  5. Wait for thread completion
//  6. Read response from Named Pipe
//
// The injected shellcode:
//  1. Calls GetModuleHandleA("jvm") to get jvm.dll handle
//  2. Calls GetProcAddress to find JVM_EnqueueOperation
//  3. Calls JVM_EnqueueOperation with command arguments
//  4. JVM writes result to the Named Pipe
//
// Security requirements:
//   - Administrator privileges OR SeDebugPrivilege
//   - Target process must be accessible
//   - Bitness must match (32-bit/64-bit)
//
// Error codes:
//   - 1001: Could not load JVM module (jvm.dll)
//   - 1002: Could not find JVM_EnqueueOperation function
func (h *hotSpot) Attach(pid, nspid int, args []string, printOutput bool, tmpPath string) (string, error) {
	// Create named pipe for communication
	pipeName, pipe, err := h.createPipe()
	if err != nil {
		return "", fmt.Errorf("failed to create pipe: %v", err)
	}
	defer windows.CloseHandle(pipe)

	// Inject remote thread into target process
	if err := h.injectThread(pid, pipeName, args); err != nil {
		return "", fmt.Errorf("failed to inject thread: %v", err)
	}

	// Read response from pipe
	output, err := h.readResponse(pipe)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %v", err)
	}

	if printOutput {
		fmt.Print(output)
	}

	return output, nil
}

func (h *hotSpot) createPipe() (string, windows.Handle, error) {
	// Get tick count for unique pipe name
	ret, _, _ := procGetTickCount.Call()
	tickCount := uint32(ret)

	pipeName := fmt.Sprintf(`\\.\pipe\javatool%d`, tickCount)

	// Create security descriptor: allow read-write access to everyone
	secDesc := "D:(A;;GRGW;;;WD)"
	secDescUTF16, _ := syscall.UTF16PtrFromString(secDesc)
	var pSecDesc uintptr
	ret, _, err := procConvertStringSecurity.Call(
		uintptr(unsafe.Pointer(secDescUTF16)),
		SDDL_REVISION_1,
		uintptr(unsafe.Pointer(&pSecDesc)),
		0,
	)
	if ret == 0 {
		return "", 0, fmt.Errorf("failed to convert security descriptor: %v", err)
	}
	defer windows.LocalFree(windows.Handle(pSecDesc))

	// Create named pipe
	pipeNameUTF16, _ := syscall.UTF16PtrFromString(pipeName)
	pipe, err := windows.CreateNamedPipe(
		pipeNameUTF16,
		PIPE_ACCESS_INBOUND,
		PIPE_TYPE_BYTE|PIPE_READMODE_BYTE|PIPE_WAIT,
		1,    // nMaxInstances
		4096, // nOutBufferSize
		8192, // nInBufferSize
		0,    // nDefaultTimeOut
		(*windows.SecurityAttributes)(unsafe.Pointer(pSecDesc)),
	)
	if err != nil {
		return "", 0, err
	}

	return pipeName, pipe, nil
}

func (h *hotSpot) injectThread(pid int, pipeName string, args []string) error {
	// Open target process
	hProcess, err := windows.OpenProcess(PROCESS_ALL_ACCESS, false, uint32(pid))
	if err != nil {
		if err == syscall.Errno(ERROR_ACCESS_DENIED) {
			// Try to enable debug privileges
			if err := enableDebugPrivileges(); err == nil {
				hProcess, err = windows.OpenProcess(PROCESS_ALL_ACCESS, false, uint32(pid))
			}
		}
		if err != nil {
			return fmt.Errorf("could not open process: %v", err)
		}
	}
	defer windows.CloseHandle(hProcess)

	// Check bitness compatibility
	if err := checkBitness(hProcess); err != nil {
		return err
	}

	// Allocate code in remote process
	remoteCode, err := h.allocateCode(hProcess)
	if err != nil {
		return fmt.Errorf("could not allocate code: %v", err)
	}
	defer h.freeMemory(hProcess, remoteCode)

	// Allocate data in remote process
	remoteData, err := h.allocateData(hProcess, pipeName, args)
	if err != nil {
		return fmt.Errorf("could not allocate data: %v", err)
	}
	defer h.freeMemory(hProcess, remoteData)

	// Create remote thread
	var threadID uint32
	ret, _, err := procCreateRemoteThread.Call(
		uintptr(hProcess),
		0, // lpThreadAttributes
		0, // dwStackSize
		remoteCode,
		remoteData,
		0, // dwCreationFlags
		uintptr(unsafe.Pointer(&threadID)),
	)
	if ret == 0 {
		return fmt.Errorf("could not create remote thread: %v", err)
	}
	hThread := windows.Handle(ret)
	defer windows.CloseHandle(hThread)

	// Wait for thread to complete
	_, err = windows.WaitForSingleObject(hThread, windows.INFINITE)
	if err != nil {
		return fmt.Errorf("error waiting for thread: %v", err)
	}

	// Get thread exit code
	var exitCode uint32
	ret, _, err = procGetExitCodeThread.Call(
		uintptr(hThread),
		uintptr(unsafe.Pointer(&exitCode)),
	)
	if ret == 0 {
		return fmt.Errorf("could not get exit code: %v", err)
	}

	if exitCode != 0 {
		return fmt.Errorf("attach failed with code %d", exitCode)
	}

	return nil
}

// allocateCode allocates and writes the remote thread code
func (h *hotSpot) allocateCode(hProcess windows.Handle) (uintptr, error) {
	// Get shellcode for the remote thread
	// This shellcode will:
	// 1. Call GetModuleHandleA("jvm") to get jvm.dll handle
	// 2. Call GetProcAddress to find JVM_EnqueueOperation
	// 3. Call JVM_EnqueueOperation with the arguments
	// 4. Return the result

	shellcode := getRemoteThreadShellcode()

	// Allocate executable memory in remote process
	ret, _, err := procVirtualAllocEx.Call(
		uintptr(hProcess),
		0,
		uintptr(len(shellcode)),
		MEM_COMMIT,
		PAGE_EXECUTE_READWRITE,
	)
	if ret == 0 {
		return 0, err
	}
	remoteCode := ret

	// Write shellcode to remote process
	var written uintptr
	ret, _, err = procWriteProcessMemory.Call(
		uintptr(hProcess),
		remoteCode,
		uintptr(unsafe.Pointer(&shellcode[0])),
		uintptr(len(shellcode)),
		uintptr(unsafe.Pointer(&written)),
	)
	if ret == 0 {
		procVirtualFreeEx.Call(uintptr(hProcess), remoteCode, 0, MEM_RELEASE)
		return 0, err
	}

	return remoteCode, nil
}

// getRemoteThreadShellcode returns the machine code for the remote thread
func getRemoteThreadShellcode() []byte {
	// Check if we're running on 64-bit or 32-bit Windows
	// and return the appropriate shellcode
	var thisWow64 bool
	err := windows.IsWow64Process(windows.CurrentProcess(), &thisWow64)
	if err != nil {
		// Default to x64 if we can't determine
		return remoteThreadShellcodeX64
	}

	if thisWow64 {
		// 32-bit process
		return remoteThreadShellcodeX86
	}

	// 64-bit process
	return remoteThreadShellcodeX64
}

func (h *hotSpot) allocateData(hProcess windows.Handle, pipeName string, args []string) (uintptr, error) {
	var data callData

	// Set function pointers (these need to be resolved in the remote process)
	kernel32Handle, _ := syscall.LoadLibrary("kernel32.dll")
	data.GetModuleHandleA, _ = syscall.GetProcAddress(kernel32Handle, "GetModuleHandleA")
	data.GetProcAddress, _ = syscall.GetProcAddress(kernel32Handle, "GetProcAddress")

	// Set strings
	copy(data.StrJvm[:], "jvm\x00")
	copy(data.StrEnqueue[:], "_JVM_EnqueueOperation\x00")
	copy(data.PipeName[:], pipeName+"\x00")

	// Set arguments
	cmdArgs := len(args)
	if cmdArgs >= 2 && args[0] == "jcmd" {
		cmdArgs = 2
	} else if cmdArgs >= 4 {
		cmdArgs = 4
	}

	for i := 0; i < len(args) && i < 4; i++ {
		if i < cmdArgs {
			copy(data.Args[i][:], args[i]+"\x00")
		}
	}

	// Allocate memory in remote process
	ret, _, err := procVirtualAllocEx.Call(
		uintptr(hProcess),
		0,
		unsafe.Sizeof(data),
		MEM_COMMIT,
		PAGE_READWRITE,
	)
	if ret == 0 {
		return 0, err
	}
	remoteData := ret

	// Write data to remote process
	var written uintptr
	ret, _, err = procWriteProcessMemory.Call(
		uintptr(hProcess),
		remoteData,
		uintptr(unsafe.Pointer(&data)),
		unsafe.Sizeof(data),
		uintptr(unsafe.Pointer(&written)),
	)
	if ret == 0 {
		procVirtualFreeEx.Call(uintptr(hProcess), remoteData, 0, MEM_RELEASE)
		return 0, err
	}

	return remoteData, nil
}

func (h *hotSpot) freeMemory(hProcess windows.Handle, addr uintptr) {
	procVirtualFreeEx.Call(uintptr(hProcess), addr, 0, MEM_RELEASE)
}

func (h *hotSpot) readResponse(pipe windows.Handle) (string, error) {
	// Wait for client connection with timeout
	err := windows.ConnectNamedPipe(pipe, nil)
	if err != nil && err != windows.ERROR_PIPE_CONNECTED {
		return "", fmt.Errorf("failed to connect pipe: %v", err)
	}

	// Read all response data
	var allData []byte
	buf := make([]byte, 8192)

	for {
		var bytesRead uint32
		err := windows.ReadFile(pipe, buf, &bytesRead, nil)

		if bytesRead > 0 {
			allData = append(allData, buf[:bytesRead]...)
		}

		if err != nil {
			if err == windows.ERROR_BROKEN_PIPE || err == windows.ERROR_NO_DATA {
				// End of data
				break
			}
			return "", fmt.Errorf("failed to read response: %v", err)
		}

		if bytesRead == 0 {
			break
		}
	}

	if len(allData) == 0 {
		return "", errors.New("no data received from JVM")
	}

	// Parse response
	// First line is the result code
	lines := bytes.SplitN(allData, []byte("\n"), 2)

	var resultCode int
	if len(lines) > 0 {
		if code, err := strconv.Atoi(string(bytes.TrimSpace(lines[0]))); err == nil {
			resultCode = code
		}
	}

	// Get output (everything after first line)
	var output string
	if len(lines) > 1 {
		output = string(lines[1])
	}

	// Check for errors
	if resultCode != 0 {
		if output != "" {
			return output, fmt.Errorf("command failed with code %d: %s", resultCode, output)
		}
		return "", fmt.Errorf("command failed with code %d", resultCode)
	}

	return output, nil
}

func enableDebugPrivileges() error {
	var token windows.Token
	err := windows.OpenThreadToken(windows.CurrentThread(), TOKEN_ADJUST_PRIVILEGES, false, &token)
	if err != nil {
		if err := windows.ImpersonateSelf(SecurityImpersonation); err != nil {
			return err
		}
		if err := windows.OpenThreadToken(windows.CurrentThread(), TOKEN_ADJUST_PRIVILEGES, false, &token); err != nil {
			return err
		}
	}
	defer token.Close()

	var luid windows.LUID
	debugName, _ := syscall.UTF16PtrFromString("SeDebugPrivilege")
	ret, _, err := procLookupPrivilegeValueW.Call(
		0,
		uintptr(unsafe.Pointer(debugName)),
		uintptr(unsafe.Pointer(&luid)),
	)
	if ret == 0 {
		return err
	}

	tp := windows.Tokenprivileges{
		PrivilegeCount: 1,
		Privileges: [1]windows.LUIDAndAttributes{
			{
				Luid:       luid,
				Attributes: SE_PRIVILEGE_ENABLED,
			},
		},
	}

	ret, _, err = procAdjustTokenPrivileges.Call(
		uintptr(token),
		0,
		uintptr(unsafe.Pointer(&tp)),
		unsafe.Sizeof(tp),
		0,
		0,
	)
	if ret == 0 {
		return err
	}

	return nil
}

func checkBitness(hProcess windows.Handle) error {
	// Check if we're trying to attach 64-bit to 32-bit or vice versa
	var targetWow64 bool
	err := windows.IsWow64Process(hProcess, &targetWow64)
	if err != nil {
		return err
	}

	var thisWow64 bool
	err = windows.IsWow64Process(windows.CurrentProcess(), &thisWow64)
	if err != nil {
		return err
	}

	if thisWow64 != targetWow64 {
		return errors.New("cannot attach: bitness mismatch (32-bit vs 64-bit)")
	}

	return nil
}

// openJ9 implements JVM interface for OpenJ9 JVM on Windows
type openJ9 struct{}

func (o *openJ9) Type() JVMType {
	return OpenJ9
}

func (o *openJ9) Detect(nspid int) bool {
	// OpenJ9 detection on Windows is not implemented
	return false
}

func (o *openJ9) Attach(pid, nspid int, args []string, printOutput bool, tmpPath string) (string, error) {
	return "", errors.New("OpenJ9 attach not supported on Windows")
}

func (o *openJ9) translateCommand(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return ""
}

func (o *openJ9) unescapeString(s string) string {
	return s
}
