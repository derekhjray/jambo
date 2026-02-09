//go:build linux

// Linux-specific implementation of JVM attach mechanism.
// This file contains implementations for both HotSpot and OpenJ9 JVMs on Linux.
package jambo

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	procPath   = "/proc"            // Linux /proc filesystem path
	nsPath     = "/proc/%d/ns/%s"   // Namespace path pattern
	statusPath = "/proc/%d/status"  // Process status file pattern
	attachPath = "/tmp/.java_pid%d" // HotSpot attach file pattern
)

// hotSpot implements JVM interface for HotSpot JVM on Linux.
// HotSpot uses Unix domain sockets for attach communication.
type hotSpot struct{}

// Type returns the JVM type (HotSpot).
func (h *hotSpot) Type() JVMType {
	return HotSpot
}

// Attach performs the attach operation for HotSpot JVM.
//
// The attach process:
//  1. Check if attach socket already exists
//  2. If not, create attach file and send SIGQUIT to trigger JVM attach listener
//  3. Connect to the Unix domain socket
//  4. Send command with arguments
//  5. Read and parse response
//
// HotSpot attach protocol:
//   - Uses Unix domain sockets at /tmp/.java_pid{nspid}
//   - Protocol version 1
//   - Commands sent as null-terminated strings
//   - Response includes status code and output
func (h *hotSpot) Attach(pid, nspid int, args []string, printOutput bool, tmpPath string) (string, error) {
	// Ignore SIGPIPE to prevent abnormal process termination
	// Make write() return EPIPE instead of terminating the process
	signal.Ignore(syscall.SIGPIPE)

	socketPath := fmt.Sprintf("%s/.java_pid%d", tmpPath, nspid)

	if !h.checkSocket(socketPath) {
		if err := h.startAttachMechanism(pid, nspid, tmpPath); err != nil {
			return "", fmt.Errorf("failed to start attach mechanism: %v", err)
		}
	}

	conn, err := connectToSocket(socketPath)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	if err := h.sendCommand(conn, args); err != nil {
		return "", err
	}

	output, err := h.readResponse(conn, args)
	if err != nil {
		return "", err
	}

	if printOutput {
		fmt.Print(output)
	}

	return output, nil
}

// Detect checks if the process is a HotSpot JVM.
// Always returns true as HotSpot is used as the fallback/default JVM type.
func (h *hotSpot) Detect(nspid int) bool {
	return true // HotSpot is the default
}

// openJ9 implements JVM interface for OpenJ9 JVM on Linux.
// OpenJ9 uses a different attach mechanism than HotSpot.
type openJ9 struct{}

// getProcessInfo retrieves process information from /proc/{pid}/status.
// Returns:
//   - uid: User ID of the process owner
//   - gid: Group ID of the process owner
//   - nspid: Namespace PID (for container support, same as pid if not in container)
//   - err: Any error encountered
//
// The namespace PID (NSpid) is important for container support.
// In containers, the PID inside the namespace differs from the host PID.
func getProcessInfo(pid int) (uid, gid, nspid int, err error) {
	statusFile := fmt.Sprintf(statusPath, pid)
	file, err := os.Open(statusFile)
	if err != nil {
		return 0, 0, 0, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	var uidStr, gidStr string
	nspidFound := false

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "Uid:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				uidStr = fields[1]
			}
		} else if strings.HasPrefix(line, "Gid:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				gidStr = fields[1]
			}
		} else if strings.HasPrefix(line, "NStgid:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				nspid, _ = strconv.Atoi(fields[len(fields)-1])
				nspidFound = true
			}
		}
	}

	if uidStr == "" || gidStr == "" {
		return 0, 0, 0, errors.New("failed to parse process info")
	}

	uid, _ = strconv.Atoi(uidStr)
	gid, _ = strconv.Atoi(gidStr)

	if !nspidFound {
		nspid = altLookupNSPID(pid)
	}

	return uid, gid, nspid, nil
}

func schedGetHostPID(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return -1
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 {
		return -1
	}

	line := lines[0]
	openParen := strings.LastIndex(line, "(")
	if openParen == -1 {
		return -1
	}

	pidStr := ""
	for i := openParen + 1; i < len(line); i++ {
		if line[i] >= '0' && line[i] <= '9' {
			pidStr += string(line[i])
		} else if pidStr != "" {
			break
		}
	}

	if pidStr == "" {
		return -1
	}

	pid, _ := strconv.Atoi(pidStr)
	return pid
}

