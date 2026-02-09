# jambo

[![Go Reference](https://pkg.go.dev/badge/github.com/cosmorse/jambo.svg)](https://pkg.go.dev/github.com/cosmorse/jambo)
[![Go Report Card](https://goreportcard.com/badge/github.com/cosmorse/jambo)](https://goreportcard.com/report/github.com/cosmorse/jambo)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)
[![GitHub release](https://img.shields.io/github/release/cosmorse/jambo.svg)](https://github.com/cosmorse/jambo/releases)
[![GitHub issues](https://img.shields.io/github/issues/cosmorse/jambo.svg)](https://github.com/cosmorse/jambo/issues)

### JVM 动态附加工具（Go 版本）

JVM 动态附加机制的 Go 语言实现，灵感来源于 [jattach](https://github.com/jattach/jattach)。

## 特性

- **Go API**：可作为库在您的 Go 应用程序中使用
- **命令行工具**：类似 jattach 的命令行界面
- **跨平台支持**：
  - ✅ Linux（完整支持，包含命名空间/容器支持）
  - ✅ Windows（通过远程线程注入支持 HotSpot）
  - ⚠️ 其他平台（基础存根实现）
- **JVM 支持**：
  - ✅ HotSpot JVM（Linux 和 Windows）
  - ✅ OpenJ9 JVM（仅 Linux）
- **容器支持**：Linux 容器命名空间支持（net、ipc、mnt、pid）
- **完善的文档**：技术细节请参阅 [文档](#文档) 章节

## 兼容性

### HotSpot JVM

| JDK 版本 | 状态 | 说明 |
|---------|------|------|
| JDK 6+ | ✅ 支持 | 初始附加协议 |
| JDK 8 | ✅ 支持 | 增强的代理加载 |
| JDK 9+ | ✅ 支持 | 增强的加载命令响应 |
| JDK 11+ | ✅ 支持 | LTS 版本，已完整测试 |
| JDK 17+ | ✅ 支持 | LTS 版本，已完整测试 |
| JDK 21+ | ✅ 支持 | LTS 版本，加载错误报告变更 |

**最低版本**：JDK 6  
**推荐版本**：JDK 8 或更高

### OpenJ9 JVM

| JDK 版本 | 状态 | 说明 |
|---------|------|------|
| JDK 8+ | ✅ 支持 | 需要 `-Dcom.ibm.tools.attach.enable=yes` |
| JDK 11+ | ✅ 支持 | LTS 版本，已完整测试 |
| JDK 17+ | ✅ 支持 | LTS 版本 |
| JDK 21+ | ✅ 支持 | LTS 版本，已完整测试 |

**最低版本**：JDK 8（OpenJ9 虚拟机）  
**推荐版本**：JDK 11 或更高  
**必需 JVM 选项**：`-Dcom.ibm.tools.attach.enable=yes`

**注意**：OpenJ9 的附加机制与 HotSpot 不同 - 它使用 TCP 套接字和信号量，而非 Unix 域套接字。详见 [docs/OPENJ9.zh.md](docs/OPENJ9.zh.md)。

## 安装

### 作为库使用

```bash
go get github.com/cosmorse/jambo
```

### 从源码构建

```bash
git clone https://github.com/cosmorse/jambo
cd jambo
go build ./cmd/jambo
```

## 使用方法

### 命令行

```bash
jambo <pid> <cmd> [args ...]
```

### 可用命令

- **load**            : 加载代理库
- **properties**      : 打印系统属性
- **agentProperties** : 打印代理属性
- **datadump**        : 显示堆和线程摘要
- **threaddump**      : 转储所有堆栈跟踪（类似 jstack）
- **dumpheap**        : 转储堆（类似 jmap）
- **inspectheap**     : 堆直方图（类似 jmap -histo）
- **setflag**         : 修改可管理的 VM 标志
- **printflag**       : 打印 VM 标志
- **jcmd**            : 执行 jcmd 命令

### 示例

#### 加载 Java 代理

```bash
jambo <pid> load instrument false "javaagent.jar=arguments"
```

#### 列出可用的 jcmd 命令

```bash
jambo <pid> jcmd help -all
```

#### 获取线程转储

```bash
jambo <pid> threaddump
```

## Go API

### 基本用法

```go
package main

import (
    "fmt"
    "github.com/cosmorse/jambo"
)

func main() {
    pid := 12345
    
    // 简单附加
    output, err := jambo.Attach(pid, "threaddump", nil, true)
    if err != nil {
        fmt.Printf("错误: %v\n", err)
        return
    }
    fmt.Println(output)
    
    // 使用选项的高级用法
    proc, err := jambo.NewProcess(pid)
    if err != nil {
        fmt.Printf("错误: %v\n", err)
        return
    }
    
    opts := &jambo.Options{
        PrintOutput: true,
        Timeout:     5000,
    }
    
    output, err = proc.Attach("properties", nil, opts)
    if err != nil {
        fmt.Printf("错误: %v\n", err)
        return
    }
    fmt.Println(output)
}
```

### API 参考

#### 类型

```go
// Process 表示目标 JVM 进程
type Process struct {
    // 私有字段，通过方法访问：
    // Pid() int      - 进程 ID
    // Uid() int      - 用户 ID
    // Gid() int      - 组 ID
    // NsPid() int    - 命名空间 PID（用于容器）
    // JVM() JVM      - JVM 实现实例
}

// Options 配置附加操作行为
type Options struct {
    PrintOutput bool  // 将命令输出打印到 stdout
    Timeout     int   // 超时时间（毫秒）（0 = 无超时）
}

// JVMType 表示 JVM 实现类型
type JVMType int
const (
    HotSpot JVMType = iota  // Oracle HotSpot 或 OpenJDK
    OpenJ9                   // Eclipse OpenJ9（原 IBM J9）
    Unknown                  // 未知 JVM 类型
)
```

#### 函数

```go
// NewProcess 为给定 PID 创建新 Process，自动检测 JVM 类型
func NewProcess(pid int) (*Process, error)

// Attach 是快速附加操作的便捷函数
func Attach(pid int, command string, args []string, printOutput bool) (string, error)

// ParsePID 解析 PID 字符串（十进制或带 0x 前缀的十六进制）
func ParsePID(pidStr string) (int, error)
```

#### 方法

```go
// Process 方法
func (p *Process) Attach(command string, args []string, opts *Options) (string, error)
func (p *Process) Pid() int
func (p *Process) Uid() int
func (p *Process) Gid() int
func (p *Process) NsPid() int
func (p *Process) JVM() JVM
```

## 文档

详细技术文档请参阅：

- **[HotSpot 附加机制](docs/HOTSPOT.zh.md)** - HotSpot JVM 在 Linux 和 Windows 上的动态附加工作原理
- **[OpenJ9 附加机制](docs/OPENJ9.zh.md)** - OpenJ9 JVM 动态附加工作原理
- **[Windows Shellcode](docs/WINDOWS_SHELLCODE.zh.md)** - Windows 远程线程注入和 shellcode 生成工作原理

## 平台支持

### Linux
- 完整支持 HotSpot 和 OpenJ9 JVM
- 容器命名空间支持（net、ipc、mnt、pid）
- 进程凭据切换
- HotSpot 使用 Unix 域套接字
- OpenJ9 使用带信号量通知的 TCP/IP 套接字

### Windows
- 通过远程线程注入支持 HotSpot JVM
- 使用命名管道进行通信
- 需要管理员权限或 SeDebugPrivilege
- 技术细节请参阅 [Windows Shellcode 文档](docs/WINDOWS_SHELLCODE.zh.md)

### 其他平台
- 提供基础 API
- 由于平台限制，功能有限

## 限制

- **命名空间切换**：在 Linux 上需要 CAP_SYS_ADMIN 能力
- **容器支持**：在大多数容器环境中工作（Docker、Kubernetes 等）
- **Windows**：
  - 需要管理员权限或 SeDebugPrivilege
  - 位数必须匹配（32 位 jambo 用于 32 位 JVM，64 位用于 64 位）
  - 可能会被杀毒软件标记（由于远程线程注入）
- **Windows 上的 OpenJ9**：不支持（OpenJ9 附加机制差异显著）

## 构建

```bash
# 构建命令行工具
go build -o jambo ./cmd/jambo

# 为特定平台构建
GOOS=linux GOARCH=amd64 go build -o jambo-linux-amd64 ./cmd/jambo
GOOS=windows GOARCH=amd64 go build -o jambo-windows-amd64.exe ./cmd/jambo
```

## 平台特定说明

### Linux

- 完整支持 HotSpot 和 OpenJ9 JVM
- 容器命名空间支持（net、ipc、mnt、pid）
- 需要适当的权限才能附加到进程
- 使用 Unix 域套接字进行通信

### Windows

- 通过远程线程注入支持 HotSpot JVM
- 需要管理员权限或 SeDebugPrivilege 才能附加到其他进程
- 使用命名管道进行通信
- 位数必须匹配（32 位 jambo 用于 32 位 JVM，64 位用于 64 位）
- **注意**：远程线程注入技术可能会被杀毒软件标记

**Windows 错误代码**：
- `1001`：无法加载 JVM 模块（未找到 jvm.dll）
- `1002`：无法找到 JVM_EnqueueOperation 函数

### 环境变量

- `JAMBO_ATTACH_PATH`：覆盖附加文件的默认临时路径

## 测试

### 单元测试

```bash
# 运行测试
go test ./...

# 带覆盖率的测试
go test -cover ./...
```

### Docker 集成测试

我们使用 Docker 容器为不同 JVM 类型提供了完善的测试套件：

```bash
cd tests

# 仅测试 HotSpot（推荐）
./run_tests.sh

# 测试 HotSpot 和 OpenJ9
./run_all_tests.sh
```

#### 测试环境状态

| JVM 类型 | 状态 | 平台 | 测试覆盖率 | 生产就绪 |
|---------|------|------|-----------|---------|
| **HotSpot** | ✅ **工作中** | Linux (Docker) | 10/10 测试通过 | ✅ **是** |
| **OpenJ9** | ✅ **工作中** | Linux | 10/10 测试通过 | ✅ **是** |
| **Windows** | ❓ 未测试 | Windows | 仅代码审查 | ❓ **需要测试** |

## 许可证

Apache License 2.0

## 贡献

欢迎贡献！请随时提交 Pull Request。

## 技术参考

### 附加机制文档
- [HotSpot 附加协议](docs/HOTSPOT.zh.md) - HotSpot 附加机制的详细解释
- [OpenJ9 附加协议](docs/OPENJ9.zh.md) - OpenJ9 附加机制的详细解释
- [Windows Shellcode](docs/WINDOWS_SHELLCODE.zh.md) - 远程线程注入和 shellcode 生成

### 上游项目
- [jattach](https://github.com/jattach/jattach) - 启发本项目的原始 C 实现
- [OpenJDK HotSpot 源码](https://github.com/openjdk/jdk/blob/master/src/hotspot/os/linux/attachListener_linux.cpp) - 官方 HotSpot 附加监听器实现
- [Eclipse OpenJ9](https://github.com/eclipse-openj9/openj9) - OpenJ9 JVM 源代码

### API 文档
- [Go 包文档](https://pkg.go.dev/github.com/cosmorse/jambo) - 完整的 Go API 参考（godoc）
