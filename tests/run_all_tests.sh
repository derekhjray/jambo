#!/bin/bash

# Comprehensive test script for jambo
# Tests both HotSpot and OpenJ9 JVMs on Linux

set -e

echo "==========================================="
echo "Jambo Comprehensive Functionality Test"
echo "==========================================="
echo

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Navigate to tests directory
cd "$(dirname "$0")"
TEST_DIR=$(pwd)
JAMBO_DIR=$(dirname "$TEST_DIR")

echo "Tests directory: $TEST_DIR"
echo "Jambo directory: $JAMBO_DIR"
echo

# Step 1: Build jambo
echo "${YELLOW}Step 1: Building jambo...${NC}"
cd "$JAMBO_DIR"
CGO_ENABLED=0 go build -o jambo ./cmd/jambo
if [ $? -eq 0 ]; then
    echo "${GREEN}✓ jambo built successfully${NC}"
else
    echo "${RED}✗ Failed to build jambo${NC}"
    exit 1
fi
echo

# Test results storage
TOTAL_TESTS=0
TOTAL_PASSED=0
TOTAL_FAILED=0

# Helper function to run test
run_test() {
    local container_name="$1"
    local test_name="$2"
    local command="$3"
    local java_pid="$4"
    
    echo "  📋 Test: $test_name"
    echo "     Command: jambo $java_pid $command"
    
    # Run command with proper error handling
    local output_file="/tmp/jambo_test_output_${container_name}_$(date +%s).txt"
    if docker exec "$container_name" /tmp/jambo $java_pid $command >"$output_file" 2>&1; then
        echo "${GREEN}     ✓ PASSED${NC}"
        head -n 5 "$output_file" | sed 's/^/     | /'
        TOTAL_PASSED=$((TOTAL_PASSED + 1))
        rm -f "$output_file"
        return 0
    else
        local exit_code=$?
        echo "${RED}     ✗ FAILED (exit code: $exit_code)${NC}"
        cat "$output_file" | sed 's/^/     | /'
        TOTAL_FAILED=$((TOTAL_FAILED + 1))
        rm -f "$output_file"
        return 1
    fi
}

