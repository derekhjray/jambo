# Windows Shellcode for JVM Attach

本文档解释了在 jambo 中如何生成和使用 Windows shellcode 进行 JVM 动态附加。

## 目录

- [概述](#概述)
- [远程线程注入](#远程线程注入)
- [Shellcode 架构](#shellcode-架构)
- [Shellcode 生成](#shellcode-生成)
- [平台注意事项](#平台注意事项)
- [安全影响](#安全影响)

## 概述

在 Windows 上，HotSpot JVM 不像 Linux 那样提供基于套接字的附加机制。相反，jambo 使用**远程线程注入**在目标 JVM 进程内执行代码。

### 为什么需要 Shellcode？

Go 不支持创建可以注入到另一个进程中的位置无关代码。因此，我们需要手写的汇编代码（shellcode），它：

1. 可以在任何内存位置执行（位置无关）
2. 调用 Windows API 函数
3. 调用 jvm.dll 中的 `JVM_EnqueueOperation`
4. 返回结果代码

### 攻击流程

```
┌──────────────┐
│   jambo.exe  │
│  (Go 代码)   │
└──────┬───────┘
       │
       │ 1. 创建命名管道
       │    \\.\pipe\javatool<tid>
       │
       │ 2. 打开目标进程
       │    OpenProcess(PROCESS_ALL_ACCESS, pid)
       │
       │ 3. 分配可执行内存
       │    VirtualAllocEx(..., PAGE_EXECUTE_READWRITE)
       │
       │ 4. 写入 shellcode
       │    WriteProcessMemory(shellcode_bytes)
       │
       │ 5. 分配数据结构
       │    VirtualAllocEx(sizeof(callData))
       │
       │ 6. 写入 callData
       │    WriteProcessMemory(callData)
       │
       │ 7. 创建远程线程
       │    CreateRemoteThread(shellcode_addr, callData_addr)
       │
       ▼
┌──────────────────────────┐
│   目标 JVM 进程           │
│                          │
│  ┌────────────────────┐  │
│  │  Shellcode 线程     │  │
│  │                    │  │
│  │  1. 获取 jvm.dll   │  │
│  │  2. 查找函数       │  │
│  │  3. 调用 JVM_*     │  │
│  │  4. JVM → 管道     │  │
│  └────────────────────┘  │
└──────────────────────────┘
       │
       │ 结果写入命名管道
       │
       ▼
┌──────────────┐
│   jambo.exe  │
│  读取结果    │
└──────────────┘
```

## 远程线程注入

### 进程内存布局

```
目标 JVM 进程内存：

0x00000000 ┌──────────────────┐
           │   Null 页面      │
           ├──────────────────┤
           │   .text          │ ← jvm.dll 代码
           │   .data          │
           ├──────────────────┤
           │   堆             │
           ├──────────────────┤
           │   ...            │
           ├──────────────────┤
0x???????? │ ← shellcode      │ ← jambo 注入
           │   (RWX)          │
           ├──────────────────┤
0x???????? │ ← callData       │ ← jambo 注入
           │   (RW)           │
           ├──────────────────┤
           │   栈             │
0xFFFFFFFF └──────────────────┘
```

### callData 结构

传递给 shellcode 的数据结构：

```c
struct callData {
    // 函数指针（在目标进程中解析）
    FARPROC GetModuleHandleA;  // +0x00（x64 上 8 字节，x86 上 4 字节）
    FARPROC GetProcAddress;    // +0x08 (x64) / +0x04 (x86)
    
    // 字符串
    char strJvm[32];           // +0x10 : "jvm\0"
    char strEnqueue[32];       // +0x30 : "_JVM_EnqueueOperation\0"
    char pipeName[260];        // +0x50 : "\\.\pipe\javatool12345\0"
    
    // 命令参数
    char args[4][1024];        // +0x150 : 总共 4KB
                               // args[0] 在 +0x150
                               // args[1] 在 +0x550
                               // args[2] 在 +0x950
                               // args[3] 在 +0xD50
};

// 总大小：约 4.5 KB
```

### 内存分配

```go
// 1. 为 shellcode 分配可执行内存
remoteCode := VirtualAllocEx(
    hProcess,
    NULL,
    len(shellcode),
    MEM_COMMIT,
    PAGE_EXECUTE_READWRITE  // RWX 权限
)

// 2. 写入 shellcode
WriteProcessMemory(hProcess, remoteCode, shellcode, len(shellcode))

// 3. 为 callData 分配内存
remoteData := VirtualAllocEx(
    hProcess,
    NULL,
    sizeof(callData),
    MEM_COMMIT,
    PAGE_READWRITE  // RW 权限
)

// 4. 写入 callData
WriteProcessMemory(hProcess, remoteData, &data, sizeof(data))
```

## Shellcode 架构

### x64 汇编（64 位）

```nasm
; 函数：remote_thread_entry
; 参数：RCX = callData 指针
; 返回：RAX = 结果代码（0 = 成功，1001/1002 = 错误）

; === 函数序言 ===
push rbp
mov rbp, rsp
sub rsp, 0x60              ; 分配栈空间（影子空间 + 局部变量）
mov [rbp-0x08], rcx        ; 保存 callData 指针

; === 获取 jvm.dll 句柄 ===
mov rcx, [rbp-0x08]        ; callData
lea rdx, [rcx+0x10]        ; callData->strJvm ("jvm")
mov rax, [rcx]             ; callData->GetModuleHandleA
call rax                    ; GetModuleHandleA("jvm")
test rax, rax
jz error_no_jvm            ; 如果为 NULL 则跳转
mov [rbp-0x10], rax        ; 保存 jvm.dll 句柄

; === 获取 JVM_EnqueueOperation 地址 ===
; 首先尝试不带下划线的版本
mov rcx, [rbp-0x10]        ; jvm.dll 句柄
mov rdx, [rbp-0x08]
lea rdx, [rdx+0x31]        ; callData->strEnqueue + 1（跳过 '_'）
mov rax, [rbp-0x08]
mov rax, [rax+0x08]        ; callData->GetProcAddress
call rax                    ; GetProcAddress(jvm, "JVM_EnqueueOperation")
test rax, rax
jnz got_function           ; 如果找到则跳转

; 尝试带下划线的版本
mov rcx, [rbp-0x10]
mov rdx, [rbp-0x08]
lea rdx, [rdx+0x30]        ; callData->strEnqueue ("_JVM_EnqueueOperation")
mov rax, [rbp-0x08]
mov rax, [rax+0x08]
call rax
test rax, rax
jz error_no_function

got_function:
mov [rbp-0x18], rax        ; 保存函数指针

; === 调用 JVM_EnqueueOperation ===
; x64 调用约定：RCX、RDX、R8、R9，然后是栈
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
mov [rsp+0x20], rax        ; 第 5 个参数在栈上
mov rax, [rbp-0x18]        ; 函数指针
call rax                    ; JVM_EnqueueOperation(...)
jmp done

; === 错误处理 ===
error_no_jvm:
mov rax, 1001              ; 错误代码：无法加载 jvm.dll
jmp done

error_no_function:
mov rax, 1002              ; 错误代码：找不到函数

; === 函数尾声 ===
done:
add rsp, 0x60
pop rbp
ret
```

### x86 汇编（32 位）

```nasm
; 函数：remote_thread_entry
; 参数：[ebp+8] = callData 指针（在栈上）
; 返回：EAX = 结果代码

; === 函数序言 ===
push ebp
mov ebp, esp
sub esp, 0x20              ; 分配局部变量

; === 获取 callData ===
mov eax, [ebp+8]
mov [ebp-4], eax           ; 保存 callData 指针

; === 获取 jvm.dll 句柄 ===
mov eax, [ebp-4]
lea ecx, [eax+0x10]        ; strJvm
push ecx
mov eax, [eax]             ; GetModuleHandleA
call eax                    ; GetModuleHandleA("jvm")
test eax, eax
jz error_no_jvm
mov [ebp-8], eax           ; 保存句柄

; === 获取函数地址 ===
mov eax, [ebp-4]
lea ecx, [eax+0x31]        ; strEnqueue + 1
push ecx
push dword [ebp-8]         ; jvm 句柄
mov eax, [ebp-4]
mov eax, [eax+4]           ; GetProcAddress
call eax
test eax, eax
jz error_no_function

; === 调用 JVM_EnqueueOperation ===
; x86 stdcall：参数在栈上，从右到左
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

; === 错误处理 ===
error_no_jvm:
mov eax, 1001
jmp done

error_no_function:
mov eax, 1002

; === 函数尾声 ===
done:
mov esp, ebp
pop ebp
ret 4                      ; stdcall：调用者清理栈
```

## Shellcode 生成

### 手动汇编方法

shellcode 是手写汇编转换为字节数组：

```go
// x64 版本
var remoteThreadShellcodeX64 = []byte{
    // push rbp
    0x55,
    // mov rbp, rsp
    0x48, 0x89, 0xE5,
    // sub rsp, 0x60
    0x48, 0x83, 0xEC, 0x60,
    // mov [rbp-0x08], rcx
    0x48, 0x89, 0x4D, 0xF8,
    // ... 其余指令
}
```

### Shellcode 开发工具

1. **NASM** - Netwide Assembler
   ```bash
   nasm -f bin shellcode.asm -o shellcode.bin
   xxd -i shellcode.bin > shellcode.go
   ```

2. **Metasm** - Ruby 汇编框架
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

3. **在线汇编器**
   - [defuse.ca/online-x86-assembler.htm](https://defuse.ca/online-x86-assembler.htm)
   - [shell-storm.org/online/Online-Assembler-and-Disassembler/](http://shell-storm.org/online/Online-Assembler-and-Disassembler/)

### 测试 Shellcode

创建独立的 C 测试程序：

```c
#include <windows.h>
#include <stdio.h>

typedef int (__stdcall *ThreadFunc)(void* param);

int main() {
    unsigned char shellcode[] = {
        0x55, 0x48, 0x89, 0xE5, // ... 你的 shellcode
    };
    
    // 分配可执行内存
    void* mem = VirtualAlloc(NULL, sizeof(shellcode), 
                             MEM_COMMIT, PAGE_EXECUTE_READWRITE);
    memcpy(mem, shellcode, sizeof(shellcode));
    
    // 准备 callData
    CallData data = { ... };
    
    // 执行
    ThreadFunc func = (ThreadFunc)mem;
    int result = func(&data);
    
    printf("Result: %d\n", result);
    VirtualFree(mem, 0, MEM_RELEASE);
    return 0;
}
```

## 平台注意事项

### x64 vs x86

| 特性 | x64 | x86 |
|------|-----|-----|
| **调用约定** | Microsoft x64（RCX、RDX、R8、R9、栈） | stdcall（栈，从右到左） |
| **指针大小** | 8 字节 | 4 字节 |
| **栈对齐** | 16 字节 | 4 字节 |
| **影子空间** | 必需（32 字节） | 不需要 |
| **返回约定** | RAX | EAX |

### 位数检测

jambo 自动选择正确的 shellcode：

```go
func getRemoteThreadShellcode() []byte {
    var thisWow64 bool
    windows.IsWow64Process(windows.CurrentProcess(), &thisWow64)
    
    if thisWow64 {
        return remoteThreadShellcodeX86  // 32 位
    }
    return remoteThreadShellcodeX64      // 64 位
}
```

### JVM 函数签名

```c
// JVM_EnqueueOperation 签名（jvm.dll 导出）
int __stdcall JVM_EnqueueOperation(
    char* cmd,       // 命令字符串
    char* arg0,      // 参数 0
    char* arg1,      // 参数 1
    char* arg2,      // 参数 2
    char* pipename   // 命名管道路径
);
```

不同的 JDK 版本可能有细微差异：
- 一些导出 `JVM_EnqueueOperation`
- 一些导出 `_JVM_EnqueueOperation`
- 一些有 `_JVM_EnqueueOperation@20`（修饰名）

我们的 shellcode 会尝试两种变体。

## 安全影响

### 所需权限

- **管理员权限**，或
- 启用 **SeDebugPrivilege**

以编程方式启用 SeDebugPrivilege：

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

### 杀毒软件检测

远程线程注入是恶意软件使用的常见技术。杀毒软件可能会标记 jambo：

**常见启发式规则**：
- 使用 `PROCESS_ALL_ACCESS` 的 `OpenProcess`
- 使用 `PAGE_EXECUTE_READWRITE` 的 `VirtualAllocEx`
- `CreateRemoteThread`
- 外部进程中的可执行内存

**缓解措施**：
1. 代码签名证书
2. 在杀毒软件中将 jambo 加入白名单
3. 使用不太可疑的技术（例如，写入后使用 `PAGE_EXECUTE_READ`）

### 防御机制

现代 Windows 有几种保护：

1. **DEP（数据执行保护）**：防止从数据页执行代码
   - 缓解方法：使用 `PAGE_EXECUTE_READWRITE`

2. **ASLR（地址空间布局随机化）**：随机化内存地址
   - 不是问题：我们动态解析地址

3. **CFG（控制流保护）**：验证间接调用目标
   - 不是问题：JVM 的 `JVM_EnqueueOperation` 没有 CFG

4. **PPL（受保护进程轻量级）**：保护进程免受篡改
   - 被阻止：无法附加到 PPL 进程

## 调试 Shellcode

### WinDbg

将 WinDbg 附加到目标进程：

```
windbg -p <pid>

# 在 shellcode 上设置断点
bp <shellcode_address>

# 单步执行
t

# 查看寄存器
r

# 查看内存
dd <address>
```

### Visual Studio 调试器

1. 附加到目标进程
2. 调试 → 窗口 → 反汇编
3. 在注入代码地址上设置断点
4. 逐步执行汇编

### 调试技巧

1. **添加 NOP 滑梯**：更容易找到 shellcode
   ```go
   shellcode := []byte{
       0x90, 0x90, 0x90, 0x90,  // NOP NOP NOP NOP
       // ... 实际代码
   }
   ```

2. **使用 `int 3` 断点**：插入调试器陷阱
   ```nasm
   int 3  ; 如果附加了调试器则触发
   ```

3. **记录到文件**：从 shellcode 写入调试信息到文件
   ```nasm
   ; 调用 CreateFileA、WriteFile 等
   ```

## 参考资料

### 汇编参考

- [Intel 64 和 IA-32 架构软件开发人员手册](https://www.intel.com/content/www/us/en/developer/articles/technical/intel-sdm.html)
- [Microsoft x64 调用约定](https://docs.microsoft.com/en-us/cpp/build/x64-calling-convention)
- [x86 操作码参考](http://ref.x86asm.net/)

### Windows API

- [CreateRemoteThread](https://docs.microsoft.com/en-us/windows/win32/api/processthreadsapi/nf-processthreadsapi-createremotethread)
- [VirtualAllocEx](https://docs.microsoft.com/en-us/windows/win32/api/memoryapi/nf-memoryapi-virtualallocex)
- [WriteProcessMemory](https://docs.microsoft.com/en-us/windows/win32/api/memoryapi/nf-memoryapi-writeprocessmemory)

### Shellcode 资源

- [Shellcode 注入](https://www.ired.team/offensive-security/code-injection-process-injection/shellcode-injection)
- [Windows Shellcode 开发](https://www.exploit-db.com/papers/13660)

### 工具

- [NASM - Netwide Assembler](https://www.nasm.us/)
- [Radare2 - 逆向工程框架](https://rada.re/)
- [Keystone Engine - 汇编框架](https://www.keystone-engine.org/)
