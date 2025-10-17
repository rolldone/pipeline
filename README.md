# Pipeline

A standalone CLI tool for executing automated pipeline workflows with interactive menu support.

## Features

- **Interactive Menu**: Navigate pipeline executions and SSH connections with arrow keys
- **Job Dependencies**: Define dependencies between jobs for sequential execution
- **Variable Persistence**: Save command output to variables for subsequent jobs
- **SSH Execution**: Execute commands remotely via SSH
- **Real-time Output Streaming**: Display command output in real-time during execution
- **Pipeline Logging**: All output automatically saved to timestamped log files
- **Silent Mode**: Control output display per step for cleaner output
- **Timeout Control**: Dual timeout system (idle + total) for smarter execution control
- **Subroutine Calls**: Use `goto_job` for error handling
- **Template Generation**: Create Docker-focused pipeline templates
- **Variable Override**: Override variables via CLI with `--var` flag
- **Multi-Environment**: Support multiple environments with vars.yaml
- **Job Execution Tracking**: Visual indicators with HEAD markers for progress tracking
- **SSH Integration**: Direct server access through configured SSH commands
- **Configuration Management**: Flexible YAML-based configuration system

## Installation

Recommended: download official release assets (binary + sample YAML files) from the project's GitHub Releases.

If you need to build locally (portable binary) or create release artifacts, follow the options below.

### Build portable from source (clone + build)

Clone the repo and build a standalone binary. You can cross-compile for different OS/architectures using `GOOS`/`GOARCH`.

Example (Linux x86_64):

```bash
git clone https://github.com/rolldone/pipeline.git
cd pipeline
GOOS=linux GOARCH=amd64 go build -o pipeline ./
```

Example (macOS arm64):

```bash
GOOS=darwin GOARCH=arm64 go build -o pipeline-macos ./
```

Example (Windows amd64):

```bash
GOOS=windows GOARCH=amd64 go build -o pipeline.exe ./
```

To make the binary callable globally on Unix-like systems, create a symbolic link to a directory in PATH (may require sudo):

```bash
sudo ln -s $(pwd)/pipeline /usr/local/bin/pipeline
```

This preserves the original binary in your build directory while making it globally accessible. On Windows, place the `pipeline.exe` somewhere in your PATH or add its folder to the PATH environment variable.

### Releases and distribution (recommended for users)

- Publish release assets (per OS/arch) on GitHub Releases. Each release should include:
  - Compiled binaries for supported platforms (linux, darwin, windows, with relevant architectures).
  - `pipeline-sample.yaml` and `job-sample.yaml` so `pipeline init` works out-of-the-box.
  - A small README or install notes in the release description.
- Users download the correct asset for their OS, extract it, and move the binary to a directory in their PATH.

### About sample/supporting YAML files

This project expects certain sample files at runtime:

- `pipeline-sample.yaml` is used by `pipeline init` (the binary looks for it next to the executable).
- `job-sample.yaml` is used as the template for `pipeline create` when deriving job templates.

If you install only the binary (for example via `go install` or by building and copying the binary) and do not include the sample files, `pipeline init` will report `pipeline-sample.yaml not found` and ask you to place the sample file next to the binary or run the binary from the repository tree. Packaging releases that include these sample files prevents that issue for end users.

### Maintainer checklist before tagging a release

- Ensure `go.mod` module path is `github.com/rolldone/pipeline` so `go install github.com/rolldone/pipeline@<tag>` works for users who choose that flow.
- Confirm `main.go` exists at repository root and builds a single CLI binary.
- Build artifacts for supported OS/architectures and attach them to the release.
- Include `pipeline-sample.yaml` and `job-sample.yaml` in release assets (or embed them in the binary via `go:embed` if desired).
- Create a semantic version tag (vMAJOR.MINOR.PATCH) for the release.

### Releases and distribution (recommended for users)

- Publish release assets (per OS/arch) on GitHub Releases. Each release should include:
  - Compiled binaries for supported platforms (linux, darwin, windows, with relevant architectures).
  - `pipeline-sample.yaml` and `job-sample.yaml` so `pipeline init` works out-of-the-box.
  - A small README or install notes in the release description.
