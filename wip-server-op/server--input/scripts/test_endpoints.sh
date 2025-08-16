#!/bin/bash

# Set the API server URL
API_URL="http://localhost:10001"

# Colors for output
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[0;33m'
NC='\033[0m' # No Color

# Function to print test title
print_test() {
  echo -e "\n${YELLOW}======== Testing $1 ========${NC}"
}

# Function to check if command was successful
check_result() {
  if [ $? -eq 0 ]; then
    echo -e "${GREEN}✓ Success${NC}"
  else
    echo -e "${RED}✗ Failed${NC}"
  fi
}

# Health check
print_test "Health Check"
curl -s "${API_URL}/health" | jq
check_result

# Mouse Move
print_test "Mouse Move"
curl -s -X POST "${API_URL}/input/mouse/move" \
  -H "Content-Type: application/json" \
  -d '{"x": 500, "y": 500}' | jq
check_result

# Mouse Move Relative
print_test "Mouse Move Relative"
curl -s -X POST "${API_URL}/input/mouse/move_relative" \
  -H "Content-Type: application/json" \
  -d '{"dx": 50, "dy": 50}' | jq
check_result

# Mouse Click
print_test "Mouse Click"
curl -s -X POST "${API_URL}/input/mouse/click" \
  -H "Content-Type: application/json" \
  -d '{"button": "left", "count": 1}' | jq
check_result

# Mouse Down and Up
print_test "Mouse Down"
curl -s -X POST "${API_URL}/input/mouse/down" \
  -H "Content-Type: application/json" \
  -d '{"button": "left"}' | jq
check_result

print_test "Mouse Up"
curl -s -X POST "${API_URL}/input/mouse/up" \
  -H "Content-Type: application/json" \
  -d '{"button": "left"}' | jq
check_result

# Mouse Scroll
print_test "Mouse Scroll"
curl -s -X POST "${API_URL}/input/mouse/scroll" \
  -H "Content-Type: application/json" \
  -d '{"dx": 0, "dy": -120}' | jq
check_result

# Mouse Location
print_test "Mouse Location"
curl -s "${API_URL}/input/mouse/location" | jq
check_result

# Keyboard Type
print_test "Keyboard Type"
curl -s -X POST "${API_URL}/input/keyboard/type" \
  -H "Content-Type: application/json" \
  -d '{"text": "Hello, World!", "wpm": 300, "enter": true}' | jq
check_result

# Keyboard Key
print_test "Keyboard Key"
curl -s -X POST "${API_URL}/input/keyboard/key" \
  -H "Content-Type: application/json" \
  -d '{"keys": ["a", "b", "c"]}' | jq
check_result

# Display Geometry
print_test "Display Geometry"
curl -s "${API_URL}/input/display/geometry" | jq
check_result

# Window Active
print_test "Window Active"
curl -s "${API_URL}/input/window/active" | jq
check_result

# Window Focused
print_test "Window Focused"
curl -s "${API_URL}/input/window/focused" | jq
check_result

# Get Window Name
print_test "Window Name"
# First get active window
ACTIVE_WINDOW=$(curl -s "${API_URL}/input/window/active" | jq -r '.wid')
curl -s -X POST "${API_URL}/input/window/name" \
  -H "Content-Type: application/json" \
  -d "{\"wid\": \"$ACTIVE_WINDOW\"}" | jq
check_result

echo -e "\n${GREEN}All tests completed!${NC}"