# OpenJ9 JVM Dynamic Attach Mechanism

This document describes how the Eclipse OpenJ9 (formerly IBM J9) JVM Dynamic Attach mechanism works and how jambo implements it.

## Table of Contents

- [Overview](#overview)
- [Differences from HotSpot](#differences-from-hotspot)
- [Attach Protocol](#attach-protocol)
- [Command Translation](#command-translation)
- [Implementation Details](#implementation-details)
- [References](#references)

## Overview

OpenJ9 uses a different attach mechanism than HotSpot. While HotSpot uses Unix domain sockets, OpenJ9 uses a more complex system involving:
- Shared memory
- Semaphores  
- TCP/IP sockets (IPv4/IPv6)
- File-based notification system

### Key Differences from HotSpot

| Feature | HotSpot | OpenJ9 |
|---------|---------|--------|
| **Transport** | Unix domain sockets | TCP/IP sockets |
| **Detection** | Socket file existence | `.com_ibm_tools_attach/{pid}/attachInfo` file |
| **Command Format** | Direct commands | ATTACH_* prefixed commands |
| **Response Format** | Plain text | Java Properties format (escaped) |
| **Triggering** | SIGQUIT + attach file | Semaphore notification |

## Attach Protocol

### Architecture

```
┌─────────────┐                    ┌─────────────┐
│   jambo     │                    │   OpenJ9    │
│  (Client)   │                    │    JVM      │
└──────┬──────┘                    └──────┬──────┘
       │                                  │
       │  1. Acquire attach lock          │
       │     _attachlock file             │
       │                                  │
       │  2. Create TCP listen socket     │
       │     (random port)                │
       │                                  │
       │  3. Write replyInfo file         │
       │     - Random key                 │
       │     - Port number                │
       ├─────────────────────────────────>│
       │                                  │
       │  4. Lock notification files      │
       │     attachNotificationSync       │
       │                                  │
       │  5. Increment semaphore          │
       │     _notifier semaphore          │
       ├─────────────────────────────────>│
       │                                  │
       │        6. JVM wakes up and       │
       │           reads replyInfo        │
       │                                  │
       │  7. JVM connects to TCP socket   │
       │<─────────────────────────────────┤
       │                                  │
       │  8. Verify connection key        │
       │     ATTACH_CONNECTED <key>       │
       │<─────────────────────────────────┤
       │                                  │
       │  9. Send translated command      │
       │     ATTACH_DIAGNOSTICS:...       │
       ├─────────────────────────────────>│
       │                                  │
       │        10. Execute command       │
       │            and send response     │
       │<─────────────────────────────────┤
       │                                  │
       │  11. Send ATTACH_DETACHED        │
       ├─────────────────────────────────>│
       │                                  │
```

### File System Structure

OpenJ9 uses a dedicated directory structure:

```
/tmp/.com_ibm_tools_attach/
├── _attachlock              # Global attach lock
├── _notifier                # Semaphore for notifications
├── <pid1>/
│   ├── attachInfo           # Indicates JVM supports attach
│   ├── replyInfo            # Connection info (key + port)
│   └── attachNotificationSync  # Per-JVM notification lock
├── <pid2>/
│   ├── attachInfo
│   ├── replyInfo
│   └── attachNotificationSync
└── ...
```

### Detection

OpenJ9 JVM is detected by checking for the `attachInfo` file:

```go
func (o *openJ9) Detect(nspid int) bool {
    tmpPath, _ := getTempPath(nspid)
    attachInfoPath := fmt.Sprintf("%s/.com_ibm_tools_attach/%d/attachInfo", tmpPath, nspid)
    
    _, err := os.Stat(attachInfoPath)
    return err == nil
}
```

### Connection Process

#### 1. Acquire Global Lock

```go
lockFile := "/tmp/.com_ibm_tools_attach/_attachlock"
lockFd := open(lockFile, O_WRONLY|O_CREAT, 0666)
flock(lockFd, LOCK_EX)  // Exclusive lock
```

#### 2. Create Listen Socket

Create a TCP socket on a random port (IPv6 preferred, falls back to IPv4):

```go
// Try IPv6 first
socket(AF_INET6, SOCK_STREAM, 0)
bind(sockfd, {.sin6_family=AF_INET6, .sin6_port=0}, ...)  // Port 0 = random
listen(sockfd, 0)
getsockname(sockfd, ...)  // Get assigned port
```

#### 3. Write replyInfo File

```
File: /tmp/.com_ibm_tools_attach/<pid>/replyInfo
Content:
<16-digit-hex-key>
<port-number>
```

Example:
```
1a2b3c4d5e6f7890
54321
```

The key is a random 64-bit value used to authenticate the connection.

#### 4. Lock Notification Files

Lock all JVM's `attachNotificationSync` files to prevent race conditions:

```go
for each JVM directory in .com_ibm_tools_attach/ {
    lockFile := "<jvm_dir>/attachNotificationSync"
    lockFd := open(lockFile, O_WRONLY|O_CREAT, 0666)
    flock(lockFd, LOCK_EX)
    // Keep locked until after semaphore notification
}
```

#### 5. Notify via Semaphore

Increment the semaphore to wake up JVM attach listener:

```go
semKey := ftok("/tmp/.com_ibm_tools_attach/_notifier", 0xa1)
sem := semget(semKey, 1, IPC_CREAT|0666)
semop(sem, {.sem_op=1}, 1)  // Increment
```

#### 6. Accept Connection

Wait for JVM to connect (with 5-second timeout):

```go
setsockopt(sockfd, SOL_SOCKET, SO_RCVTIMEO, {tv_sec=5}, ...)
clientFd := accept(sockfd, ...)
```

#### 7. Verify Connection

Read and verify the connection header:

```
Expected: "ATTACH_CONNECTED <16-digit-hex-key> "
Example:  "ATTACH_CONNECTED 1a2b3c4d5e6f7890 "
```

If the key doesn't match, reject the connection.

## Command Translation

OpenJ9 uses different command names than HotSpot. Jambo automatically translates standard commands:

### Translation Table

| HotSpot Command | OpenJ9 Command | Description |
|-----------------|----------------|-------------|
| `load <path> false <opts>` | `ATTACH_LOADAGENT(<path>,<opts>)` | Load Java agent (relative path) |
| `load <path> true <opts>` | `ATTACH_LOADAGENTPATH(<path>,<opts>)` | Load Java agent (absolute path) |
| `jcmd <cmd> <args>` | `ATTACH_DIAGNOSTICS:<cmd>,<args>` | Execute diagnostic command |
| `threaddump` | `ATTACH_DIAGNOSTICS:Thread.print` | Get thread dump |
| `dumpheap <file>` | `ATTACH_DIAGNOSTICS:Dump.heap,<file>` | Heap dump |
| `inspectheap` | `ATTACH_DIAGNOSTICS:GC.class_histogram` | Heap histogram |
| `datadump` | `ATTACH_DIAGNOSTICS:Dump.java` | Java core dump |
| `properties` | `ATTACH_GETSYSTEMPROPERTIES` | Get system properties |
| `agentProperties` | `ATTACH_GETAGENTPROPERTIES` | Get agent properties |

### Translation Implementation

```go
func (o *openJ9) translateCommand(args []string) string {
    cmd := args[0]
    
    switch cmd {
    case "load":
        agentPath := args[1]
        options := ""
        if len(args) > 3 {
            options = args[3]
        }
        
        // Check if absolute path
        if len(args) > 2 && args[2] == "true" {
            return fmt.Sprintf("ATTACH_LOADAGENTPATH(%s,%s)", agentPath, options)
        }
        return fmt.Sprintf("ATTACH_LOADAGENT(%s,%s)", agentPath, options)
        
    case "jcmd":
        if len(args) > 1 {
            return "ATTACH_DIAGNOSTICS:" + strings.Join(args[1:], ",")
        }
        return "ATTACH_DIAGNOSTICS:help"
        
    case "threaddump":
        opts := ""
        if len(args) > 1 {
            opts = args[1]
        }
        return fmt.Sprintf("ATTACH_DIAGNOSTICS:Thread.print,%s", opts)
        
    // ... more cases
    }
    
    return cmd  // Unknown command, pass through
}
```

### Command Format

OpenJ9 commands are sent as a single null-terminated string:

```
"ATTACH_DIAGNOSTICS:VM.version\0"
```

No protocol version or multiple arguments like HotSpot.

## Response Format

### Response Structure

OpenJ9 responses are null-terminated and may be encoded in Java Properties format:

```
<response-text>\0
```

### Response Types

#### 1. ACK Response (for load command)

```
ATTACH_ACK\0
```

Success: Agent loaded successfully.

#### 2. Error Response (for load command)

```
ATTACH_ERR AgentInitializationException <code>\0
```

Example:
```
ATTACH_ERR AgentInitializationException 1\0
```

#### 3. Diagnostic Result

```
openj9_diagnostics.string_result=<escaped-result>\0
```

Example:
```
openj9_diagnostics.string_result=Thread Dump:\n\tThread-1 ...\0
```

### String Unescaping

OpenJ9 uses Java Properties format escaping for diagnostic output:

| Escape Sequence | Character |
|----------------|-----------|
| `\\` | `\` |
| `\n` | Newline |
| `\t` | Tab |
| `\r` | Carriage return |
| `\f` | Form feed |
| `\"` | Quote |

Implementation:

```go
func (o *openJ9) unescapeString(s string) string {
    // Remove trailing newline
    if idx := strings.Index(s, "\n"); idx != -1 {
        s = s[:idx]
    }
    
    var result strings.Builder
    for i := 0; i < len(s); i++ {
        if s[i] == '\\' && i+1 < len(s) {
            switch s[i+1] {
            case 'n':
                result.WriteByte('\n')
                i++
            case 't':
                result.WriteByte('\t')
                i++
            case 'r':
                result.WriteByte('\r')
                i++
            case 'f':
                result.WriteByte('\f')
                i++
            default:
                result.WriteByte(s[i+1])
                i++
            }
        } else {
            result.WriteByte(s[i])
        }
    }
    
    return result.String()
}
```

## Implementation Details

### Reading Response

Unlike HotSpot (which uses line-based protocol), OpenJ9 requires reading until null terminator:

```go
func (o *openJ9) readResponse(conn *socketConn, cmd string, printOutput bool) (string, error) {
    buf := make([]byte, 0, 8192)
    tmp := make([]byte, 1024)
    
    for {
        n, err := syscall.Read(conn.fd, tmp)
        if err != nil {
            return "", err
        }
        
        buf = append(buf, tmp[:n]...)
        
        // Check for null terminator
        if buf[len(buf)-1] == 0 {
            buf = buf[:len(buf)-1]  // Remove null
            break
        }
        
        if len(buf) > 10*1024*1024 {  // 10MB limit
            return "", errors.New("response too large")
        }
    }
    
    response := string(buf)
    
    // Parse response based on command type
    // ...
}
```

### Detaching

After command execution, send detach notification:

```go
syscall.Write(conn.fd, []byte("ATTACH_DETACHED\0"))

// Read acknowledgment
for {
    n, _ := syscall.Read(conn.fd, buf)
    if n > 0 && buf[n-1] == 0 {
        break
    }
}
```

### Error Handling

OpenJ9-specific error codes:

| Error | Meaning |
|-------|---------|
| `ATTACH_ACK` | Success (load command) |
| `ATTACH_ERR AgentInitializationException N` | Agent initialization failed with code N |
| Empty response | Command not supported |

## Limitations

1. **Windows Support**: OpenJ9 attach on Windows is not implemented in jambo
2. **Complexity**: OpenJ9's attach mechanism is more complex than HotSpot's
3. **File Dependencies**: Requires proper file permissions in `/tmp/.com_ibm_tools_attach/`

## References

### Official Documentation

- [Eclipse OpenJ9 Documentation](https://www.eclipse.org/openj9/docs/)
- [OpenJ9 Diagnostic Tools](https://www.eclipse.org/openj9/docs/tool_jcmd/)

### Source Code

- [OpenJ9 Attach API](https://github.com/eclipse-openj9/openj9)
- [jattach OpenJ9 Implementation](https://github.com/jattach/jattach/blob/master/src/posix/jattach_openj9.c)

### Related

- [Java Attach API Specification](https://docs.oracle.com/javase/8/docs/jdk/api/attach/spec/)
- [IBM J9 to OpenJ9 Migration](https://www.eclipse.org/openj9/docs/introduction/)
