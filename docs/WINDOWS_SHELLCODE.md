# Windows Shellcode for JVM Attach

This document explains how Windows shellcode is generated and used for JVM dynamic attach in jambo.

## Table of Contents

- [Overview](#overview)
- [Remote Thread Injection](#remote-thread-injection)
- [Shellcode Architecture](#shellcode-architecture)
- [Shellcode Generation](#shellcode-generation)
- [Platform Considerations](#platform-considerations)
- [Security Implications](#security-implications)

## Overview

On Windows, HotSpot JVM doesn't provide a socket-based attach mechanism like on Linux. Instead, jambo uses **remote thread injection** to execute code inside the target JVM process.

### Why Shellcode?

Go doesn't support creating position-independent code that can be injected into another process. Therefore, we need hand-written assembly (shellcode) that:

1. Can execute in any memory location (position-independent)
2. Calls Windows API functions
3. Invokes `JVM_EnqueueOperation` in jvm.dll
4. Returns the result code

### Attack Flow

```
┌──────────────┐
│   jambo.exe  │
│  (Go code)   │
└──────┬───────┘
       │
       │ 1. Create Named Pipe
       │    \\.\pipe\javatool<tid>
       │
       │ 2. Open target process
       │    OpenProcess(PROCESS_ALL_ACCESS, pid)
       │
       │ 3. Allocate executable memory
       │    VirtualAllocEx(..., PAGE_EXECUTE_READWRITE)
       │
       │ 4. Write shellcode
       │    WriteProcessMemory(shellcode_bytes)
       │
       │ 5. Allocate data structure
       │    VirtualAllocEx(sizeof(callData))
       │
       │ 6. Write callData
       │    WriteProcessMemory(callData)
       │
       │ 7. Create remote thread
       │    CreateRemoteThread(shellcode_addr, callData_addr)
       │
       ▼
┌──────────────────────────┐
│   Target JVM Process     │
│                          │
│  ┌────────────────────┐  │
│  │  Shellcode Thread  │  │
│  │                    │  │
│  │  1. Get jvm.dll    │  │
│  │  2. Find function  │  │
│  │  3. Call JVM_*     │  │
│  │  4. JVM → Pipe     │  │
│  └────────────────────┘  │
└──────────────────────────┘
       │
       │ Result written to Named Pipe
       │
       ▼
┌──────────────┐
│   jambo.exe  │
│  Reads result│
└──────────────┘
```

## Remote Thread Injection

### Process Memory Layout

```
Target JVM Process Memory:

0x00000000 ┌──────────────────┐
           │   Null page      │
           ├──────────────────┤
           │   .text          │ ← jvm.dll code
           │   .data          │
           ├──────────────────┤
           │   Heap           │
           ├──────────────────┤
           │   ...            │
           ├──────────────────┤
0x???????? │ ← shellcode      │ ← Injected by jambo
           │   (RWX)          │
           ├──────────────────┤
0x???????? │ ← callData       │ ← Injected by jambo
           │   (RW)           │
           ├──────────────────┤
           │   Stack          │
0xFFFFFFFF └──────────────────┘
```

### callData Structure

The data structure passed to shellcode:

```c
struct callData {
    // Function pointers (resolved in target process)
    FARPROC GetModuleHandleA;  // +0x00 (8 bytes on x64, 4 on x86)
    FARPROC GetProcAddress;    // +0x08 (x64) / +0x04 (x86)
    
    // Strings
    char strJvm[32];           // +0x10 : "jvm\0"
    char strEnqueue[32];       // +0x30 : "_JVM_EnqueueOperation\0"
    char pipeName[260];        // +0x50 : "\\.\pipe\javatool12345\0"
    
    // Command arguments
    char args[4][1024];        // +0x150 : 4KB total
                               // args[0] at +0x150
                               // args[1] at +0x550
                               // args[2] at +0x950
                               // args[3] at +0xD50
};

// Total size: ~4.5 KB
```

### Memory Allocation

```go
// 1. Allocate executable memory for shellcode
remoteCode := VirtualAllocEx(
    hProcess,
    NULL,
    len(shellcode),
    MEM_COMMIT,
    PAGE_EXECUTE_READWRITE  // RWX permissions
)

// 2. Write shellcode
WriteProcessMemory(hProcess, remoteCode, shellcode, len(shellcode))

// 3. Allocate memory for callData
remoteData := VirtualAllocEx(
    hProcess,
    NULL,
    sizeof(callData),
    MEM_COMMIT,
    PAGE_READWRITE  // RW permissions
)

// 4. Write callData
WriteProcessMemory(hProcess, remoteData, &data, sizeof(data))
```

## Shellcode Architecture

### x64 Assembly (64-bit)

```nasm
; Function: remote_thread_entry
; Parameter: RCX = pointer to callData
; Returns: RAX = result code (0 = success, 1001/1002 = error)

; === Function Prologue ===
push rbp
mov rbp, rsp
sub rsp, 0x60              ; Allocate stack space (shadow space + locals)
mov [rbp-0x08], rcx        ; Save callData pointer

; === Get jvm.dll Handle ===
mov rcx, [rbp-0x08]        ; callData
lea rdx, [rcx+0x10]        ; callData->strJvm ("jvm")
mov rax, [rcx]             ; callData->GetModuleHandleA
call rax                    ; GetModuleHandleA("jvm")
test rax, rax
jz error_no_jvm            ; Jump if NULL
mov [rbp-0x10], rax        ; Save jvm.dll handle

; === Get JVM_EnqueueOperation Address ===
; Try without underscore first
mov rcx, [rbp-0x10]        ; jvm.dll handle
mov rdx, [rbp-0x08]
lea rdx, [rdx+0x31]        ; callData->strEnqueue + 1 (skip '_')
mov rax, [rbp-0x08]
mov rax, [rax+0x08]        ; callData->GetProcAddress
call rax                    ; GetProcAddress(jvm, "JVM_EnqueueOperation")
test rax, rax
jnz got_function           ; Jump if found

; Try with underscore
mov rcx, [rbp-0x10]
mov rdx, [rbp-0x08]
lea rdx, [rdx+0x30]        ; callData->strEnqueue ("_JVM_EnqueueOperation")
mov rax, [rbp-0x08]
mov rax, [rax+0x08]
call rax
test rax, rax
jz error_no_function

got_function:
mov [rbp-0x18], rax        ; Save function pointer

; === Call JVM_EnqueueOperation ===
; x64 calling convention: RCX, RDX, R8, R9, then stack
mov rcx, [rbp-0x08]
lea rcx, [rcx+0x150]       ; args[0]
mov rdx, [rbp-0x08]
lea rdx, [rdx+0x550]       ; args[1]
mov r8, [rbp-0x08]
lea r8, [r8+0x950]         ; args[2]
mov r9, [rbp-0x08]
lea r9, [r9+0xD50]         ; args[3]
mov rax, [rbp-0x08]
lea rax, [rax+0x50]        ; pipeName
mov [rsp+0x20], rax        ; 5th argument on stack
mov rax, [rbp-0x18]        ; Function pointer
call rax                    ; JVM_EnqueueOperation(...)
jmp done

; === Error Handlers ===
error_no_jvm:
mov rax, 1001              ; Error code: can't load jvm.dll
jmp done

error_no_function:
mov rax, 1002              ; Error code: can't find function

; === Function Epilogue ===
done:
add rsp, 0x60
pop rbp
ret
```

### x86 Assembly (32-bit)

```nasm
; Function: remote_thread_entry
; Parameter: [ebp+8] = pointer to callData (on stack)
; Returns: EAX = result code

; === Function Prologue ===
push ebp
mov ebp, esp
sub esp, 0x20              ; Allocate locals

; === Get callData ===
mov eax, [ebp+8]
mov [ebp-4], eax           ; Save callData pointer

; === Get jvm.dll Handle ===
mov eax, [ebp-4]
lea ecx, [eax+0x10]        ; strJvm
push ecx
mov eax, [eax]             ; GetModuleHandleA
call eax                    ; GetModuleHandleA("jvm")
test eax, eax
jz error_no_jvm
mov [ebp-8], eax           ; Save handle

; === Get Function Address ===
mov eax, [ebp-4]
lea ecx, [eax+0x31]        ; strEnqueue + 1
push ecx
push dword [ebp-8]         ; jvm handle
mov eax, [ebp-4]
mov eax, [eax+4]           ; GetProcAddress
call eax
test eax, eax
jz error_no_function

; === Call JVM_EnqueueOperation ===
; x86 stdcall: arguments on stack, right to left
mov ecx, [ebp-4]
lea edx, [ecx+0x50]        ; pipeName
push edx
lea edx, [ecx+0xD50]       ; args[3]
push edx
lea edx, [ecx+0x950]       ; args[2]
push edx
lea edx, [ecx+0x550]       ; args[1]
push edx
lea edx, [ecx+0x150]       ; args[0]
push edx
call eax                    ; JVM_EnqueueOperation(...)
jmp done

; === Error Handlers ===
error_no_jvm:
mov eax, 1001
jmp done

error_no_function:
mov eax, 1002

; === Function Epilogue ===
done:
mov esp, ebp
pop ebp
ret 4                      ; stdcall: caller cleans stack
```

## Shellcode Generation

### Manual Assembly Approach

The shellcode is hand-written assembly converted to byte arrays:

```go
// x64 version
var remoteThreadShellcodeX64 = []byte{
    // push rbp
    0x55,
    // mov rbp, rsp
    0x48, 0x89, 0xE5,
    // sub rsp, 0x60
    0x48, 0x83, 0xEC, 0x60,
    // mov [rbp-0x08], rcx
    0x48, 0x89, 0x4D, 0xF8,
    // ... rest of instructions
}
```

### Tools for Shellcode Development

1. **NASM** - Netwide Assembler
   ```bash
   nasm -f bin shellcode.asm -o shellcode.bin
   xxd -i shellcode.bin > shellcode.go
   ```

2. **Metasm** - Ruby assembler framework
   ```ruby
   require 'metasm'
   sc = Metasm::Shellcode.assemble(Metasm::X64.new, <<EOS
     push rbp
     mov rbp, rsp
     ...
   EOS
   )
   puts sc.encode_string.unpack('C*').map { |b| sprintf('0x%02X', b) }.join(', ')
   ```

3. **Online Assemblers**
   - [defuse.ca/online-x86-assembler.htm](https://defuse.ca/online-x86-assembler.htm)
   - [shell-storm.org/online/Online-Assembler-and-Disassembler/](http://shell-storm.org/online/Online-Assembler-and-Disassembler/)

### Testing Shellcode

Create a standalone C test program:

```c
#include <windows.h>
#include <stdio.h>

typedef int (__stdcall *ThreadFunc)(void* param);

int main() {
    unsigned char shellcode[] = {
        0x55, 0x48, 0x89, 0xE5, // ... your shellcode
    };
    
    // Allocate executable memory
    void* mem = VirtualAlloc(NULL, sizeof(shellcode), 
                             MEM_COMMIT, PAGE_EXECUTE_READWRITE);
    memcpy(mem, shellcode, sizeof(shellcode));
    
    // Prepare callData
    CallData data = { ... };
    
    // Execute
    ThreadFunc func = (ThreadFunc)mem;
    int result = func(&data);
    
    printf("Result: %d\n", result);
    VirtualFree(mem, 0, MEM_RELEASE);
    return 0;
}
```

## Platform Considerations

### x64 vs x86

| Feature | x64 | x86 |
|---------|-----|-----|
| **Calling Convention** | Microsoft x64 (RCX, RDX, R8, R9, stack) | stdcall (stack, right-to-left) |
| **Pointer Size** | 8 bytes | 4 bytes |
| **Stack Alignment** | 16 bytes | 4 bytes |
| **Shadow Space** | Required (32 bytes) | Not required |
| **Return Convention** | RAX | EAX |

### Bitness Detection

jambo automatically selects the correct shellcode:

```go
func getRemoteThreadShellcode() []byte {
    var thisWow64 bool
    windows.IsWow64Process(windows.CurrentProcess(), &thisWow64)
    
    if thisWow64 {
        return remoteThreadShellcodeX86  // 32-bit
    }
    return remoteThreadShellcodeX64      // 64-bit
}
```

### JVM Function Signatures

```c
// JVM_EnqueueOperation signature (jvm.dll export)
int __stdcall JVM_EnqueueOperation(
    char* cmd,       // Command string
    char* arg0,      // Argument 0
    char* arg1,      // Argument 1
    char* arg2,      // Argument 2
    char* pipename   // Named pipe path
);
```

Different JDK versions may have slight variations:
- Some export `JVM_EnqueueOperation`
- Some export `_JVM_EnqueueOperation`
- Some have `_JVM_EnqueueOperation@20` (decorated name)

Our shellcode tries both variants.

## Security Implications

### Permissions Required

- **Administrator privileges**, OR
- **SeDebugPrivilege** enabled

To enable SeDebugPrivilege programmatically:

```go
func enableDebugPrivileges() error {
    var token windows.Token
    windows.OpenThreadToken(windows.CurrentThread(), 
                           TOKEN_ADJUST_PRIVILEGES, false, &token)
    
    var luid windows.LUID
    windows.LookupPrivilegeValue(nil, 
                                 windows.StringToUTF16Ptr("SeDebugPrivilege"), 
                                 &luid)
    
    tp := windows.Tokenprivileges{
        PrivilegeCount: 1,
        Privileges: [1]windows.LUIDAndAttributes{{
            Luid:       luid,
            Attributes: SE_PRIVILEGE_ENABLED,
        }},
    }
    
    return windows.AdjustTokenPrivileges(token, false, &tp, ...)
}
```

### Antivirus Detection

Remote thread injection is a common technique used by malware. Antivirus software may flag jambo:

**Common Heuristics**:
- `OpenProcess` with `PROCESS_ALL_ACCESS`
- `VirtualAllocEx` with `PAGE_EXECUTE_READWRITE`
- `CreateRemoteThread`
- Executable memory in foreign process

**Mitigation**:
1. Code signing certificate
2. Whitelist jambo in AV software
3. Use less suspicious techniques (e.g., `PAGE_EXECUTE_READ` after writing)

### Defense Mechanisms

Modern Windows has several protections:

1. **DEP (Data Execution Prevention)**: Prevents code execution from data pages
   - Mitigated by: Using `PAGE_EXECUTE_READWRITE`

2. **ASLR (Address Space Layout Randomization)**: Randomizes memory addresses
   - Not an issue: We resolve addresses dynamically

3. **CFG (Control Flow Guard)**: Validates indirect call targets
   - Not an issue: JVM doesn't have CFG on `JVM_EnqueueOperation`

4. **PPL (Protected Process Light)**: Protects processes from tampering
   - Blocked: Cannot attach to PPL processes

## Debugging Shellcode

### WinDbg

Attach WinDbg to target process:

```
windbg -p <pid>

# Set breakpoint on shellcode
bp <shellcode_address>

# Single-step
t

# View registers
r

# View memory
dd <address>
```

### Visual Studio Debugger

1. Attach to target process
2. Debug → Windows → Disassembly
3. Set breakpoint on injected code address
4. Step through assembly

### Debugging Tips

1. **Add NOP slides**: Make it easier to find shellcode
   ```go
   shellcode := []byte{
       0x90, 0x90, 0x90, 0x90,  // NOP NOP NOP NOP
       // ... actual code
   }
   ```

2. **Use `int 3` breakpoint**: Insert debugger trap
   ```nasm
   int 3  ; Triggers debugger if attached
   ```

3. **Log to file**: Write debug info to a file from shellcode
   ```nasm
   ; Call CreateFileA, WriteFile, etc.
   ```

## References

### Assembly References

- [Intel 64 and IA-32 Architectures Software Developer's Manual](https://www.intel.com/content/www/us/en/developer/articles/technical/intel-sdm.html)
- [Microsoft x64 Calling Convention](https://docs.microsoft.com/en-us/cpp/build/x64-calling-convention)
- [x86 Opcode Reference](http://ref.x86asm.net/)

### Windows API

- [CreateRemoteThread](https://docs.microsoft.com/en-us/windows/win32/api/processthreadsapi/nf-processthreadsapi-createremotethread)
- [VirtualAllocEx](https://docs.microsoft.com/en-us/windows/win32/api/memoryapi/nf-memoryapi-virtualallocex)
- [WriteProcessMemory](https://docs.microsoft.com/en-us/windows/win32/api/memoryapi/nf-memoryapi-writeprocessmemory)

### Shellcode Resources

- [Shellcode Injection](https://www.ired.team/offensive-security/code-injection-process-injection/shellcode-injection)
- [Windows Shellcode Development](https://www.exploit-db.com/papers/13660)

### Tools

- [NASM - Netwide Assembler](https://www.nasm.us/)
- [Radare2 - Reverse Engineering Framework](https://rada.re/)
- [Keystone Engine - Assembler Framework](https://www.keystone-engine.org/)