- Users download the correct asset for their OS, extract it, and move the binary to a directory in their PATH.
## Quick Start

Initialize a new pipeline project:

```bash
pipeline init
```

This creates `pipeline.yaml` configuration file and `pipelines/` directory.

Launch the interactive menu:

```bash
pipeline
```

Select from available pipeline executions and SSH connections using arrow keys.

## Configuration

Pipeline uses a `pipeline.yaml` configuration file. You can specify a custom config file:

```bash
pipeline --config custom.yaml list
```

### Config Structure

```yaml
project_name: my-project

var:
  auth:
    username: user
    host: example.com
    port: 22
    remotePath: /home/user

direct_access:
  pipeline_dir: ./pipelines
  executions:
    - name: "Deploy to Production"
      key: "prod-deploy"
      pipeline: "deploy.yaml"
      hosts: ["prod-server"]
      variables: {}
  ssh_configs:
    - Host: prod-server
      HostName: =var.auth.host
      User: =var.auth.username
      Port: =var.auth.port
      IdentityFile: ~/.ssh/id_rsa
  ssh_commands:
    - access_name: "Production Server"
      command: "ssh prod-server"
    - access_name: "Database Server"
      command: "ssh db-server"
```

### Configuration Sections

- **`var`**: Template variables for reuse across config
- **`direct_access.executions`**: Pipeline execution definitions
- **`direct_access.ssh_configs`**: SSH connection configurations
- **`direct_access.ssh_commands`**: Direct SSH access commands (shown in menu)

## Usage

```bash
pipeline [command]
```

When run without arguments, launches an interactive menu for selecting operations.

## Creating a New Pipeline

```bash
# Create a new pipeline with Docker-focused template
pipeline create my-app

# File created in ./pipelines/my-app.yaml
# Includes 5 jobs: prepare, build, test, deploy, rollback
```

The generated template includes:
- **Docker Integration**: Build, test, and deploy containerized applications
- **Variable Management**: Variables for tracking build information
- **Error Handling**: Rollback on failure
- **SSH Deployment**: Multi-host deployment support

## Pipeline Structure

```yaml
pipeline:
  name: "my-app"
  description: "Docker-based CI/CD pipeline"
  strict_variables: false

  # Global variables
  variables:
    DOCKER_REGISTRY: "docker.io"
    DOCKER_REPO: "your-org/my-app"
    DOCKER_TAG: "latest"

  jobs:
    - name: "prepare"
      mode: "local"        # Run locally
      steps:
        - name: "check-tools"
          type: "command"
          commands: ["docker --version"]

    - name: "build"
      mode: "local"        # Run locally
      depends_on: ["prepare"]
      steps:
        - name: "build-image"
          type: "command"
          commands: ["docker build -t {{DOCKER_REGISTRY}}/{{DOCKER_REPO}}:{{DOCKER_TAG}} ."]
          save_output: "image_id"

    - name: "deploy"
      mode: "remote"       # Run via SSH (default)
      depends_on: ["build"]
      steps:
        - name: "upload-files"
          type: "file_transfer"
          source: "dist/"
          destination: "/var/www/app/"
        - name: "restart-service"
          type: "command"
          commands: ["systemctl restart myapp"]
```

## Job Configuration

Jobs in pipelines can be configured with execution modes to determine whether steps run locally or remotely.

### Job Configuration Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `name` | string | - | Unique job identifier |
| `depends_on` | []string | - | Jobs that must complete before this job runs |
| `mode` | string | "remote" | Execution mode: `"local"` or `"remote"` |
| `steps` | []Step | - | Steps to execute in this job |

### Job Execution Modes

#### Local Mode (`mode: "local"`)
All steps in the job run locally on the machine running the pipeline:
- **Command steps**: Run commands in local shell
- **File transfer steps**: Copy files/directories locally (not upload via SCP)
- **Script steps**: Run local scripts

#### Remote Mode (`mode: "remote"`) - Default
All steps in the job run via SSH to remote hosts:
- **Command steps**: Run commands via SSH
- **File transfer steps**: Upload/download via SCP
- **Script steps**: Upload script to remote and execute via SSH

