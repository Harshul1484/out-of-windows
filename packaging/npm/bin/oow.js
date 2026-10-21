#!/usr/bin/env node
'use strict';

// Starts the oow.exe built for this machine. The executables are the GitHub
// release's own files, checked against its SHA256SUMS and build-provenance
// attestations before the package was published.
const { spawn } = require('node:child_process');
const path = require('node:path');

const arch = { x64: 'win32-x64', arm64: 'win32-arm64' }[process.arch];
if (process.platform !== 'win32' || !arch) {
  console.error(`oow runs on Windows x64 and ARM64 only (this is ${process.platform}/${process.arch}).`);
  process.exit(1);
}

const exe = path.join(__dirname, '..', 'vendor', arch, 'oow.exe');
const child = spawn(exe, process.argv.slice(2), { stdio: 'inherit' });

// Ctrl+C reaches oow through the shared console; oow stops after the current
// item and reports what changed. Exiting first would cut that report short.
for (const signal of ['SIGINT', 'SIGBREAK']) {
  process.on(signal, () => {});
}

child.on('error', (err) => {
  console.error(`oow: cannot start ${exe}: ${err.message}`);
  process.exit(1);
});
child.on('exit', (code) => process.exit(code ?? 1));