func altLookupNSPID(pid int) int {
	nsFile := fmt.Sprintf("/proc/%d/ns/pid", pid)

	var oldStat, newStat syscall.Stat_t
	if syscall.Stat("/proc/self/ns/pid", &oldStat) == nil &&
		syscall.Stat(nsFile, &newStat) == nil {
		if oldStat.Ino == newStat.Ino {
			return pid
		}
	}

	procPath := fmt.Sprintf("/proc/%d/root/proc", pid)
	dir, err := os.Open(procPath)
	if err != nil {
		return pid
	}
	defer dir.Close()

	entries, err := dir.Readdirnames(-1)
	if err != nil {
		return pid
	}

	for _, entry := range entries {
		if len(entry) > 0 && entry[0] >= '1' && entry[0] <= '9' {
			schedPath := fmt.Sprintf("/proc/%d/root/proc/%s/sched", pid, entry)
			if schedGetHostPID(schedPath) == pid {
				if containerPID, err := strconv.Atoi(entry); err == nil {
					return containerPID
				}
			}
		}
	}

	return pid
}

func (h *hotSpot) checkSocket(path string) bool {
	var stat syscall.Stat_t
	if err := syscall.Stat(path, &stat); err != nil {
		return false
	}
	return stat.Mode&syscall.S_IFSOCK != 0
}

func getFileOwner(path string) int {
	var stat syscall.Stat_t
	if err := syscall.Stat(path, &stat); err != nil {
		return -1
	}
	return int(stat.Uid)
}

func (h *hotSpot) startAttachMechanism(pid, nspid int, tmpPath string) error {
	// Try current directory first
	path := fmt.Sprintf("/proc/%d/cwd/.attach_pid%d", nspid, nspid)
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_WRONLY, 0660)
	if err != nil || (syscall.Close(fd) == nil && getFileOwner(path) != os.Geteuid()) {
		syscall.Unlink(path)

		// Try /tmp
		path = fmt.Sprintf("%s/.attach_pid%d", tmpPath, nspid)
		fd, err = syscall.Open(path, syscall.O_CREAT|syscall.O_WRONLY, 0660)
		if err != nil {
			return err
		}
		syscall.Close(fd)
	}

	// Send SIGQUIT to trigger attach mechanism
	if err := syscall.Kill(pid, syscall.SIGQUIT); err != nil {
		return err
	}

	// Wait for socket to appear with incremental backoff
	// Start with 20ms and increment by 20ms each iteration up to 500ms
	// Total timeout is approximately 6000ms
	socketPath := fmt.Sprintf("%s/.java_pid%d", tmpPath, nspid)
	delay := 20 * time.Millisecond

	for delay < 500*time.Millisecond {
		time.Sleep(delay)
		if h.checkSocket(socketPath) {
			syscall.Unlink(path)
			return nil
		}

		// Check if process still exists
		if err := syscall.Kill(pid, 0); err != nil {
			break
		}

		delay += 20 * time.Millisecond
	}

	syscall.Unlink(path)
	return errors.New("timeout waiting for attach socket")
}

func enterNamespace(pid int, nsType string) error {
	nsFile := fmt.Sprintf(nsPath, pid, nsType)
	selfNsFile := fmt.Sprintf("/proc/self/ns/%s", nsType)

	// Check if we're already in the same namespace
	var oldStat, newStat syscall.Stat_t
	if err := syscall.Stat(selfNsFile, &oldStat); err == nil {
		if err := syscall.Stat(nsFile, &newStat); err == nil {
			if oldStat.Ino == newStat.Ino {
				// Already in the same namespace
				return nil
			}
		}
	}

	fd, err := syscall.Open(nsFile, syscall.O_RDONLY, 0)
	if err != nil {
		// If we can't open the namespace file, it's likely we're in the same namespace already
		return nil
	}
	defer syscall.Close(fd)

	// Try to enter the namespace using setns syscall
	// Note: This requires CAP_SYS_ADMIN capability
	err = unix.Setns(fd, 0)
	if err != nil {
		// Fail silently if we don't have permission
		// The attach may still work if we're in the same namespace
		return nil
	}

	return nil
}