### Example Usage

```yaml
jobs:
  - name: "local-development"
    mode: "local"
    steps:
      - name: "install-deps"
        type: "command"
        commands: ["npm install"]
      - name: "copy-config"
        type: "file_transfer"
        source: "config/dev.yaml"
        destination: "dist/config.yaml"

  - name: "remote-production"
    mode: "remote"
    steps:
      - name: "deploy-app"
        type: "file_transfer"
        source: "dist/"
        destination: "/var/www/app/"
      - name: "restart-services"
        type: "command"
        commands: ["systemctl restart nginx", "systemctl restart app"]
```

## Step Configuration

Each step in a pipeline supports advanced configuration for detailed execution control:

### Basic Step Structure
```yaml
steps:
  - name: "build-app"
    type: "command"
    commands: ["npm run build"]
    description: "Build the application"
```

### Advanced Step Configuration
```yaml
steps:
  - name: "ping-test"
    type: "command"
    commands: ["ping -c 3 google.com"]
    description: "Test network connectivity"
    silent: true                    # Suppress real-time output display
    timeout: 30                     # Total timeout in seconds (default: 0 = unlimited)
    idle_timeout: 300               # Idle timeout in seconds (default: 600 = 10 minutes)
    working_dir: "/app"             # Override working directory
    save_output: "ping_result"      # Save command output to variable
    conditions:                     # Conditional execution
      - pattern: "5 packets"
        action: "continue"
```

### Step Configuration Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `name` | string | - | Unique step identifier |
| `type` | string | - | Step type: `command`, `file_transfer`, `script` |
| `commands` | []string | - | Commands to execute (for `command` type) |
| `file` | string | - | Script file path (for `script` type) |
| `source` | string | - | Source path (for `file_transfer` type) |
| `destination` | string | - | Destination path (for `file_transfer` type) |
| `direction` | string | "upload" | Transfer direction: `upload` or `download` |
| `template` | string | - | Template rendering: `"enabled"` to render `{{variables}}` in file content |
| `description` | string | - | Human-readable description |
| `silent` | bool | false | Suppress real-time output display |
| `timeout` | int | 0 | Total timeout in seconds (0 = unlimited) |
| `idle_timeout` | int | 600 | Idle timeout in seconds (10 minutes) - resets on output activity |
| `working_dir` | string | - | Override working directory for this step |
| `save_output` | string | - | Save command output to context variable |
| `conditions` | []Condition | - | Conditional execution based on output |
| `expect` | []Expect | - | Interactive prompt responses |

### Silent Mode
`silent: true` suppresses real-time output display per line, but output is still saved and can be displayed in subsequent steps:

```yaml
jobs:
  - name: "test"
    steps:
      - name: "run-tests"
        type: "command"
        commands: ["npm test"]
        silent: true              # No real-time output
        save_output: "test_output"

      - name: "show-results"
        type: "command"
        commands: ["echo 'Test Results: {{test_output}}'"]
        # Will display the full test output at once
```

### Timeout Configuration

Pipeline supports two timeout types for flexible execution control:

#### Total Timeout (`timeout`)
- **Default**: 0 (unlimited)
- **Behavior**: Total maximum execution time
- **Use case**: Prevent commands that are truly stuck from running too long

#### Idle Timeout (`idle_timeout`)
- **Default**: 600 seconds (10 minutes)
- **Behavior**: Maximum time without output activity - timer resets on new output
- **Use case**: Ideal for commands that provide progress feedback (download, build, etc.)

```yaml
steps:
  - name: "download-large-file"
    type: "command"
    commands: ["wget https://example.com/large-file.zip"]
    timeout: 3600        # Total timeout: 1 hour max
    idle_timeout: 300   # Idle timeout: 5 minutes (resets on download progress)

  - name: "compile-project"
    type: "command"
    commands: ["make all"]
    timeout: 0           # No total timeout limit
    idle_timeout: 600    # 10 minutes idle timeout (resets on compiler output)
```

**Timeout Logic**:
- Command will timeout if it exceeds **total timeout** OR has no output for **idle timeout**
- Idle timer automatically resets on each command output
- Total timeout 0 = unlimited (only idle timeout applies)

