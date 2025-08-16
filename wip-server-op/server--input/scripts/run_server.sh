#!/bin/bash

# Set environment variables
export DISPLAY=:20
export PORT=10001
export DISPLAY_NUM=20

# Change to the server--input directory
cd /home/raidendotai/neo/kernel-images/wip-server-op/server--input

# Build the server
echo "Building the server..."
make build

# Run the server
echo "Starting the server..."
./server