func setCredentials(uid, gid int) error {
	if err := syscall.Setregid(-1, gid); err != nil {
		return err
	}
	if err := syscall.Setreuid(-1, uid); err != nil {
		return err
	}
	return nil
}

func getTempPath(pid int) (string, error) {
	// Check environment variable first
	path := os.Getenv("JAMBO_ATTACH_PATH")
	if path != "" {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}

	// Try process root /tmp (for containers)
	path = fmt.Sprintf("/proc/%d/root/tmp", pid)
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}

	// Fallback to /tmp
	return "/tmp", nil
}

// Type returns the JVM type
func (o *openJ9) Type() JVMType {
	return OpenJ9
}

// translateCommand translates HotSpot commands to OpenJ9 equivalents
func (o *openJ9) translateCommand(args []string) string {
	if len(args) == 0 {
		return ""
	}

	cmd := args[0]

	// load command: ATTACH_LOADAGENT or ATTACH_LOADAGENTPATH
	if cmd == "load" && len(args) >= 2 {
		agentPath := args[1]
		options := ""
		if len(args) > 3 {
			options = args[3]
		}

		// Check if absolute path (third argument is "true")
		if len(args) > 2 && args[2] == "true" {
			return fmt.Sprintf("ATTACH_LOADAGENTPATH(%s,%s)", agentPath, options)
		}
		return fmt.Sprintf("ATTACH_LOADAGENT(%s,%s)", agentPath, options)
	}

	// jcmd command: ATTACH_DIAGNOSTICS with comma-separated arguments
	if cmd == "jcmd" {
		if len(args) > 1 {
			// Join all arguments after "jcmd" with commas
			return "ATTACH_DIAGNOSTICS:" + strings.Join(args[1:], ",")
		}
		return "ATTACH_DIAGNOSTICS:help"
	}

	// threaddump command
	if cmd == "threaddump" {
		if len(args) > 1 {
			return fmt.Sprintf("ATTACH_DIAGNOSTICS:Thread.print,%s", args[1])
		}
		return "ATTACH_DIAGNOSTICS:Thread.print,"
	}

	// dumpheap command
	if cmd == "dumpheap" {
		if len(args) > 1 {
			return fmt.Sprintf("ATTACH_DIAGNOSTICS:Dump.heap,%s", args[1])
		}
		return "ATTACH_DIAGNOSTICS:Dump.heap,"
	}

	// inspectheap command
	if cmd == "inspectheap" {
		if len(args) > 1 {
			return fmt.Sprintf("ATTACH_DIAGNOSTICS:GC.class_histogram,%s", args[1])
		}
		return "ATTACH_DIAGNOSTICS:GC.class_histogram,"
	}

	// datadump command
	if cmd == "datadump" {
		if len(args) > 1 {
			return fmt.Sprintf("ATTACH_DIAGNOSTICS:Dump.java,%s", args[1])
		}
		return "ATTACH_DIAGNOSTICS:Dump.java,"
	}

	// properties command
	if cmd == "properties" {
		return "ATTACH_GETSYSTEMPROPERTIES"
	}

	// agentProperties command
	if cmd == "agentProperties" {
		return "ATTACH_GETAGENTPROPERTIES"
	}

	// For unknown commands, return as-is
	return cmd
}