### Output Saving & Context Variables
Use `save_output` to save command results to variables usable in subsequent jobs/steps:

```yaml
steps:
  - name: "get-version"
    type: "command"
    commands: ["git rev-parse HEAD"]
    save_output: "commit_sha"

  - name: "build-image"
    type: "command"
    commands: ["docker build -t myapp:{{commit_sha}} ."]
```

### File Template Rendering (changed)

Content templating is no longer supported in `file_transfer` or `rsync_file` steps.

If you previously used `file_transfer` with `template: "enabled"` to render file contents, migrate to the `write_file` step which is designed to render content and then write or upload it. `write_file` renders using `{{var}}` interpolation and uploads rendered bytes from memory (default file mode 0755 when `perm` is omitted).

Example `write_file` usage (render then upload):

```yaml
jobs:
  - name: "deploy"
    steps:
      - name: "upload_config"
        type: "write_file"
        files:
          - source: "config.php"
            destination: "/var/www/config.php"
            template: "enabled"
```

### Transfer per-file with `files` (recommended for many files)

When you need to upload or download multiple files with different targets, use the `files` array within a `file_transfer` step. Each entry can have its own `source`, optional `destination`, and optional `template` as override. If `files` exists, it takes precedence over `source`/`sources`.

Example:

```yaml
- name: "upload-files"
  type: "file_transfer"
  files:
    - source: "xxx.txt"
      destination: "/xxx/xxx/xxx.txt"
    - source: "vvv.txt"
      destination: "/xxx/xxx/vvv.txt"
    - source: "dist/"
      destination: "/xxx/dist/"
```

### write_file step (render then write/upload)

A new `write_file` step renders file contents (when templating is enabled) and then writes them to the target(s). It is intended for cases where you want the executor to perform rendering and then either write files locally or upload rendered bytes to remote hosts without creating persistent temp files on disk of the remote side.

Key points:
- Renders file contents using the same `{{var}}` interpolation logic when `template: "enabled"` is set at the step or per-file entry.
- Uploads rendered content from memory (no temporary intermediate remote file required) using the in-memory uploader. This minimizes local disk churn and avoids leaving partially rendered files on the remote host.
- Default file permission: when a per-file `perm` is not specified, the executor uses `0755` as the default mode for written/uploaded files.
- Tolerant rendering: if the executor cannot stage a rendered file (for example, due to a local write error when preparing staged copies), it will fall back to using the original file bytes and record a `render_warnings` entry in the step's saved output (if `save_output` is set).
- `save_output` for `write_file` stores a concise JSON summary with fields: `exit_code`, `reason`, `duration_seconds`, and `files_written`. If render fallbacks occurred, a `render_warnings` array is also included.

Example (render locally then upload rendered bytes to remote):

```yaml
- name: "deploy-config"
  type: "write_file"
  files:
    - source: "config/app.conf"
      destination: "/etc/myapp/app.conf"
      template: "enabled"
  mode: "remote"        # or "local" to write on the runner
  save_output: "write_summary"
```

Notes:
- Use `files` for per-file destination and `perm` control. If `perm` is omitted the step applies `0755` by default.
- This step is useful when you need rendered configuration files deployed directly (for example service unit files, config templates) and want the rendering step consolidated in the pipeline executor rather than using separate local preprocessing.


Behavior:
- Per-file `destination` overrides step-level `destination`. If neither is set for an entry, the step will error.
- Glob patterns in `source` are expanded locally for upload operations; each match result is transferred while preserving the relative path under the destination directory.
- Per-file `template` overrides step-level `template` setting.
- If multiple files are specified, `destination` must be a directory (ending with `/` or already a directory) — otherwise the step will error.
- Backward compatibility: if `files` is not provided, `source` / `sources` will work as before.

## Rsync-based file transfer step (`rsync_file`)

This project supports an `rsync_file` step that wraps the system `rsync` binary. It provides more advanced include/exclude, delete, and performance options compared to the built-in `file_transfer` step.

