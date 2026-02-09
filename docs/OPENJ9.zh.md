# OpenJ9 JVM 动态附加机制

本文档描述 Eclipse OpenJ9（原 IBM J9）JVM 动态附加机制的工作原理以及 jambo 的实现方式。

## 目录

- [概述](#概述)
- [与 HotSpot 的差异](#与-hotspot-的差异)
- [附加协议](#附加协议)
- [命令转换](#命令转换)
- [实现细节](#实现细节)
- [参考资料](#参考资料)

## 概述

OpenJ9 使用与 HotSpot 不同的附加机制。HotSpot 使用 Unix 域套接字，而 OpenJ9 使用更复杂的系统，包括：
- 共享内存
- 信号量
- TCP/IP 套接字（IPv4/IPv6）
- 基于文件的通知系统

### 与 HotSpot 的主要差异

| 特性 | HotSpot | OpenJ9 |
|------|---------|--------|
| **传输方式** | Unix 域套接字 | TCP/IP 套接字 |
| **检测方式** | 套接字文件存在性 | `.com_ibm_tools_attach/{pid}/attachInfo` 文件 |
| **命令格式** | 直接命令 | ATTACH_* 前缀命令 |
| **响应格式** | 纯文本 | Java Properties 格式（转义） |
| **触发方式** | SIGQUIT + 附加文件 | 信号量通知 |

## 附加协议

### 架构

```
┌─────────────┐                    ┌─────────────┐
│   jambo     │                    │   OpenJ9    │
│  (客户端)   │                    │    JVM      │
└──────┬──────┘                    └──────┬──────┘
       │                                  │
       │  1. 获取附加锁                    │
       │     _attachlock 文件             │
       │                                  │
       │  2. 创建 TCP 监听套接字           │
       │     (随机端口)                   │
       │                                  │
       │  3. 写入 replyInfo 文件          │
       │     - 随机密钥                   │
       │     - 端口号                     │
       ├─────────────────────────────────>│
       │                                  │
       │  4. 锁定通知文件                  │
       │     attachNotificationSync       │
       │                                  │
       │  5. 增加信号量                    │
       │     _notifier 信号量             │
       ├─────────────────────────────────>│
       │                                  │
       │        6. JVM 唤醒并              │
       │           读取 replyInfo         │
       │                                  │
       │  7. JVM 连接到 TCP 套接字         │
       │<─────────────────────────────────┤
       │                                  │
       │  8. 验证连接密钥                  │
       │     ATTACH_CONNECTED <key>       │
       │<─────────────────────────────────┤
       │                                  │
       │  9. 发送转换后的命令              │
       │     ATTACH_DIAGNOSTICS:...       │
       ├─────────────────────────────────>│
       │                                  │
       │        10. 执行命令               │
       │            并发送响应            │
       │<─────────────────────────────────┤
       │                                  │
       │  11. 发送 ATTACH_DETACHED        │
       ├─────────────────────────────────>│
       │                                  │
```

### 文件系统结构

OpenJ9 使用专用的目录结构：

```
/tmp/.com_ibm_tools_attach/
├── _attachlock              # 全局附加锁
├── _notifier                # 通知信号量
├── <pid1>/
│   ├── attachInfo           # 表示 JVM 支持附加
│   ├── replyInfo            # 连接信息（密钥 + 端口）
│   └── attachNotificationSync  # 每个 JVM 的通知锁
├── <pid2>/
│   ├── attachInfo
│   ├── replyInfo
│   └── attachNotificationSync
└── ...
```

### 检测

通过检查 `attachInfo` 文件来检测 OpenJ9 JVM：

```go
func (o *openJ9) Detect(nspid int) bool {
    tmpPath, _ := getTempPath(nspid)
    attachInfoPath := fmt.Sprintf("%s/.com_ibm_tools_attach/%d/attachInfo", tmpPath, nspid)
    
    _, err := os.Stat(attachInfoPath)
    return err == nil
}
```

### 连接过程

#### 1. 获取全局锁

```go
lockFile := "/tmp/.com_ibm_tools_attach/_attachlock"
lockFd := open(lockFile, O_WRONLY|O_CREAT, 0666)
flock(lockFd, LOCK_EX)  // 排他锁
```

#### 2. 创建监听套接字

在随机端口上创建 TCP 套接字（首选 IPv6，回退到 IPv4）：

```go
// 首先尝试 IPv6
socket(AF_INET6, SOCK_STREAM, 0)
bind(sockfd, {.sin6_family=AF_INET6, .sin6_port=0}, ...)  // 端口 0 = 随机
listen(sockfd, 0)
getsockname(sockfd, ...)  // 获取分配的端口
```

#### 3. 写入 replyInfo 文件

```
文件：/tmp/.com_ibm_tools_attach/<pid>/replyInfo
内容：
<16位十六进制密钥>
<端口号>
```

示例：
```
1a2b3c4d5e6f7890
54321
```

密钥是一个随机的 64 位值，用于验证连接。

#### 4. 锁定通知文件

锁定所有 JVM 的 `attachNotificationSync` 文件以防止竞争条件：

```go
for each JVM directory in .com_ibm_tools_attach/ {
    lockFile := "<jvm_dir>/attachNotificationSync"
    lockFd := open(lockFile, O_WRONLY|O_CREAT, 0666)
    flock(lockFd, LOCK_EX)
    // 保持锁定直到信号量通知之后
}
```

#### 5. 通过信号量通知

增加信号量以唤醒 JVM 附加监听器：

```go
semKey := ftok("/tmp/.com_ibm_tools_attach/_notifier", 0xa1)
sem := semget(semKey, 1, IPC_CREAT|0666)
semop(sem, {.sem_op=1}, 1)  // 增加
```

#### 6. 接受连接

等待 JVM 连接（5 秒超时）：

```go
setsockopt(sockfd, SOL_SOCKET, SO_RCVTIMEO, {tv_sec=5}, ...)
clientFd := accept(sockfd, ...)
```

#### 7. 验证连接

读取并验证连接头：

```
预期：\u201cATTACH_CONNECTED <16位十六进制密钥> \u201d
示例：\u201cATTACH_CONNECTED 1a2b3c4d5e6f7890 \u201d
```

如果密钥不匹配，拒绝连接。

## 命令转换

OpenJ9 使用与 HotSpot 不同的命令名称。Jambo 自动转换标准命令：

### 转换表

| HotSpot 命令 | OpenJ9 命令 | 描述 |
|-------------|------------|------|
| `load <path> false <opts>` | `ATTACH_LOADAGENT(<path>,<opts>)` | 加载 Java 代理（相对路径） |
| `load <path> true <opts>` | `ATTACH_LOADAGENTPATH(<path>,<opts>)` | 加载 Java 代理（绝对路径） |
| `jcmd <cmd> <args>` | `ATTACH_DIAGNOSTICS:<cmd>,<args>` | 执行诊断命令 |
| `threaddump` | `ATTACH_DIAGNOSTICS:Thread.print` | 获取线程转储 |
| `dumpheap <file>` | `ATTACH_DIAGNOSTICS:Dump.heap,<file>` | 堆转储 |
| `inspectheap` | `ATTACH_DIAGNOSTICS:GC.class_histogram` | 堆直方图 |
| `datadump` | `ATTACH_DIAGNOSTICS:Dump.java` | Java 核心转储 |
| `properties` | `ATTACH_GETSYSTEMPROPERTIES` | 获取系统属性 |
| `agentProperties` | `ATTACH_GETAGENTPROPERTIES` | 获取代理属性 |

### 转换实现

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
        
        // 检查是否为绝对路径
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
        
    // ... 更多情况
    }
    
    return cmd  // 未知命令，直接传递
}
```

### 命令格式

OpenJ9 命令作为单个以 null 结尾的字符串发送：

```
"ATTACH_DIAGNOSTICS:VM.version\0"
```

不像 HotSpot 那样有协议版本或多个参数。

## 响应格式

### 响应结构

OpenJ9 响应以 null 结尾，可能采用 Java Properties 格式编码：

```
<响应文本>\0
```

### 响应类型

#### 1. ACK 响应（用于 load 命令）

```
ATTACH_ACK\0
```

成功：代理加载成功。

#### 2. 错误响应（用于 load 命令）

```
ATTACH_ERR AgentInitializationException <code>\0
```

示例：
```
ATTACH_ERR AgentInitializationException 1\0
```

#### 3. 诊断结果

```
openj9_diagnostics.string_result=<转义结果>\0
```

示例：
```
openj9_diagnostics.string_result=Thread Dump:\n\tThread-1 ...\0
```

### 字符串反转义

OpenJ9 对诊断输出使用 Java Properties 格式转义：

| 转义序列 | 字符 |
|---------|------|
| `\\` | `\` |
| `\n` | 换行符 |
| `\t` | 制表符 |
| `\r` | 回车符 |
| `\f` | 换页符 |
| `\"` | 引号 |

实现：

```go
func (o *openJ9) unescapeString(s string) string {
    // 移除尾随换行符
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

## 实现细节

### 读取响应

与 HotSpot（使用基于行的协议）不同，OpenJ9 需要读取直到 null 终止符：

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
        
        // 检查 null 终止符
        if buf[len(buf)-1] == 0 {
            buf = buf[:len(buf)-1]  // 移除 null
            break
        }
        
        if len(buf) > 10*1024*1024 {  // 10MB 限制
            return "", errors.New("response too large")
        }
    }
    
    response := string(buf)
    
    // 根据命令类型解析响应
    // ...
}
```

### 分离

命令执行后，发送分离通知：

```go
syscall.Write(conn.fd, []byte("ATTACH_DETACHED\0"))

// 读取确认
for {
    n, _ := syscall.Read(conn.fd, buf)
    if n > 0 && buf[n-1] == 0 {
        break
    }
}
```

### 错误处理

OpenJ9 特定的错误代码：

| 错误 | 含义 |
|------|------|
| `ATTACH_ACK` | 成功（load 命令） |
| `ATTACH_ERR AgentInitializationException N` | 代理初始化失败，代码为 N |
| 空响应 | 命令不受支持 |

## 限制

1. **Windows 支持**：jambo 中未实现 Windows 上的 OpenJ9 附加
2. **复杂性**：OpenJ9 的附加机制比 HotSpot 更复杂
3. **文件依赖**：需要 `/tmp/.com_ibm_tools_attach/` 中的正确文件权限

## 参考资料

### 官方文档

- [Eclipse OpenJ9 文档](https://www.eclipse.org/openj9/docs/)
- [OpenJ9 诊断工具](https://www.eclipse.org/openj9/docs/tool_jcmd/)

### 源代码

- [OpenJ9 Attach API](https://github.com/eclipse-openj9/openj9)
- [jattach OpenJ9 实现](https://github.com/jattach/jattach/blob/master/src/posix/jattach_openj9.c)

### 相关

- [Java Attach API 规范](https://docs.oracle.com/javase/8/docs/jdk/api/attach/spec/)
- [IBM J9 到 OpenJ9 迁移](https://www.eclipse.org/openj9/docs/introduction/)