// Attach performs the attach operation for OpenJ9 JVM
func (o *openJ9) Attach(pid, nspid int, args []string, printOutput bool, tmpPath string) (string, error) {
	// Verify attachInfo exists
	attachInfoPath := fmt.Sprintf("%s/.com_ibm_tools_attach/%d/attachInfo", tmpPath, nspid)
	if _, err := os.Stat(attachInfoPath); err != nil {
		return "", fmt.Errorf("OpenJ9 attachInfo not found at %s: %v (JVM may not have attach enabled)", attachInfoPath, err)
	}

	// Step 1: Acquire attach lock
	attachLock, err := o.acquireLock(tmpPath, "", "_attachlock")
	if err != nil {
		return "", fmt.Errorf("could not acquire attach lock: %v", err)
	}
	defer o.releaseLock(attachLock)

	// Step 2: Create TCP listen socket
	listener, port, err := o.createAttachSocket()
	if err != nil {
		return "", fmt.Errorf("failed to create attach socket: %v", err)
	}
	defer listener.Close()

	// Step 3: Generate random key for connection verification
	key := o.randomKey()

	// Step 4: Write replyInfo file with port and key
	if err := o.writeReplyInfo(tmpPath, nspid, port, key); err != nil {
		return "", fmt.Errorf("could not write replyInfo: %v", err)
	}
	defer o.cleanupReplyInfo(tmpPath, nspid)

	// Step 5: Lock notification files and notify semaphore
	notifLocks, notifCount := o.lockNotificationFiles(tmpPath)
	defer o.unlockNotificationFiles(notifLocks)

	if err := o.notifySemaphore(tmpPath, 1, notifCount); err != nil {
		return "", fmt.Errorf("could not notify semaphore: %v", err)
	}
	defer o.notifySemaphore(tmpPath, -1, notifCount)

	// Step 6: Accept connection from JVM
	conn, err := o.acceptClient(listener, key)
	if err != nil {
		return "", fmt.Errorf("JVM did not respond: %v", err)
	}
	defer conn.Close()

	if printOutput {
		fmt.Println("Connected to remote JVM")
	}

	// Step 7: Translate and send command
	translatedCmd := o.translateCommand(args)
	if err := o.writeCommand(conn, translatedCmd); err != nil {
		return "", fmt.Errorf("error writing command: %v", err)
	}

	// Step 8: Read response
	output, exitCode, err := o.readResponse(conn, translatedCmd, printOutput)
	if err != nil {
		return output, err
	}

	// Step 9: Send detach command if successful
	if exitCode != 1 {
		o.detach(conn)
	}

	if exitCode != 0 {
		return output, fmt.Errorf("command execution failed with exit code %d", exitCode)
	}

	return output, nil
}

// acquireLock acquires a file lock for synchronization
func (o *openJ9) acquireLock(tmpPath, subdir, filename string) (int, error) {
	var path string
	if subdir == "" {
		path = fmt.Sprintf("%s/.com_ibm_tools_attach/%s", tmpPath, filename)
	} else {
		path = fmt.Sprintf("%s/.com_ibm_tools_attach/%s/%s", tmpPath, subdir, filename)
	}

	// Ensure parent directory exists
	dir := fmt.Sprintf("%s/.com_ibm_tools_attach", tmpPath)
	if subdir != "" {
		dir = fmt.Sprintf("%s/%s", dir, subdir)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return -1, fmt.Errorf("failed to create directory %s: %v", dir, err)
	}

	fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_CREAT, 0666)
	if err != nil {
		return -1, fmt.Errorf("failed to open %s: %v", path, err)
	}

	if err := syscall.Flock(fd, syscall.LOCK_EX); err != nil {
		syscall.Close(fd)
		return -1, fmt.Errorf("failed to lock %s: %v", path, err)
	}

	return fd, nil
}

// releaseLock releases a file lock
func (o *openJ9) releaseLock(fd int) {
	if fd >= 0 {
		syscall.Flock(fd, syscall.LOCK_UN)
		syscall.Close(fd)
	}
}

// createAttachSocket creates a TCP socket and returns listener and port
func (o *openJ9) createAttachSocket() (net.Listener, int, error) {
	// OpenJ9 expects localhost connections
	// Try IPv4 loopback first (most common)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		// Fall back to IPv6 loopback
		listener, err = net.Listen("tcp6", "[::1]:0")
		if err != nil {
			return nil, 0, err
		}
	}

	addr := listener.Addr().(*net.TCPAddr)
	return listener, addr.Port, nil
}

// randomKey generates a random 64-bit key for connection verification
func (o *openJ9) randomKey() uint64 {
	key := uint64(time.Now().UnixNano()) * 0xc6a4a7935bd1e995

	fd, err := syscall.Open("/dev/urandom", syscall.O_RDONLY, 0)
	if err == nil {
		var buf [8]byte
		syscall.Read(fd, buf[:])
		syscall.Close(fd)
		key = binary.LittleEndian.Uint64(buf[:])
	}

	return key
}

// writeReplyInfo writes the replyInfo file with port and key
func (o *openJ9) writeReplyInfo(tmpPath string, pid, port int, key uint64) error {
	path := fmt.Sprintf("%s/.com_ibm_tools_attach/%d/replyInfo", tmpPath, pid)

	fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer syscall.Close(fd)

	content := fmt.Sprintf("%016x\n%d\n", key, port)
	_, err = syscall.Write(fd, []byte(content))
	return err
}