Important notes:
- Requires `rsync` installed on the machine running the pipeline and on remote hosts when running in `remote` mode.
- The step uses the existing SSH configuration created under `.sync_temp/.ssh/config` so rsync remote invocations use `-e "ssh -F .sync_temp/.ssh/config"`.
- `rsync_file` does not render file contents. If you previously relied on templating before rsync, pre-render files using `write_file` into a temporary directory and rsync that directory.

Example:

```yaml
- name: "sync-assets"
  type: "rsync_file"
  includes: ["*/", "*.js", "*.css"]
  excludes: ["*.map"]
  options: ["--progress"]
  delete_policy: "soft"       # soft (default) or force
  confirm_force: false         # must be true when delete_policy: force
  prune_empty_dirs: true
  compress: true
  dry_run: false
  direction: "upload"
  source: "dist/"
  destination: "/var/www/assets/"
  save_output: "rsync_summary"
```

Fields (rsync-specific):

| Field | Type | Default | Description |
|---|---:|---:|---|
| `includes` | []string | - | Maps to `--include=` patterns (order preserved)
| `excludes` | []string | - | Maps to `--exclude=` patterns
| `options` | []string | - | Additional rsync flags (e.g. `--progress`, `--chmod`)
| `delete_policy` | string | `soft` | `soft` disallows destructive `--delete` options; `force` enables `--delete` but requires `confirm_force: true`
| `delete_excluded` | bool | false | Maps to `--delete-excluded` (only when `delete_policy: force`)
| `compress` | bool | true | Use `-z` when running remote
| `prune_empty_dirs` | bool | false | Maps to `--prune-empty-dirs`
| `dry_run` | bool | false | Maps to `--dry-run` for preview

SaveOutput (structured JSON)
When `save_output` is set on an `rsync_file` step, the executor will add `--stats` to the rsync invocation and parse the textual stats. A JSON summary is saved into the pipeline's context variables under the given key. Example saved JSON:

```json
{
  "exit_code": 0,
  "reason": "success",        // success | idle_timeout | total_timeout | error
  "duration_seconds": 2,
  "files_transferred": 12,
  "bytes_transferred": 1234567,
  "stdout_tail": "...last lines...",
  "stderr_tail": "...last lines..."
}
```

The `reason` field helps callers distinguish between normal success and various termination reasons (idle timeout, total timeout, or other errors).

Safety
- `delete_policy: force` requires `confirm_force: true` to avoid accidental destructive syncs.
- When `delete_policy: soft`, specifying `--delete` or `--delete-excluded` through `options` will be rejected by the arg-builder.

Integration testing
- See `docs/INTEGRATION_TESTING.md` for instructions to run a Docker-based SSH server with `rsync` installed for integration tests.

### Example: per-file entries (`files: []`) with `rsync_file`

Use the `files` array when you need to transfer individual files with different destinations or templates. `rsync_file` accepts the same `files` entries as `file_transfer` — the executor expands globs, preserves relative paths when destination is a directory, and runs rsync per resolved entry.

```yaml
- name: "sync-configs-and-assets"
  type: "rsync_file"
  includes: ["*/", "*.conf"]
  excludes: ["*.tmp"]
  options: ["--progress"]
  delete_policy: "force"
  confirm_force: true
  compress: true
  direction: "upload"
  files:
    - source: "nginx/nginx.conf"
      destination: "/etc/nginx/nginx.conf"
    - source: "site/assets/"
      destination: "/var/www/site/assets/"
    - source: "site/robots.txt"
      destination: "/var/www/site/robots.txt"
  save_output: "rsync_summary"
```

Notes:
- `files` entries may include glob patterns (e.g. `site/**/*.js`) — the executor expands them locally before invoking rsync.
- If you require per-file templating (variable interpolation inside file contents), either use `file_transfer` (which renders per-file) or pre-render files to a temporary directory and rsync that directory.
- `rsync_file` executes rsync per resolved entry; for many small files it's usually more efficient to sync a directory instead of many individual entries.

## Running Pipelines

```bash
# List available executions
pipeline list

# Run specific execution
pipeline run dev
pipeline run prod

# Override variables via CLI
pipeline run prod --var DOCKER_TAG=v1.2.3
pipeline run dev --var DOCKER_TAG=dev-test --var BUILD_ENV=development

# Dynamic variables for CI/CD
pipeline run prod --var DOCKER_TAG=$(git rev-parse --short HEAD)
pipeline run dev --var DOCKER_TAG=dev-$(date +%Y%m%d-%H%M%S)
```

