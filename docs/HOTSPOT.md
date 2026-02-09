# HotSpot JVM Dynamic Attach Mechanism

This document describes how the HotSpot JVM Dynamic Attach mechanism works and how jambo implements it.

## Table of Contents

- [Overview](#overview)
- [Attach Protocol on Linux](#attach-protocol-on-linux)
- [Attach Protocol on Windows](#attach-protocol-on-windows)
- [Implementation Details](#implementation-details)
- [References](#references)

## Overview

The HotSpot JVM provides a Dynamic Attach mechanism that allows external processes to connect to a running JVM and execute various diagnostic commands. This is the foundation for tools like `jstack`, `jmap`, `jcmd`, etc.

### Key Features

- **Load Java Agents**: Dynamically load Java agents into running JVMs
- **Execute Diagnostic Commands**: Thread dumps, heap dumps, VM info, etc.
- **No JVM Restart Required**: Attach without interrupting the running application
- **Cross-Platform**: Works on Linux, Windows, macOS, etc.

## Attach Protocol on Linux

### Architecture

On Linux, HotSpot uses Unix Domain Sockets for inter-process communication:

```
┌─────────────┐                    ┌─────────────┐
│   jambo     │                    │  HotSpot    │
│  (Client)   │                    │    JVM      │
└──────┬──────┘                    └──────┬──────┘
       │                                  │
       │  1. Create attach file           │
       │  /proc/<pid>/cwd/.attach_pid<pid>│
       ├─────────────────────────────────>│
       │                                  │
       │  2. Send SIGQUIT signal          │
       ├─────────────────────────────────>│
       │                                  │
       │                3. JVM creates    │
       │                Unix socket       │
       │           /tmp/.java_pid<nspid>  │
       │                                  │
       │  4. Connect to socket            │
       ├─────────────────────────────────>│
       │                                  │
       │  5. Send protocol version + cmd  │
       ├─────────────────────────────────>│
       │                                  │
       │          6. Execute command      │
       │             and send response    │
       │<─────────────────────────────────┤
       │                                  │
       │  7. Close connection             │
       ├─────────────────────────────────>│
       │                                  │
```

### Step-by-Step Process

#### 1. Check for Existing Socket

First, check if the attach socket already exists:

```bash
/tmp/.java_pid<nspid>
```

If it exists, skip to step 4 (connect to socket).

#### 2. Create Attach File

Create an attach trigger file in the target process's working directory:

```bash
/proc/<pid>/cwd/.attach_pid<pid>
```

The file must be owned by the same user as the target JVM process.

#### 3. Send SIGQUIT Signal

Send SIGQUIT (signal 3) to the target JVM process:

```c
kill(pid, SIGQUIT);
```

This triggers the JVM's attach listener thread, which:
- Detects the attach file
- Creates the Unix domain socket at `/tmp/.java_pid<nspid>`
- Starts listening for connections

#### 4. Wait for Socket Creation

Poll for the socket file with exponential backoff:

```
Initial wait: 20ms
Retry intervals: 20ms, 40ms, 60ms, ... (up to 400ms)
Total timeout: ~5 seconds
```

#### 5. Connect to Socket

Once the socket exists, connect using Unix domain socket:

```c
int fd = socket(AF_UNIX, SOCK_STREAM, 0);
struct sockaddr_un addr = {
    .sun_family = AF_UNIX,
    .sun_path = "/tmp/.java_pid<nspid>"
};
connect(fd, (struct sockaddr*)&addr, sizeof(addr));
```

#### 6. Send Command

Send the command using the attach protocol format:

```
Protocol Version (1 byte): "1\0"
Command (null-terminated): "threaddump\0"
Arg1 (null-terminated): "\0"
Arg2 (null-terminated): "\0"
Arg3 (null-terminated): "\0"
```

**Protocol Details**:
- Protocol version: Always "1"
- Maximum 4 arguments
- Each field is null-terminated
- For `jcmd`: Only command + 1 argument
- For other commands: Up to 4 arguments
- Excessive arguments are merged with spaces

**Example - Thread Dump**:
```
"1\0threaddump\0\0\0\0"
```

**Example - Load Agent**:
```
"1\0load\0/path/agent.jar\0false\0options=value\0"
```

**Example - jcmd**:
```
"1\0jcmd\0VM.version\0\0\0"
```

#### 7. Read Response

Read the response from the socket:

```
Response Format:
Line 1: Result code (integer as string)
Line 2+: Command output
```

**Result Codes**:
- `0`: Success
- `Non-zero`: Error occurred

**Special Handling for `load` Command**:

Different JDK versions have different response formats:

- **JDK 8**: Second line contains agent return code
  ```
  0\n
  <return_code>\n
  ```

- **JDK 9+**: Contains "return code: " prefix
  ```
  0\n
  return code: <code>\n
  <output>
  ```

- **JDK 21+**: Agent errors appear as text
  ```
  0\n
  <error_message>
  ```

### Protocol Version History

| Version | JDK Version | Changes |
|---------|-------------|---------|
| 1 | JDK 6+ | Initial protocol |
| 1 | JDK 9+ | Enhanced load command response |
| 1 | JDK 21+ | Load error reporting changes |

## Attach Protocol on Windows

### Architecture

Windows uses a different approach based on remote thread injection:

```
┌─────────────┐                    ┌─────────────┐
│   jambo     │                    │  HotSpot    │
│  (Client)   │                    │    JVM      │
└──────┬──────┘                    └──────┬──────┘
       │                                  │
       │  1. Create Named Pipe            │
       │     \\.\pipe\javatool<tid>       │
       │                                  │
       │  2. Open target process          │
       │     (requires privileges)        │
       ├─────────────────────────────────>│
       │                                  │
       │  3. Allocate shellcode in        │
       │     remote process memory        │
       ├─────────────────────────────────>│
       │                                  │
       │  4. Create remote thread         │
       │     to execute shellcode         │
       ├─────────────────────────────────>│
       │                                  │
       │        5. Shellcode executes:    │
       │           - GetModuleHandle(jvm) │
       │           - GetProcAddress(...)  │
       │           - JVM_EnqueueOperation │
       │                                  │
       │  6. JVM writes to Named Pipe     │
       │<─────────────────────────────────┤
       │                                  │
       │  7. Read response from pipe      │
       │<─────────────────────────────────┤
       │                                  │
```

### Process Details

1. **Create Named Pipe**: `\\.\pipe\javatool<tickcount>`
2. **Open Target Process**: Requires `PROCESS_ALL_ACCESS` or `SeDebugPrivilege`
3. **Inject Code**: Allocate executable memory and write shellcode
4. **Create Remote Thread**: Execute shellcode in target process
5. **Shellcode Execution**: Calls `JVM_EnqueueOperation` in jvm.dll
6. **Read Response**: JVM writes result to Named Pipe

For detailed shellcode implementation, see [Windows Shellcode Documentation](WINDOWS_SHELLCODE.md).

## Implementation Details

### Container Support (Linux)

jambo automatically handles containerized JVMs:

**Namespace Detection**:
```go
// Read NSpid from /proc/<pid>/status
// If NSpid differs from Pid, process is in container
```

**Namespace Switching**:
```go
// Enter target namespaces
setns(net_ns_fd, CLONE_NEWNET)
setns(ipc_ns_fd, CLONE_NEWIPC)
setns(mnt_ns_fd, CLONE_NEWNS)
```

**Socket Path**:
```go
// Use namespace PID for socket path
socketPath = fmt.Sprintf("/tmp/.java_pid%d", nsPid)
```

### Credential Switching

When attaching to processes owned by different users:

```go
// Switch to target process credentials
setuid(targetUID)
setgid(targetGID)
```

Requires:
- Running as root, OR
- Having `CAP_SETUID` and `CAP_SETGID` capabilities

### Error Handling

Common errors and solutions:

| Error | Cause | Solution |
|-------|-------|----------|
| `Permission denied` | Insufficient privileges | Run with sudo or appropriate capabilities |
| `Process not found` | Invalid PID or process exited | Verify PID is correct |
| `Socket timeout` | JVM attach listener not responding | Check JVM has attach listener enabled |
| `Connection refused` | Socket exists but not accepting | Delete stale socket file |

### SIGPIPE Handling

```go
// Ignore SIGPIPE to prevent process termination
signal.Ignore(syscall.SIGPIPE)
```

Without this, writing to a closed socket would terminate jambo with SIGPIPE.

## References

### Official Documentation

- [JVM Tool Interface (JVMTI)](https://docs.oracle.com/javase/8/docs/platform/jvmti/jvmti.html)
- [Java Attach API](https://docs.oracle.com/javase/8/docs/jdk/api/attach/spec/com/sun/tools/attach/VirtualMachine.html)

### Source Code References

- [OpenJDK HotSpot Attach Listener](https://github.com/openjdk/jdk/blob/master/src/hotspot/os/linux/attachListener_linux.cpp)
- [jattach - Original C Implementation](https://github.com/jattach/jattach)

### Related Tools

- `jcmd` - JVM Command-line diagnostic tool
- `jstack` - Thread dump tool
- `jmap` - Memory map tool
- `jstat` - JVM statistics monitoring tool

### Protocol Versions

The attach protocol version "1" has remained stable across JDK versions, with only minor changes to response formats for specific commands.