// cleanupReplyInfo removes the replyInfo file
func (o *openJ9) cleanupReplyInfo(tmpPath string, pid int) {
	path := fmt.Sprintf("%s/.com_ibm_tools_attach/%d/replyInfo", tmpPath, pid)
	syscall.Unlink(path)
}

// lockNotificationFiles locks all notification files and returns the locks
func (o *openJ9) lockNotificationFiles(tmpPath string) ([]int, int) {
	var locks []int
	path := fmt.Sprintf("%s/.com_ibm_tools_attach", tmpPath)

	dir, err := os.Open(path)
	if err != nil {
		return locks, 0
	}
	defer dir.Close()

	entries, err := dir.Readdirnames(-1)
	if err != nil {
		return locks, 0
	}

	for _, entry := range entries {
		if len(entry) > 0 && entry[0] >= '1' && entry[0] <= '9' {
			fd, err := o.acquireLock(tmpPath, entry, "attachNotificationSync")
			if err == nil {
				locks = append(locks, fd)
			}
		}
	}

	return locks, len(locks)
}

// unlockNotificationFiles releases all notification file locks
func (o *openJ9) unlockNotificationFiles(locks []int) {
	for _, fd := range locks {
		o.releaseLock(fd)
	}
}

// notifySemaphore notifies the JVM via semaphore
func (o *openJ9) notifySemaphore(tmpPath string, value, count int) error {
	if count == 0 {
		return nil
	}

	path := fmt.Sprintf("%s/.com_ibm_tools_attach/_notifier", tmpPath)

	// Use ftok to generate semaphore key
	// ftok formula: (st.st_ino & 0xFFFF) | ((proj_id & 0xFF) << 16) | ((st.st_dev & 0xFF) << 24)
	var stat syscall.Stat_t
	if err := syscall.Stat(path, &stat); err != nil {
		return err
	}

	// Standard ftok implementation
	// OpenJ9 uses a different byte order: (proj_id << 24) | (dev << 16) | (inode & 0xFFFF)
	projId := 0xa1
	key := int((uint64(projId&0xFF) << 24) | ((stat.Dev & 0xFF) << 16) | (stat.Ino & 0xFFFF))

	// Get or create semaphore using syscall directly
	semid, _, errno := syscall.Syscall(syscall.SYS_SEMGET, uintptr(key), 1, unix.IPC_CREAT|0666)
	if errno != 0 {
		return fmt.Errorf("semget failed: %v", errno)
	}

	// Perform semaphore operation
	flags := 0
	if value < 0 {
		flags = unix.IPC_NOWAIT
	}

	for i := 0; i < count; i++ {
		// sembuf structure: {sem_num, sem_op, sem_flg}
		ops := [3]int16{0, int16(value), int16(flags)}
		_, _, errno := syscall.Syscall(syscall.SYS_SEMOP, semid, uintptr(unsafe.Pointer(&ops[0])), 1)
		if errno != 0 && value >= 0 {
			// Only return error for increment operations
			return fmt.Errorf("semop failed: %v", errno)
		}
	}

	return nil
}

// acceptClient accepts connection from JVM and verifies the key
func (o *openJ9) acceptClient(listener net.Listener, key uint64) (net.Conn, error) {
	// Set shorter timeout for initial debugging (5 seconds like jattach)
	if tcpListener, ok := listener.(*net.TCPListener); ok {
		tcpListener.SetDeadline(time.Now().Add(5 * time.Second))
	}

	conn, err := listener.Accept()
	if err != nil {
		return nil, fmt.Errorf("JVM did not respond: %v", err)
	}

	// Read and verify connection key
	var buf [35]byte
	off := 0
	for off < len(buf) {
		n, err := conn.Read(buf[off:])
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("JVM connection prematurely closed: %v", err)
		}
		off += n
	}

	// Verify connection key (note: response ends with space+null, not just space)
	expected := fmt.Sprintf("ATTACH_CONNECTED %016x ", key)
	response := string(buf[:])
	// Remove trailing null byte if present
	if len(response) > 0 && response[len(response)-1] == '\x00' {
		response = response[:len(response)-1]
	}
	if response != expected {
		conn.Close()
		return nil, fmt.Errorf("unexpected JVM response: got %q, expected %q", response, expected)
	}

	// Reset timeout for command execution
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		tcpConn.SetDeadline(time.Time{})
	}

	return conn, nil
}