## Real-time Output Streaming

Pipeline displays command output in real-time during execution, providing full visibility into the progress of running commands:

```
▶️  EXECUTING JOB: deploy (HEAD)
📋 Executing step: ping-check
Running on 100.96.182.47: ping -c 3 google.com
Command output: PING google.com (216.239.38.120) 56(84) bytes of data.
Command output: 64 bytes from any-in-2678.1e100.net (216.239.38.120): icmp_seq=1 ttl=128 time=30.0 ms
Command output: 64 bytes from any-in-2678.1e100.net (216.239.38.120): icmp_seq=2 ttl=128 time=30.8 ms
Command output: 64 bytes from any-in-2678.1e100.net (216.239.38.120): icmp_seq=3 ttl=128 time=31.1 ms
Command output:
Command output: --- google.com ping statistics ---
Command output: 3 packets transmitted, 3 received, 0% packet loss, time 1998ms
📋 Executing step: deploy-app
Running on 100.96.182.47: docker-compose up -d
Command output: Creating network "myapp_default" with the default driver
Command output: Creating myapp_web_1 ... done
Command output: Creating myapp_db_1 ... done
✅ Completed job: deploy
```

### Silent Mode for Clean Output
For verbose commands, use `silent: true` to suppress real-time output and display results at the end only:

```yaml
steps:
  - name: "verbose-command"
    type: "command"
    commands: ["ping -c 10 google.com"]
    silent: true
    save_output: "ping_result"

  - name: "display-result"
    type: "command"
    commands: ["echo 'Ping completed: {{ping_result}}'"]
```

### Job Execution Tracking

Pipeline displays execution progress with visual indicators:
- **▶️ EXECUTING JOB: [name] (HEAD)**: Job currently executing
- **📋 Executing step: [name]**: Step running in job
- **✅ Completed job: [name]**: Job completed successfully
- **❌ Failed job: [name]**: Job failed

## Pipeline Logging

Each pipeline execution automatically logs all output to timestamped log files for debugging and audit trails.

### Log File Location

```
.sync_temp/
  └── logs/
      ├── watcher.log                           # Application/watcher logs
      └── {pipeline-name}-{timestamp}.log       # Pipeline execution logs
```

### Log Format

Log files contain:
- **Pipeline info**: Pipeline name and execution timestamp
- **Job execution**: Visual markers for each job
- **Command output**: All stdout/stderr with timestamps
- **Clean format**: ANSI escape codes automatically stripped

**Example log file (`split-database-2025-10-15-08-32-15.log`):**
```log
=== Pipeline: split-database ===
Started: 2025-10-15-08-32-15

[2025-10-15 08:32:15] ▶️  EXECUTING JOB: prepare (HEAD)
[2025-10-15 08:32:16] Docker version 20.10.7, build f0df350
[2025-10-15 08:32:16] 2025/10/15 08:32:16 DEBUG: Processed 100 rows from select query
[2025-10-15 08:32:16] 2025/10/15 08:32:16 DEBUG: Executing batch insert with 100 rows...
[2025-10-15 08:32:17] /home/user/app/internal/database/migration.go:164
[2025-10-15 08:32:17] [1.568ms] [rows:0] INSERT INTO `mockup_user_document` ...
```

### Error evidence (history buffer)

During execution, pipeline maintains a ring buffer of recent output up to 300 KB (307200 bytes). This buffer is FIFO: when the total size of all lines exceeds 300 KB, the oldest lines are evicted until the total size is back under the limit.

When a step fails (timeout, execution error, or session error), the contents of this buffer are flushed to the pipeline log file as evidence. This provides context from the most recent output before the error occurred.

Important notes:
- Default buffer capacity: 300 KB (307200 bytes).
- FIFO eviction: occurs only when total buffer size exceeds 300 KB.
- Single lines larger than 300 KB are not automatically truncated; the implementation stores the line (possibly after evicting all older lines), so totalBytes can temporarily exceed the cap to accommodate the full line.
- Buffer is per-pipeline execution (not shared across executions).

