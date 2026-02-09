# Jambo Docker Integration Tests

This directory contains comprehensive Docker-based integration tests for jambo functionality.

## Files

- `Dockerfile.hotspot` - Docker image definition for HotSpot JVM (OpenJDK)
- `Dockerfile.openj9` - Docker image definition for OpenJ9 JVM (IBM Semeru Runtime)
- `TestApp.java` - Simple Java application for testing attach functionality
- `run_tests.sh` - Quick test script for HotSpot JVM only
- `run_all_tests.sh` - Comprehensive test script for both HotSpot and OpenJ9 JVMs

## Quick Start

### Test HotSpot JVM Only (Recommended for Quick Testing)

```bash
cd tests
./run_tests.sh
```

### Test Both HotSpot and OpenJ9 JVMs (Comprehensive)

```bash
cd tests
./run_all_tests.sh
```

## Test Environment

### HotSpot Configuration
- **Base Image**: OpenJDK (latest)
- **JVM Type**: HotSpot
- **OS**: Oracle Linux
- **Status**: ✅ Fully Working

### OpenJ9 Configuration
- **Base Image**: IBM Semeru Runtime (OpenJ9 21 JDK)
- **JVM Type**: Eclipse OpenJ9
- **OS**: Ubuntu (Jammy)
- **Special Features**: Attach mechanism enabled via JVM options
- **Status**: ✅ Fully Working

## Tests Included

1. **Thread Dump** - `threaddump` command
2. **System Properties** - `properties` command
3. **JVM Version** - `jcmd VM.version`
4. **System Properties (jcmd)** - `jcmd VM.system_properties`
5. **Heap Info** - `jcmd GC.heap_info`
6. **Thread Print** - `jcmd Thread.print`
7. **VM Flags** - `jcmd VM.flags`
8. **Help** - `jcmd help`
9. **VM Uptime** - `jcmd VM.uptime`
10. **VM Command Line** - `jcmd VM.command_line`

## Manual Testing

If you want to run tests manually:

### HotSpot JVM Testing

```bash
# 1. Build Docker image
docker build -f Dockerfile.hotspot -t jambo-test-hotspot .

# 2. Start container
docker run -d --name jambo-test-hotspot jambo-test-hotspot

# 3. Get Java PID
JAVA_PID=$(docker exec jambo-test-hotspot pgrep -f "java TestApp")
echo "Java PID: $JAVA_PID"

# 4. Build jambo (from parent directory)
cd ..
go build -o jambo ./cmd/jambo

# 5. Copy jambo to container
docker cp jambo jambo-test-hotspot:/tmp/jambo

# 6. Run specific test
docker exec jambo-test-hotspot /tmp/jambo $JAVA_PID threaddump

# 7. Run jcmd command
docker exec jambo-test-hotspot /tmp/jambo $JAVA_PID jcmd VM.version

# 8. Cleanup
docker stop jambo-test-hotspot
docker rm jambo-test-hotspot
```

### OpenJ9 JVM Testing

```bash
# 1. Build Docker image
docker build -f Dockerfile.openj9 -t jambo-test-openj9 .

# 2. Start container with attach enabled
docker run -d --name jambo-test-openj9 jambo-test-openj9 \
  java -Dcom.ibm.tools.attach.enable=yes -Dcom.ibm.tools.attach.timeout=30000 TestApp

# 3. Get Java PID (usually PID 1 in containers)
JAVA_PID=1

# 4. Copy jambo to container
docker cp ../jambo jambo-test-openj9:/tmp/jambo

# 5. Run specific test
docker exec jambo-test-openj9 /tmp/jambo $JAVA_PID threaddump

# 6. Cleanup
docker stop jambo-test-openj9
docker rm jambo-test-openj9
```

**Note**: OpenJ9 requires `-Dcom.ibm.tools.attach.enable=yes` to enable attach mechanism.

## Expected Results

### HotSpot JVM
All 10 tests should pass successfully. The output will show:

```
✓ All tests passed!
Total tests: 10
Passed: 10
Failed: 0
Success rate: 100.0%
```

### OpenJ9 JVM
All 10 tests should pass successfully. The output will show:

```
✓ All tests passed!
Total tests: 10
Passed: 10
Failed: 0
Success rate: 100.0%
```

## Troubleshooting

### Container already exists
```bash
docker stop jambo-test 2>/dev/null
docker rm jambo-test 2>/dev/null
```

### Cannot find Java process
Make sure the container is running and the Java application has started:
```bash
docker logs jambo-test
docker exec jambo-test ps aux | grep java
```

### Permission issues
jambo needs to attach to the Java process, which requires appropriate permissions. The tests copy jambo into the container to run with the same user context.

## Adding New Tests

To add a new test, edit `run_tests.sh` and add:

```bash
run_test "Your Test Name" "command args"
```

For example:
```bash
run_test "Heap Histogram" "jcmd GC.class_histogram"
```