// writeCommand sends a command to the JVM
func (o *openJ9) writeCommand(conn net.Conn, cmd string) error {
	cmdBytes := []byte(cmd + "\x00")
	off := 0
	for off < len(cmdBytes) {
		n, err := conn.Write(cmdBytes[off:])
		if err != nil {
			return err
		}
		off += n
	}
	return nil
}

// detach sends the detach command to JVM
func (o *openJ9) detach(conn net.Conn) {
	o.writeCommand(conn, "ATTACH_DETACHED")

	// Read response until null terminator
	buf := make([]byte, 256)
	for {
		n, err := conn.Read(buf)
		if err != nil || n == 0 {
			break
		}
		if buf[n-1] == 0 {
			break
		}
	}
}

// readResponse reads and processes OpenJ9-specific response format
func (o *openJ9) readResponse(conn net.Conn, cmd string, printOutput bool) (string, int, error) {
	// Read response until null terminator
	buf := make([]byte, 0, 8192)
	tmp := make([]byte, 1024)

	for {
		n, err := conn.Read(tmp)
		if n == 0 {
			return "", 1, errors.New("unexpected EOF reading response")
		}
		if err != nil {
			return "", 1, fmt.Errorf("error reading response: %v", err)
		}

		buf = append(buf, tmp[:n]...)

		// Check for null terminator
		if buf[len(buf)-1] == 0 {
			buf = buf[:len(buf)-1] // Remove null terminator
			break
		}

		// Prevent excessive memory usage
		if len(buf) > 10*1024*1024 {
			return "", 1, errors.New("response too large")
		}
	}

	response := string(buf)
	resultCode := 0

	// Handle ATTACH_LOADAGENT response
	if strings.HasPrefix(cmd, "ATTACH_LOADAGENT") {
		if !strings.HasPrefix(response, "ATTACH_ACK") {
			// Check for AgentInitializationException
			if strings.HasPrefix(response, "ATTACH_ERR AgentInitializationException") {
				// Extract error code after the exception message
				parts := strings.Fields(response)
				if len(parts) > 2 {
					if code, err := strconv.Atoi(parts[2]); err == nil {
						resultCode = code
					} else {
						resultCode = -1
					}
				} else {
					resultCode = -1
				}
			} else {
				resultCode = -1
			}
		}
	}

	// Handle ATTACH_DIAGNOSTICS response
	if strings.HasPrefix(cmd, "ATTACH_DIAGNOSTICS:") && printOutput {
		// Look for diagnostic result in Java Properties format
		if idx := strings.Index(response, "openj9_diagnostics.string_result="); idx != -1 {
			// Extract and unescape the diagnostic result
			result := response[idx+33:]
			unescaped := o.unescapeString(result)
			fmt.Println(unescaped)
			return unescaped, resultCode, nil
		}
	}

	// Print output if requested
	if printOutput {
		fmt.Println(response)
	}

	if resultCode != 0 {
		return response, resultCode, fmt.Errorf("command failed with code %d", resultCode)
	}

	return response, resultCode, nil
}

// unescapeString unescapes Java Properties format strings
func (o *openJ9) unescapeString(s string) string {
	// Remove trailing newline if present
	if idx := strings.Index(s, "\n"); idx != -1 {
		s = s[:idx]
	}

	var result strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 'f':
				result.WriteByte('\f')
				i++
			case 'n':
				result.WriteByte('\n')
				i++
			case 'r':
				result.WriteByte('\r')
				i++
			case 't':
				result.WriteByte('\t')
				i++
			default:
				if i+1 < len(s) {
					result.WriteByte(s[i+1])
					i++
				}
			}
		} else {
			result.WriteByte(s[i])
		}
	}

	return result.String()
}

// Detect checks if the process is an OpenJ9 JVM
// OpenJ9 creates .com_ibm_tools_attach/{pid}/attachInfo file
// This follows the official jattach implementation
func (o *openJ9) Detect(nspid int) bool {
	// Try different potential tmp paths
	paths := []string{
		fmt.Sprintf("/proc/%d/root/tmp", nspid),
		"/tmp",
	}

	// Check JAMBO_ATTACH_PATH environment variable
	if envPath := os.Getenv("JAMBO_ATTACH_PATH"); envPath != "" {
		paths = append([]string{envPath}, paths...)
	}

	for _, tmpPath := range paths {
		attachInfoPath := fmt.Sprintf("%s/.com_ibm_tools_attach/%d/attachInfo", tmpPath, nspid)
		if _, err := os.Stat(attachInfoPath); err == nil {
			return true
		}
	}

	return false
}

