# Jambo Docker 集成测试

本目录包含 jambo 功能的完整 Docker 集成测试。

## 文件

- `Dockerfile.hotspot` - HotSpot JVM (OpenJDK) 的 Docker 镜像定义
- `Dockerfile.openj9` - OpenJ9 JVM (IBM Semeru Runtime) 的 Docker 镜像定义
- `TestApp.java` - 用于测试附加功能的简单 Java 应用程序
- `run_tests.sh` - 仅针对 HotSpot JVM 的快速测试脚本
- `run_all_tests.sh` - 针对 HotSpot 和 OpenJ9 JVM 的完整测试脚本

## 快速开始

### 仅测试 HotSpot JVM（推荐用于快速测试）

```bash
cd tests
./run_tests.sh
```

### 测试 HotSpot 和 OpenJ9 JVM（完整测试）

```bash
cd tests
./run_all_tests.sh
```

## 测试环境

### HotSpot 配置
- **基础镜像**：OpenJDK (latest)
- **JVM 类型**：HotSpot
- **操作系统**：Oracle Linux
- **状态**：✅ 完全工作

### OpenJ9 配置
- **基础镜像**：IBM Semeru Runtime (OpenJ9 21 JDK)
- **JVM 类型**：Eclipse OpenJ9
- **操作系统**：Ubuntu (Jammy)
- **特殊功能**：通过 JVM 选项启用附加机制
- **状态**：✅ 完全工作

## 包含的测试

1. **线程转储** - `threaddump` 命令
2. **系统属性** - `properties` 命令
3. **JVM 版本** - `jcmd VM.version`
4. **系统属性 (jcmd)** - `jcmd VM.system_properties`
5. **堆信息** - `jcmd GC.heap_info`
6. **线程打印** - `jcmd Thread.print`
7. **VM 标志** - `jcmd VM.flags`
8. **帮助** - `jcmd help`
9. **VM 运行时间** - `jcmd VM.uptime`
10. **VM 命令行** - `jcmd VM.command_line`

## 手动测试

如果您想手动运行测试：

### HotSpot JVM 测试

```bash
# 1. 构建 Docker 镜像
docker build -f Dockerfile.hotspot -t jambo-test-hotspot .

# 2. 启动容器
docker run -d --name jambo-test-hotspot jambo-test-hotspot

# 3. 获取 Java PID
JAVA_PID=$(docker exec jambo-test-hotspot pgrep -f "java TestApp")
echo "Java PID: $JAVA_PID"

# 4. 构建 jambo（从父目录）
cd ..
go build -o jambo ./cmd/jambo

# 5. 将 jambo 复制到容器
docker cp jambo jambo-test-hotspot:/tmp/jambo

# 6. 运行特定测试
docker exec jambo-test-hotspot /tmp/jambo $JAVA_PID threaddump

# 7. 运行 jcmd 命令
docker exec jambo-test-hotspot /tmp/jambo $JAVA_PID jcmd VM.version

# 8. 清理
docker stop jambo-test-hotspot
docker rm jambo-test-hotspot
```

### OpenJ9 JVM 测试

```bash
# 1. 构建 Docker 镜像
docker build -f Dockerfile.openj9 -t jambo-test-openj9 .

# 2. 启动容器并启用附加机制
docker run -d --name jambo-test-openj9 jambo-test-openj9 \
  java -Dcom.ibm.tools.attach.enable=yes -Dcom.ibm.tools.attach.timeout=30000 TestApp

# 3. 获取 Java PID（通常在容器中为 PID 1）
JAVA_PID=1

# 4. 将 jambo 复制到容器
docker cp ../jambo jambo-test-openj9:/tmp/jambo

# 5. 运行特定测试
docker exec jambo-test-openj9 /tmp/jambo $JAVA_PID threaddump

# 6. 清理
docker stop jambo-test-openj9
docker rm jambo-test-openj9
```

**注意**：OpenJ9 需要 `-Dcom.ibm.tools.attach.enable=yes` 来启用附加机制。

## 预期结果

### HotSpot JVM
所有 10 个测试应成功通过。输出将显示：

```
✓ All tests passed!
Total tests: 10
Passed: 10
Failed: 0
Success rate: 100.0%
```

### OpenJ9 JVM
所有 10 个测试应成功通过。输出将显示：

```
✓ All tests passed!
Total tests: 10
Passed: 10
Failed: 0
Success rate: 100.0%
```

## 故障排除

### 容器已存在
```bash
docker stop jambo-test 2>/dev/null
docker rm jambo-test 2>/dev/null
```

### 找不到 Java 进程
确保容器正在运行且 Java 应用程序已启动：
```bash
docker logs jambo-test
docker exec jambo-test ps aux | grep java
```

### 权限问题
jambo 需要附加到 Java 进程，这需要适当的权限。测试将 jambo 复制到容器中，以便使用相同的用户上下文运行。

## 添加新测试

要添加新测试，编辑 `run_tests.sh` 并添加：

```bash
run_test "您的测试名称" "命令参数"
```

例如：
```bash
run_test "堆直方图" "jcmd GC.class_histogram"
```