The header written to the log will explain that this is error evidence and indicate that the buffer contents (up to 300 KB) are included. Example (timestamp format added by logging system):

```log
[2025-10-15 12:37:04] === ERROR EVIDENCE (buffer up to 300KB) ===
[2025-10-15 12:37:04] <captured output line 1>
[2025-10-15 12:37:04] <captured output line 2>
... (up to the full buffer contents)
```

### Controlling real-time logging with `log_output`

Use the `log_output` field to control whether real-time output from a step is written to the pipeline log file. This field is opt-in and resolved with the following priority (highest → lowest):

- Step-level `log_output`
- Job-level `log_output`
- Pipeline-level `log_output`

Example: if a Step has `log_output: true`, then that step's output will be written to the log file even if the Job or Pipeline sets `log_output: false`. The reverse also applies: `log_output: false` at step level will disable logging for that step even if the job/pipeline level allows.

Key points:
- The in-memory 300 KB buffer always captures output, regardless of `log_output` value. This allows the system to write "error evidence" when failures occur even if file logging is disabled for normal operations.
- `log_output` only controls writing output to log files and real-time display during execution. It does not affect `save_output` behavior for saving output to context variables.

Example YAML:

Step-level:

```yaml
steps:
  - name: "verbose-step"
    type: "command"
    commands: ["make test"]
    log_output: true
```

Job-level:

```yaml
jobs:
  - name: "build"
    log_output: true
    steps:
      - name: "run-tests"
        type: "command"
        commands: ["npm test"]
```

Pipeline-level:

```yaml
pipeline:
  name: "ci"
  log_output: false  # disable file logging by default; enable selectively via job/step
```

Use `log_output` to reduce log noise from very verbose commands, while still relying on the in-memory error-evidence buffer for debugging when failures occur.

### Use Cases

**Debugging**:
```bash
# Review output after execution
tail -f .sync_temp/logs/my-pipeline-2025-10-15-08-30-00.log

# Search for errors
grep -i "error" .sync_temp/logs/my-pipeline-*.log

# Check specific timestamp
grep "08:32:1" .sync_temp/logs/split-database-*.log
```

**Audit Trail**:
- Track all commands executed
- Timestamp every operation
- Archive logs for compliance

**CI/CD Integration**:
```bash
# Upload logs to artifact storage
aws s3 cp .sync_temp/logs/ s3://my-bucket/pipeline-logs/ --recursive

# Attach logs to notification
curl -X POST https://slack.com/api/files.upload \
  -F file=@.sync_temp/logs/deploy-prod-*.log \
  -F channels=deployments
```

**Troubleshooting**:
- Review output from failed commands
- Compare logs from different runs
- Debug timing issues with timestamps

### Log Features

- **Automatic**: No configuration required
- **Timestamped**: Each line with `[YYYY-MM-DD HH:MM:SS]` format
- **Clean**: ANSI escape codes automatically stripped
- **Comprehensive**: Captures all stdout/stderr output
- **Persistent**: Doesn't disappear after pipeline completion

## Variable System

Pipeline supports 3 ways to manage variables:

### 1. Variables File (vars.yaml)

File-based system for managing environment-specific variables:

```yaml
# File: .sync_pipelines/vars.yaml
dev:
  DOCKER_TAG: "dev-$(date +%Y%m%d-%H%M%S)"
  BUILD_ENV: "development"
  DEBUG: "true"

staging:
  DOCKER_TAG: "staging-latest"
  BUILD_ENV: "staging"

prod:
  DOCKER_TAG: "$(git rev-parse --short HEAD)"
  BUILD_ENV: "production"
```

```yaml
# In pipeline.yaml
executions:
  - name: "Development Build"
    key: "dev"
    pipeline: "my-app.yaml"
    var: "dev"                    # Reference to vars.yaml
    hosts: ["localhost"]
```

### 2. Direct Variables

Variables defined directly in executions (new feature):