type socketConn struct {
	fd int
}

// connectToSocket connects to a Unix domain socket (shared by HotSpot and OpenJ9)
func connectToSocket(path string) (*socketConn, error) {
	fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, err
	}

	addr := &syscall.SockaddrUnix{Name: path}
	if err := syscall.Connect(fd, addr); err != nil {
		syscall.Close(fd)
		return nil, err
	}

	return &socketConn{fd: fd}, nil
}

func (c *socketConn) Close() error {
	return syscall.Close(c.fd)
}

func (h *hotSpot) sendCommand(conn *socketConn, args []string) error {
	var buf bytes.Buffer

	// Protocol version
	buf.WriteString("1")
	buf.WriteByte(0)

	// Determine number of arguments to send
	cmdArgs := len(args)
	if cmdArgs >= 2 && args[0] == "jcmd" {
		cmdArgs = 2
	} else if cmdArgs >= 4 {
		cmdArgs = 4
	}

	// Write arguments
	for i := 0; i < len(args) && buf.Len() < 8192; i++ {
		if i >= cmdArgs {
			// Merge excessive arguments with spaces
			buf.Bytes()[buf.Len()-1] = ' '
		}
		buf.WriteString(args[i])
		buf.WriteByte(0)
	}

	// Pad to 4 arguments if needed
	for i := cmdArgs; i < 4 && buf.Len() < 8192; i++ {
		buf.WriteByte(0)
	}

	data := buf.Bytes()
	_, err := syscall.Write(conn.fd, data)
	return err
}

func (h *hotSpot) readResponse(conn *socketConn, args []string) (string, error) {
	buf := make([]byte, 8192)
	bytesRead, err := syscall.Read(conn.fd, buf)
	if bytesRead == 0 {
		return "", errors.New("unexpected EOF reading response")
	}
	if err != nil {
		return "", err
	}

	// First line is result code
	buf = buf[:bytesRead]
	lines := strings.SplitN(string(buf), "\n", 2)
	if len(lines) < 1 {
		return "", errors.New("invalid response format")
	}

	// Parse result code
	resultCode := 0
	if len(lines[0]) > 0 {
		if code, err := strconv.Atoi(lines[0]); err == nil {
			resultCode = code
		}
	}

	// Special treatment of 'load' command
	if len(args) > 0 && args[0] == "load" {
		// Read the entire output of the 'load' command
		total := bytesRead
		for total < len(buf)-1 {
			n, err := syscall.Read(conn.fd, buf[total:])
			if err != nil || n <= 0 {
				break
			}
			total += n
		}
		bytesRead = total
		buf = buf[:bytesRead]

		// Parse the return code of Agent_OnAttach
		if resultCode == 0 && bytesRead >= 2 {
			output := string(buf)
			if strings.Contains(output, "return code: ") {
				// JDK 9+: Agent_OnAttach result comes on the second line after "return code: "
				parts := strings.SplitN(output[2:], "return code: ", 2)
				if len(parts) > 1 {
					if code, err := strconv.Atoi(strings.TrimSpace(strings.Split(parts[1], "\n")[0])); err == nil {
						resultCode = code
					}
				}
			} else if len(output) > 2 && ((output[2] >= '0' && output[2] <= '9') || output[2] == '-') {
				// JDK 8: Agent_OnAttach result comes on the second line alone
				secondLine := strings.Split(output[2:], "\n")[0]
				if code, err := strconv.Atoi(strings.TrimSpace(secondLine)); err == nil {
					resultCode = code
				}
			} else {
				// JDK 21+: load command always returns 0; the rest of output is an error message
				resultCode = -1
			}
		}
	}

	if len(lines) > 1 {
		output := lines[1]
		if resultCode != 0 {
			return output, fmt.Errorf("command failed with code %d", resultCode)
		}
		return output, nil
	}

	if resultCode != 0 {
		return "", fmt.Errorf("command failed with code %d", resultCode)
	}

	return "", nil
}
