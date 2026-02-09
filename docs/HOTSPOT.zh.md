# HotSpot JVM 动态附加机制

本文档描述 HotSpot JVM 动态附加机制的工作原理以及 jambo 的实现方式。

## 目录

- [概述](#概述)
- [Linux 上的附加协议](#linux-上的附加协议)
- [Windows 上的附加协议](#windows-上的附加协议)
- [实现细节](#实现细节)
- [参考资料](#参考资料)

## 概述

HotSpot JVM 提供了一种动态附加机制，允许外部进程连接到正在运行的 JVM 并执行各种诊断命令。这是 `jstack`、`jmap`、`jcmd` 等工具的基础。

### 关键特性

- **加载 Java 代理**：动态加载 Java 代理到正在运行的 JVM 中
- **执行诊断命令**：线程转储、堆转储、VM 信息等
- **无需重启 JVM**：在不中断运行应用程序的情况下附加
- **跨平台**：在 Linux、Windows、macOS 等平台上工作

## Linux 上的附加协议

### 架构

在 Linux 上，HotSpot 使用 Unix 域套接字进行进程间通信：

```
┌─────────────┐                    ┌─────────────┐
│   jambo     │                    │  HotSpot    │
│  (客户端)   │                    │    JVM      │
└──────┬──────┘                    └──────┬──────┘
       │                                  │
       │  1. 创建附加文件                  │
       │  /proc/<pid>/cwd/.attach_pid<pid>│
       ├─────────────────────────────────>│
       │                                  │
       │  2. 发送 SIGQUIT 信号             │
       ├─────────────────────────────────>│
       │                                  │
       │                3. JVM 创建        │
       │                Unix 套接字        │
       │           /tmp/.java_pid<nspid>  │
       │                                  │
       │  4. 连接到套接字                  │
       ├─────────────────────────────────>│
       │                                  │
       │  5. 发送协议版本 + 命令            │
       ├─────────────────────────────────>│
       │                                  │
       │          6. 执行命令              │
       │             并发送响应            │
       │<─────────────────────────────────┤
       │                                  │
       │  7. 关闭连接                      │
       ├─────────────────────────────────>│
       │                                  │
```

### 分步过程

#### 1. 检查现有套接字

首先，检查附加套接字是否已存在：

```bash
/tmp/.java_pid<nspid>
```

如果存在，跳转到步骤 4（连接到套接字）。

#### 2. 创建附加文件

在目标进程的工作目录中创建附加触发文件：

```bash
/proc/<pid>/cwd/.attach_pid<pid>
```

该文件必须由与目标 JVM 进程相同的用户拥有。

#### 3. 发送 SIGQUIT 信号

向目标 JVM 进程发送 SIGQUIT（信号 3）：

```c
kill(pid, SIGQUIT);
```

这会触发 JVM 的附加监听器线程，该线程将：
- 检测附加文件
- 在 `/tmp/.java_pid<nspid>` 创建 Unix 域套接字
- 开始监听连接

#### 4. 等待套接字创建

使用指数退避轮询套接字文件：

```
初始等待：20ms
重试间隔：20ms、40ms、60ms、...（最多 400ms）
总超时时间：约 5 秒
```

#### 5. 连接到套接字

一旦套接字存在，使用 Unix 域套接字连接：

```c
int fd = socket(AF_UNIX, SOCK_STREAM, 0);
struct sockaddr_un addr = {
    .sun_family = AF_UNIX,
    .sun_path = "/tmp/.java_pid<nspid>"
};
connect(fd, (struct sockaddr*)&addr, sizeof(addr));
```

#### 6. 发送命令

使用附加协议格式发送命令：

```
协议版本（1 字节）："1\0"
命令（以 null 结尾）："threaddump\0"
参数 1（以 null 结尾）："\0"
参数 2（以 null 结尾）："\0"
参数 3（以 null 结尾）："\0"
```

**协议详情**：
- 协议版本：始终为 "1"
- 最多 4 个参数
- 每个字段以 null 结尾
- 对于 `jcmd`：仅命令 + 1 个参数
- 对于其他命令：最多 4 个参数
- 过多的参数将用空格合并

**示例 - 线程转储**：
```
"1\0threaddump\0\0\0\0"
```

**示例 - 加载代理**：
```
"1\0load\0/path/agent.jar\0false\0options=value\0"
```

**示例 - jcmd**：
```
"1\0jcmd\0VM.version\0\0\0"
```

#### 7. 读取响应

从套接字读取响应：

```
响应格式：
第 1 行：结果代码（整数作为字符串）
第 2 行以上：命令输出
```

**结果代码**：
- `0`：成功
- `非零`：发生错误

**`load` 命令的特殊处理**：

不同的 JDK 版本有不同的响应格式：

- **JDK 8**：第二行包含代理返回码
  ```
  0\n
  <return_code>\n
  ```

- **JDK 9+**：包含 "return code: " 前缀
  ```
  0\n
  return code: <code>\n
  <output>
  ```

- **JDK 21+**：代理错误以文本形式出现
  ```
  0\n
  <error_message>
  ```

### 协议版本历史

| 版本 | JDK 版本 | 变化 |
|------|---------|------|
| 1 | JDK 6+ | 初始协议 |
| 1 | JDK 9+ | 增强的 load 命令响应 |
| 1 | JDK 21+ | 加载错误报告变更 |

## Windows 上的附加协议

### 架构

Windows 使用基于远程线程注入的不同方法：

```
┌─────────────┐                    ┌─────────────┐
│   jambo     │                    │  HotSpot    │
│  (客户端)   │                    │    JVM      │
└──────┬──────┘                    └──────┬──────┘
       │                                  │
       │  1. 创建命名管道                  │
       │     \\.\pipe\javatool<tid>       │
       │                                  │
       │  2. 打开目标进程                  │
       │     (需要权限)                   │
       ├─────────────────────────────────>│
       │                                  │
       │  3. 在远程进程内存中              │
       │     分配 shellcode               │
       ├─────────────────────────────────>│
       │                                  │
       │  4. 创建远程线程                  │
       │     执行 shellcode               │
       ├─────────────────────────────────>│
       │                                  │
       │        5. Shellcode 执行：       │
       │           - GetModuleHandle(jvm) │
       │           - GetProcAddress(...)  │
       │           - JVM_EnqueueOperation │
       │                                  │
       │  6. JVM 写入命名管道              │
       │<─────────────────────────────────┤
       │                                  │
       │  7. 从管道读取响应                │
       │<─────────────────────────────────┤
       │                                  │
```

### 过程细节

1. **创建命名管道**：`\\.\pipe\javatool<tickcount>`
2. **打开目标进程**：需要 `PROCESS_ALL_ACCESS` 或 `SeDebugPrivilege`
3. **注入代码**：分配可执行内存并写入 shellcode
4. **创建远程线程**：在目标进程中执行 shellcode
5. **Shellcode 执行**：在 jvm.dll 中调用 `JVM_EnqueueOperation`
6. **读取响应**：JVM 将结果写入命名管道

有关详细的 shellcode 实现，请参阅 [Windows Shellcode 文档](WINDOWS_SHELLCODE.zh.md)。

## 实现细节

### 容器支持（Linux）

jambo 自动处理容器化的 JVM：

**命名空间检测**：
```go
// 从 /proc/<pid>/status 读取 NSpid
// 如果 NSpid 与 Pid 不同，进程在容器中
```

**命名空间切换**：
```go
// 进入目标命名空间
setns(net_ns_fd, CLONE_NEWNET)
setns(ipc_ns_fd, CLONE_NEWIPC)
setns(mnt_ns_fd, CLONE_NEWNS)
```

**套接字路径**：
```go
// 使用命名空间 PID 作为套接字路径
socketPath = fmt.Sprintf("/tmp/.java_pid%d", nsPid)
```

### 凭据切换

附加到不同用户拥有的进程时：

```go
// 切换到目标进程凭据
setuid(targetUID)
setgid(targetGID)
```

要求：
- 以 root 身份运行，或
- 具有 `CAP_SETUID` 和 `CAP_SETGID` 能力

### 错误处理

常见错误和解决方案：

| 错误 | 原因 | 解决方案 |
|------|------|---------|
| `Permission denied` | 权限不足 | 使用 sudo 或适当的能力运行 |
| `Process not found` | 无效的 PID 或进程已退出 | 验证 PID 是否正确 |
| `Socket timeout` | JVM 附加监听器未响应 | 检查 JVM 是否启用了附加监听器 |
| `Connection refused` | 套接字存在但不接受连接 | 删除过时的套接字文件 |

### SIGPIPE 处理

```go
// 忽略 SIGPIPE 以防止进程终止
signal.Ignore(syscall.SIGPIPE)
```

如果没有这个，写入已关闭的套接字会导致 jambo 因 SIGPIPE 而终止。

## 参考资料

### 官方文档

- [JVM Tool Interface (JVMTI)](https://docs.oracle.com/javase/8/docs/platform/jvmti/jvmti.html)
- [Java Attach API](https://docs.oracle.com/javase/8/docs/jdk/api/attach/spec/com/sun/tools/attach/VirtualMachine.html)

### 源代码参考

- [OpenJDK HotSpot Attach Listener](https://github.com/openjdk/jdk/blob/master/src/hotspot/os/linux/attachListener_linux.cpp)
- [jattach - 原始 C 实现](https://github.com/jattach/jattach)

### 相关工具

- `jcmd` - JVM 命令行诊断工具
- `jstack` - 线程转储工具
- `jmap` - 内存映射工具
- `jstat` - JVM 统计监控工具

### 协议版本

附加协议版本 "1" 在各个 JDK 版本中保持稳定，仅对特定命令的响应格式进行了细微更改。