```yaml
executions:
  - name: "Development Build"
    key: "dev"
    pipeline: "my-app.yaml"
    variables:                    # Direct definition
      DOCKER_TAG: "dev-$(date +%Y%m%d-%H%M%S)"
      BUILD_ENV: "development"
    hosts: ["localhost"]
```

### 3. Hybrid Approach

Combination of vars.yaml + direct variables:

```yaml
executions:
  - name: "Development Build"
    key: "dev"
    pipeline: "my-app.yaml"
    var: "dev"                    # Base from vars.yaml
    variables:                    # Override specific values
      DOCKER_TAG: "custom-override"
    hosts: ["localhost"]
```

### Variable Priority

Variables are merged with the following priority:

1. **CLI Overrides** (`--var KEY=value`) - **Highest**
2. **execution.variables** (direct YAML) - **High**
3. **vars.yaml** (via execution.var) - **Medium**
4. **Global pipeline.variables** - **Low**
5. **Runtime variables** (save_output) - **Lowest**

### Variable Interpolation

Use format `{{VAR_NAME}}` in commands:

```yaml
steps:
  - name: "build-image"
    type: "command"
    commands: ["docker build -t {{DOCKER_REGISTRY}}/{{DOCKER_REPO}}:{{DOCKER_TAG}} ."]
    save_output: "image_id"

  - name: "deploy"
    type: "command"
    commands: ["echo 'Deploying image {{image_id}}'"]
```

#### Fields Supporting Variable Interpolation

Variable interpolation with format `{{VAR_NAME}}` is supported in:

| Field | Type | Description |
|-------|------|-------------|
| `commands` | []string | Commands to execute |
| `file` | string | Script file path (for `script` type) |
| `source` | string | Source path (for `file_transfer` type) |
| `destination` | string | Destination path (for `file_transfer` type) |
| `working_dir` | string | Working directory for step |
| `conditions[].pattern` | string | Regex pattern for conditional matching |
| `expect[].response` | string | Response for interactive prompts |

Example usage in various fields:

```yaml
steps:
  - name: "run-script"
    type: "script"
    file: "scripts/{{SCRIPT_NAME}}.sh"  # Variable in file path
    working_dir: "{{WORKSPACE_PATH}}"   # Variable in working directory

  - name: "upload-config"
    type: "file_transfer"
    source: "config/{{ENV}}.yaml"       # Variable in source path
    destination: "/etc/app/{{ENV}}.yaml" # Variable in destination path
```

## Advanced Features

### Variable Persistence
```yaml
steps:
  - name: "get-version"
    commands: ["echo 'v1.2.3'"]
    save_output: "app_version"

  - name: "deploy"
    commands: ["echo 'Deploying {{app_version}}'"]
```

### Subroutine Calls
```yaml
steps:
  - name: "check-condition"
    commands: ["echo 'SUCCESS'"]
    conditions:
      - pattern: "SUCCESS"
        action: "goto_job"
        job: "success_handler"
```

### Condition Actions

The `action` field determines the action taken when a `pattern` matches output. Supported options:

- `continue`: Continue to next step in same job
- `drop`: Stop job execution (next steps not run, job completes without error)
- `goto_step`: Jump to another step in same job (use `step` field for target)
- `goto_job`: Jump to another job in pipeline (use `job` field for target)
- `fail`: Mark job as failed

Example usage:

```yaml
steps:
  - name: "check-status"
    type: "command"
    commands: ["curl -s http://service/health"]
    conditions:
      - pattern: "OK"
        action: "continue"
      - pattern: "ERROR"
        action: "goto_job"
        job: "handle-error"
      - pattern: "TIMEOUT"
        action: "fail"
      - pattern: "RETRY"
        action: "goto_step"
        step: "check-status"
      - pattern: "DROP"
        action: "drop"
```

### Else Condition
```yaml
steps:
  - name: "check-status"
    type: "command"
    commands: ["curl -s http://service/health"]
    conditions:
      - pattern: "OK"
        action: "continue"
      - pattern: "ERROR"
        action: "fail"
    else_action: "goto_job"
    else_job: "handle-unknown"
```

**Else Fields:**
- `else_action`: Action if no conditions match
- `else_step`: Target step for `goto_step`
- `else_job`: Target job for `goto_job`