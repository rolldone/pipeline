# Agent Util Package

This package provides utility functions for the sync agent, including a printer utility for clean terminal output.

## Features

- **Printer Utility**: Thread-safe printing with line clearing capabilities
- **ClearLine()**: Clears the current terminal line before printing
- **Printf()**: Formatted printing with automatic line clearing
- **Println()**: Line printing with automatic line clearing

## Usage

```go
import "sync-agent/internal/util"

// Print with automatic line clearing
util.Default.Printf("✅ Operation completed: %s\n", result)

// Clear line manually
util.Default.ClearLine()
util.Default.Printf("🔄 Processing...\n")
```

## Functions

- `util.Default.Printf(format string, a ...interface{})` - Print formatted text with line clearing
- `util.Default.Println(a ...interface{})` - Print line with line clearing  
- `util.Default.ClearLine()` - Clear current terminal line
- `util.Default.PrintBlock(block string, clearLine bool)` - Print multi-line block
- `util.Default.ClearScreen()` - Clear entire screen
- `util.Default.Suspend()` / `util.Default.Resume()` - Control printing suspension
