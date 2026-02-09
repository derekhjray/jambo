#!/bin/bash

# Test script for jambo functionality
# This script builds Docker container, runs test Java app, and tests various jambo commands

set -e

echo "==================================="
echo "Jambo Functionality Test"
echo "==================================="
echo

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
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
go build -o jambo ./cmd/jambo
if [ $? -eq 0 ]; then
    echo "${GREEN}✓ jambo built successfully${NC}"
else
    echo "${RED}✗ Failed to build jambo${NC}"
    exit 1
fi
echo

# Step 2: Build Docker image
echo "${YELLOW}Step 2: Building Docker image...${NC}"
cd "$TEST_DIR"
docker build -f Dockerfile.hotspot -t jambo-test:latest .
if [ $? -eq 0 ]; then
    echo "${GREEN}✓ Docker image built successfully${NC}"
else
    echo "${RED}✗ Failed to build Docker image${NC}"
    exit 1
fi
echo

# Step 3: Start container
echo "${YELLOW}Step 3: Starting test container...${NC}"

# Clean up any existing container
if docker ps -a --format '{{.Names}}' | grep -q "^jambo-test$"; then
    echo "  Removing existing container..."
    docker stop jambo-test >/dev/null 2>&1 || true
    docker rm jambo-test >/dev/null 2>&1 || true
fi

CONTAINER_ID=$(docker run -d --name jambo-test jambo-test:latest)
if [ $? -eq 0 ]; then
    echo "${GREEN}✓ Container started: $CONTAINER_ID${NC}"
else
    echo "${RED}✗ Failed to start container${NC}"
    exit 1
fi

# Wait for container to initialize
sleep 3

# Check if container is still running
if ! docker ps --format '{{.Names}}' | grep -q "^jambo-test$"; then
    echo "${RED}✗ Container is not running${NC}"
    echo "Container logs:"
    docker logs jambo-test 2>&1 | tail -20
    exit 1
fi
echo

# Step 4: Get Java PID
echo "${YELLOW}Step 4: Finding Java process PID...${NC}"
JAVA_PID=$(docker exec jambo-test pgrep -f "java TestApp" | head -n 1)
if [ -z "$JAVA_PID" ]; then
    echo "${RED}✗ Java process not found${NC}"
    docker stop jambo-test >/dev/null 2>&1
    docker rm jambo-test >/dev/null 2>&1
    exit 1
fi
echo "${GREEN}✓ Java PID: $JAVA_PID${NC}"
echo

# Step 5: Run tests
echo "${YELLOW}Step 5: Running jambo tests...${NC}"
echo "==================================="
echo

PASSED=0
FAILED=0

# Helper function to run test
run_test() {
    local test_name="$1"
    local command="$2"
    
    echo "📋 Test: $test_name"
    echo "   Command: $command"
    
    # Copy jambo to container
    docker cp "$JAMBO_DIR/jambo" jambo-test:/tmp/jambo >/dev/null 2>&1
    
    # Run command with better error handling
    local output_file="/tmp/jambo_test_output_$(date +%s).txt"
    if docker exec jambo-test /tmp/jambo $JAVA_PID $command >"$output_file" 2>&1; then
        echo "${GREEN}   ✓ PASSED${NC}"
        echo "   Output (first 5 lines):"
        head -n 5 "$output_file" | sed 's/^/   | /'
        PASSED=$((PASSED + 1))
        rm -f "$output_file"
    else
        local exit_code=$?
        echo "${RED}   ✗ FAILED (exit code: $exit_code)${NC}"
        echo "   Error:"
        cat "$output_file" | sed 's/^/   | /'
        FAILED=$((FAILED + 1))
        rm -f "$output_file"
    fi
    echo
}

# Test 1: threaddump
run_test "Thread Dump" "threaddump"

# Test 2: properties
run_test "System Properties" "properties"

# Test 3: jcmd VM.version
run_test "jcmd VM.version" "jcmd VM.version"

# Test 4: jcmd VM.system_properties (first 10 lines)
run_test "jcmd VM.system_properties" "jcmd VM.system_properties"

# Test 5: jcmd GC.heap_info
run_test "jcmd GC.heap_info" "jcmd GC.heap_info"

# Test 6: jcmd Thread.print
run_test "jcmd Thread.print" "jcmd Thread.print"

# Test 7: jcmd VM.flags
run_test "jcmd VM.flags" "jcmd VM.flags"

# Test 8: jcmd help
run_test "jcmd help" "jcmd help"

# Test 9: jcmd VM.uptime
run_test "jcmd VM.uptime" "jcmd VM.uptime"

# Test 10: jcmd VM.command_line
run_test "jcmd VM.command_line" "jcmd VM.command_line"

# Step 6: Cleanup
echo "==================================="
echo "${YELLOW}Step 6: Cleaning up...${NC}"
docker stop jambo-test >/dev/null 2>&1
docker rm jambo-test >/dev/null 2>&1
echo "${GREEN}✓ Container removed${NC}"
echo

# Summary
echo "==================================="
echo "Test Summary"
echo "==================================="
TOTAL=$((PASSED + FAILED))
echo "Total tests: $TOTAL"
echo "${GREEN}Passed: $PASSED${NC}"
if [ $FAILED -gt 0 ]; then
    echo "${RED}Failed: $FAILED${NC}"
    echo "${RED}Success rate: $(awk "BEGIN {printf \"%.1f%%\", ($PASSED/$TOTAL)*100}")${NC}"
else
    echo "Failed: $FAILED"
    echo "${GREEN}Success rate: 100.0%${NC}"
fi
echo

if [ $FAILED -eq 0 ]; then
    echo "${GREEN}✓ All tests passed!${NC}"
    echo
    echo "Test details:"
    echo "  - JVM Type:    HotSpot"
    echo "  - Platform:    Linux (Docker)"
    echo "  - Total Tests: $TOTAL"
    exit 0
else
    echo "${RED}✗ Some tests failed${NC}"
    echo "${YELLOW}Please check the error messages above for details${NC}"
    exit 1
fi