# Function to test a JVM
test_jvm() {
    local jvm_type="$1"
    local dockerfile="$2"
    local container_name="$3"
    local image_name="$4"
    
    echo "${BLUE}========================================${NC}"
    echo "${BLUE}Testing $jvm_type JVM${NC}"
    echo "${BLUE}========================================${NC}"
    echo
    
    # Build Docker image
    echo "${YELLOW}Building Docker image for $jvm_type...${NC}"
    cd "$TEST_DIR"
    docker build -f "$dockerfile" -t "$image_name" .
    if [ $? -ne 0 ]; then
        echo "${RED}✗ Failed to build Docker image for $jvm_type${NC}"
        return 1
    fi
    echo "${GREEN}✓ Docker image built successfully${NC}"
    echo
    
    # Start container
    echo "${YELLOW}Starting test container...${NC}"
    
    # Clean up any existing container with the same name
    if docker ps -a --format '{{.Names}}' | grep -q "^${container_name}$"; then
        echo "  Removing existing container: $container_name"
        docker stop "$container_name" >/dev/null 2>&1 || true
        docker rm "$container_name" >/dev/null 2>&1 || true
    fi
    
    CONTAINER_ID=$(docker run -d --name "$container_name" "$image_name")
    if [ $? -ne 0 ]; then
        echo "${RED}✗ Failed to start container${NC}"
        return 1
    fi
    echo "${GREEN}✓ Container started: $CONTAINER_ID${NC}"
    
    # Wait for container to initialize and check if it's running
    sleep 3
    if ! docker ps --format '{{.Names}}' | grep -q "^${container_name}$"; then
        echo "${RED}✗ Container is not running${NC}"
        docker logs "$container_name" 2>&1 | tail -20
        return 1
    fi
    echo
    
    # Get Java PID
    echo "${YELLOW}Finding Java process PID...${NC}"
    JAVA_PID=$(docker exec "$container_name" sh -c "ps aux 2>/dev/null | grep 'java TestApp' | grep -v grep | awk '{print \$2}' | head -n 1" 2>/dev/null || \
               docker exec "$container_name" sh -c "ps -ef 2>/dev/null | grep 'java TestApp' | grep -v grep | awk '{print \$2}' | head -n 1" 2>/dev/null || \
               echo "1")  # Fallback to PID 1 if ps not available
    if [ -z "$JAVA_PID" ] || [ "$JAVA_PID" = "1" ]; then
        # Try to use PID 1 directly (Java is usually PID 1 in containers)
        JAVA_PID="1"
    fi
    echo "${GREEN}✓ Java PID: $JAVA_PID${NC}"
    
    # Detect JVM type
    echo "${YELLOW}Detecting JVM type...${NC}"
    JVM_VERSION=$(docker exec "$container_name" java -version 2>&1)
    echo "$JVM_VERSION" | head -3 | sed 's/^/  /'
    echo
    
    # Copy jambo to container
    docker cp "$JAMBO_DIR/jambo" "$container_name":/tmp/jambo >/dev/null 2>&1
    
    # Run tests
    echo "${YELLOW}Running tests...${NC}"
    echo
    
    local tests_before=$TOTAL_TESTS
    
    # Test 1: threaddump
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    run_test "$container_name" "Thread Dump" "threaddump" "$JAVA_PID"
    echo
    
    # Test 2: properties
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    run_test "$container_name" "System Properties" "properties" "$JAVA_PID"
    echo
    
    # Test 3: jcmd VM.version
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    run_test "$container_name" "jcmd VM.version" "jcmd VM.version" "$JAVA_PID"
    echo
    
    # Test 4: jcmd VM.system_properties
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    run_test "$container_name" "jcmd VM.system_properties" "jcmd VM.system_properties" "$JAVA_PID"
    echo
    
    # Test 5: jcmd GC.heap_info
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    run_test "$container_name" "jcmd GC.heap_info" "jcmd GC.heap_info" "$JAVA_PID"
    echo
    
    # Test 6: jcmd Thread.print
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    run_test "$container_name" "jcmd Thread.print" "jcmd Thread.print" "$JAVA_PID"
    echo
    
    # Test 7: jcmd VM.flags
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    run_test "$container_name" "jcmd VM.flags" "jcmd VM.flags" "$JAVA_PID"
    echo
    
    # Test 8: jcmd help
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    run_test "$container_name" "jcmd help" "jcmd help" "$JAVA_PID"
    echo
    
    # Test 9: jcmd VM.uptime
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    run_test "$container_name" "jcmd VM.uptime" "jcmd VM.uptime" "$JAVA_PID"
    echo
    
    # Test 10: jcmd VM.command_line
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    run_test "$container_name" "jcmd VM.command_line" "jcmd VM.command_line" "$JAVA_PID"
    echo
    
    local tests_after=$TOTAL_TESTS
    local jvm_passed=$((tests_after - tests_before - (TOTAL_FAILED - (TOTAL_TESTS - TOTAL_PASSED - TOTAL_FAILED))))
    
    # Cleanup
    echo "${YELLOW}Cleaning up...${NC}"
    docker stop "$container_name" >/dev/null 2>&1
    docker rm "$container_name" >/dev/null 2>&1
    echo "${GREEN}✓ Container removed${NC}"
    echo
    
    # JVM-specific summary
    echo "${BLUE}$jvm_type Test Summary:${NC}"
    local jvm_tests=$((tests_after - tests_before))
    local jvm_failed=$((jvm_tests - jvm_passed))
    echo "  Tests run: $jvm_tests"
    echo "  ${GREEN}Passed: $jvm_passed${NC}"
    if [ $jvm_failed -gt 0 ]; then
        echo "  ${RED}Failed: $jvm_failed${NC}"
    else
        echo "  Failed: $jvm_failed"
    fi
    echo
}

# Test HotSpot JVM
test_jvm "HotSpot" "Dockerfile.hotspot" "jambo-test-hotspot" "jambo-test-hotspot:latest"

# Test OpenJ9 JVM
test_jvm "OpenJ9" "Dockerfile.openj9" "jambo-test-openj9" "jambo-test-openj9:latest"

# Final Summary
echo "==========================================="
echo "Final Test Summary"
echo "==========================================="
echo "Total tests: $TOTAL_TESTS"
echo "${GREEN}Passed: $TOTAL_PASSED${NC}"
if [ $TOTAL_FAILED -gt 0 ]; then
    echo "${RED}Failed: $TOTAL_FAILED${NC}"
    echo "${RED}Success rate: $(awk "BEGIN {printf \"%.1f%%\", ($TOTAL_PASSED/$TOTAL_TESTS)*100}")${NC}"
else
    echo "Failed: $TOTAL_FAILED"
    echo "${GREEN}Success rate: 100.0%${NC}"
fi
echo

if [ $TOTAL_FAILED -eq 0 ]; then
    echo "${GREEN}✓ All tests passed!${NC}"
    echo
    echo "Test coverage:"
    echo "  - HotSpot JVM: ✓ Fully tested"
    echo "  - OpenJ9 JVM:  ✓ Fully tested"
    echo "  - Platform:    Linux (Docker containers)"
    echo "  - Total Tests: $TOTAL_TESTS"
    exit 0
else
    echo "${RED}✗ Some tests failed${NC}"
    echo "${YELLOW}Please check the error messages above for details${NC}"
    exit 1
fi
