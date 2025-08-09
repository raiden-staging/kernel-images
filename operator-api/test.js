#!/usr/bin/env node

import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import { existsSync } from 'node:fs';

const __dirname = dirname(fileURLToPath(import.meta.url));

// Parse command line arguments
const args = process.argv.slice(2);
const testFiles = [];
const vitestArgs = [];

// Process arguments
args.forEach(arg => {
  if (arg.startsWith('--') || arg.startsWith('-')) {
    vitestArgs.push(arg);
  } else {
    testFiles.push(arg);
  }
});

// If no test files specified, run all tests
if (testFiles.length === 0) {
  console.log('Running all tests...');
} else {
  console.log(`Running tests: ${testFiles.join(', ')}`);
}

// Prepare test file paths
const testPaths = testFiles.map(file => {
  // If file doesn't end with .test.js, add it
  const testFile = file.endsWith('.test.js') ? file : `${file}.test.js`;
  return join(__dirname, 'tests', testFile);
}).filter(path => {
  const exists = existsSync(path);
  if (!exists) {
    console.warn(`Warning: Test file not found: ${path}`);
  }
  return exists;
});

// Build vitest command
const command = 'npx';
const commandArgs = [
  'vitest',
  'run',
  ...(testPaths.length > 0 ? testPaths : []),
  ...vitestArgs
];

// Run the tests
const testProcess = spawn(command, commandArgs, {
  stdio: 'inherit',
  shell: true
});

testProcess.on('close', code => {
  process.exit(code);
});

/*
Examples of how to use this test runner:

1. Run all tests:
   node test.js

2. Run a specific test file:
   node test.js screenshot

3. Run multiple test files:
   node test.js screenshot stream

4. Run with vitest options:
   node test.js screenshot --watch

5. Run with both file and options:
   node test.js screenshot --bail
*/
