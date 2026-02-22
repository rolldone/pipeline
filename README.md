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
- **Multi-Hop SSH**: Access servers through jump hosts/bastion servers
- **Configuration Management**: Flexible YAML-based configuration system
- **Execution Validation**: Validates pipeline files, hosts, jobs, and variables before execution
- **Key-Based Job Referencing**: Reference jobs by key for cleaner execution configurations

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

## SSH Configuration & Authentication

Pipeline supports multiple SSH authentication methods for secure remote execution:

### SSH Authentication Methods

Pipeline supports three SSH authentication methods in priority order:

1. **Public Key with Passphrase** (recommended for encrypted keys)
2. **Public Key without Passphrase** (for unencrypted keys)
3. **Password Authentication** (fallback method)

### SSH Configuration Fields

| Field | Type | Description |
|-------|------|-------------|
| `HostName` | string | Actual hostname/IP to connect to |
| `Host` | string | Alias for HostName (fallback) |
| `User` | string | SSH username |
| `Port` | string | SSH port (default: 22) |
| `IdentityFile` | string | Path to SSH private key file |
| `Password` | string | SSH password (standard field) |
| `_Password` | string | SSH password (hidden field, takes priority) |
| `_Passphrase` | string | Passphrase for encrypted SSH private keys |
| `ProxyJump` | string | Jump host for multi-hop SSH connections |

### Multi-Hop SSH Support

Pipeline supports multi-hop SSH connections through jump hosts/bastion servers. This allows accessing servers that are not directly reachable from the pipeline host.

#### How Multi-Hop Works

Multi-hop SSH establishes connections through intermediate jump hosts:
```
Pipeline Host → Jump Host (bastion) → Target Server
```

The jump host acts as a proxy, forwarding SSH traffic to the final destination.

#### ProxyJump Configuration

Use the `ProxyJump` field in SSH configurations to specify a jump host:

```yaml
ssh_configs:
  # Jump host configuration (direct access required)
  - Host: bastion
    HostName: bastion.example.com
    User: bastion-user
    IdentityFile: ~/.ssh/bastion_key
    Port: 22

  # Target server accessed through jump host
  - Host: app-server
    HostName: app-server.internal
    User: app-user
    IdentityFile: ~/.ssh/app_key
    ProxyJump: bastion  # Reference to jump host by Host alias
```

#### ProxyJump Format Options

The `ProxyJump` field supports several formats:

```yaml
ssh_configs:
  # 1. Host alias (recommended - references another SSH config)
  - Host: web-server
    HostName: web01.internal
    User: deploy
    ProxyJump: bastion

  # 2. Full specification with user and port
  - Host: db-server
    HostName: db01.internal
    User: dbadmin
    ProxyJump: bastion-user@bastion.example.com:2222

  # 3. Host and port only
  - Host: api-server
    HostName: api01.internal
    User: apiuser
    ProxyJump: bastion.example.com:2222

  # 4. Host only (default port 22)
  - Host: cache-server
    HostName: cache01.internal
    User: cacheuser
    ProxyJump: bastion.example.com
```

#### Authentication Flow

For multi-hop connections, authentication occurs in two stages:

1. **Jump Host Authentication**: Pipeline authenticates with the jump host using the same methods as direct connections
2. **Target Host Authentication**: After establishing the jump connection, pipeline authenticates with the target host

Both hosts can use different authentication methods (password, key, passphrase) independently.

#### Multi-Hop Example

```yaml
project_name: multi-hop-deployment

ssh_configs:
  # Bastion/jump host
  - Host: bastion
    HostName: bastion.company.com
    User: bastion-user
    IdentityFile: ~/.ssh/bastion_key
    _Passphrase: bastion-passphrase

  # Application servers behind bastion
  - Host: app01
    HostName: app01.internal.company.com
    User: app-deploy
    IdentityFile: ~/.ssh/app_key
    ProxyJump: bastion

  - Host: app02
    HostName: app02.internal.company.com
    User: app-deploy
    IdentityFile: ~/.ssh/app_key
    ProxyJump: bastion

  # Database server with different jump host
  - Host: db01
    HostName: db01.internal.company.com
    User: db-admin
    IdentityFile: ~/.ssh/db_key
    ProxyJump: db-bastion-user@db-bastion.company.com:2222

executions:
  - name: "Deploy to All Servers"
    key: "deploy-all"
    pipeline: "deploy.yaml"
    hosts: ["app01", "app02", "db01"]  # Mix of direct and multi-hop hosts
    jobs: ["deploy-app", "migrate-db"]
```

#### Security Considerations

- **Jump Host Security**: Ensure jump hosts are properly secured and monitored
- **Access Control**: Limit SSH access to jump hosts and implement proper authorization
- **Network Security**: Use VPNs or private networks where possible instead of public jump hosts
- **Key Management**: Use different SSH keys for jump hosts and target servers
- **Logging**: Enable SSH logging on jump hosts for audit trails

#### Troubleshooting Multi-Hop Connections

**Common Issues:**

1. **"Failed to connect to jump host"**
   - Verify jump host SSH config is correct
   - Check network connectivity to jump host
   - Ensure jump host allows SSH connections

2. **"Failed to dial target host through jump host"**
   - Verify target host address is reachable from jump host
   - Check if jump host has proper routing/firewall rules
   - Test SSH connection from jump host to target manually

3. **Authentication failures**
   - Ensure both jump and target host credentials are correct
   - Check if target host accepts connections from jump host IP
   - Verify SSH keys are properly installed on respective hosts

**Debug Tips:**
- Test jump host connection separately: `ssh bastion-user@bastion.example.com`
- Test target connection from jump host: `ssh -J bastion-user@bastion.example.com target-user@target.internal`
- Check SSH verbose output for detailed connection information
- Review pipeline logs in `.sync_temp/logs/` for connection errors

### Authentication Examples

#### Public Key with Passphrase (Encrypted Key)
```yaml
ssh_configs:
  - Host: prod-server
    HostName: 192.168.1.100
    User: deploy
    Port: 22
    IdentityFile: ~/.ssh/id_rsa_encrypted
    _Passphrase: my-secret-passphrase
```

#### Public Key without Passphrase (Unencrypted Key)
```yaml
ssh_configs:
  - Host: dev-server
    HostName: dev.example.com
    User: ubuntu
    IdentityFile: ~/.ssh/id_ed25519
```

#### Password Authentication
```yaml
ssh_configs:
  - Host: legacy-server
    HostName: legacy.example.com
    User: admin
    _Password: my-ssh-password
```

### Authentication Priority Logic

Pipeline attempts authentication methods in this order:

1. **Public Key + Passphrase**: If `IdentityFile` and `_Passphrase` are both provided
2. **Public Key Only**: If `IdentityFile` is provided but no `_Passphrase`
3. **Password**: If `_Password` (or `Password`) is provided

### Encrypted SSH Keys Setup

For enhanced security, use encrypted SSH private keys with passphrases:

```bash
# Generate encrypted SSH key pair
ssh-keygen -t ed25519 -C "pipeline-deploy" -f ~/.ssh/pipeline_key

# Copy public key to server
ssh-copy-id -i ~/.ssh/pipeline_key.pub user@server

# Configure in pipeline.yaml
ssh_configs:
  - Host: secure-server
    HostName: server.example.com
    User: user
    IdentityFile: ~/.ssh/pipeline_key
    _Passphrase: your-secure-passphrase
```

### Security Best Practices

- **Use encrypted keys**: Always encrypt SSH private keys with strong passphrases
- **Hidden fields**: Use `_Password` and `_Passphrase` fields to keep sensitive data out of version control
- **Key rotation**: Regularly rotate SSH keys and update passphrases
- **Access control**: Limit SSH key access to specific servers and users

### Troubleshooting SSH Authentication

**Common Issues:**

1. **"Permission denied (publickey)"**
   - Check `IdentityFile` path is correct
   - Ensure public key is installed on server
   - Verify key permissions: `chmod 600 ~/.ssh/private_key`

2. **"Permission denied (publickey,password)"**
   - For encrypted keys: verify `_Passphrase` is correct
   - For password auth: check `_Password` field

3. **"Host key verification failed"**
   - Server's host key changed or first connection
   - Pipeline automatically handles known_hosts for you

**Debug Tips:**
- Pipeline logs SSH connection attempts
- Check `.sync_temp/logs/` for detailed error messages
- Test SSH connection manually: `ssh -i keyfile user@host`

### SSH Connection Management

Pipeline automatically:
- Manages SSH known_hosts verification
- Creates temporary SSH config files in `.sync_temp/.ssh/`
- Handles connection pooling for performance
- Cleans up connections after execution

All SSH operations (command execution, file transfers, rsync) use the same authentication configuration.

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
      steps:
        - name: "build-image"
          type: "command"
          commands: ["docker build -t {{DOCKER_REGISTRY}}/{{DOCKER_REPO}}:{{DOCKER_TAG}} ."]
          save_output: "image_id"

    - name: "deploy"
      mode: "remote"       # Run via SSH (default)
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
| `key` | string | - | Unique key for referencing in executions (optional, falls back to `name`) |
| `name` | string | - | Unique job identifier |
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

### Step Mode Override

Individual steps can override the job's execution mode by specifying a `mode` field. This is useful for mixed local/remote operations within the same job.

```yaml
jobs:
  - name: "deploy-with-local-setup"
    mode: "remote"  # Default for all steps
    steps:
      - name: "create-local-dir"
        type: "command"
        mode: "local"        # Override: run locally
        commands: ["mkdir -p ./temp_files"]
      - name: "generate-config"
        type: "command"
        mode: "local"        # Override: run locally
        commands: ["cp config.template ./temp_files/config.yml"]
      - name: "upload-files"
        type: "file_transfer"
        source: "./temp_files/"
        destination: "/var/www/config/"  # Uses job mode (remote)
```

When `mode` is not specified on a step, it inherits the job's mode (defaulting to `"remote"` if the job has no mode).

## Execution Configuration

Executions define how and when to run pipeline jobs. They provide validation and selective job execution.

### Execution Configuration Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `name` | string | - | Human-readable execution name |
| `key` | string | - | Unique execution identifier |
| `pipeline` | string | - | Pipeline YAML file to execute |
| `jobs` | []string | - | Specific jobs to run (by key or name). If empty, runs all jobs |
| `hosts` | []string | - | SSH host identifiers for remote execution |
| `var` | string | - | Variable set key from vars.yaml |
| `variables` | map | - | Direct variable overrides |

### Job Referencing

Jobs in executions can be referenced by `key` (recommended) or `name` (fallback):

```yaml
jobs:
  - key: "build"
    name: "Build Application"
    steps: [...]
  - key: "test"
    name: "Run Tests"
    steps: [...]
  - key: "deploy"
    name: "Deploy to Production"
    steps: [...]

executions:
  - name: "Full Pipeline"
    key: "full"
    pipeline: "main.yaml"
    jobs: ["build", "test", "deploy"]  # Reference by key

  - name: "Deploy Only"
    key: "deploy-only"
    pipeline: "main.yaml"
    jobs: ["deploy"]  # Run only deployment job
```

### Execution Validation

Pipeline validates executions before running to catch configuration errors early:

- **Pipeline file**: Must exist in `pipeline_dir`
- **Hosts**: Must be defined in `ssh_configs`
- **Jobs**: Must exist in the referenced pipeline (by key or name)
- **Variables**: If `var` is specified, the key must exist in `vars.yaml`

Invalid executions are rejected with clear error messages:

```
❌ Pipeline file 'missing.yaml' not found
❌ Host 'unknown-server' not found in SSH configs
❌ Job 'invalid-job' not found in pipeline 'main.yaml'
❌ Vars key 'missing-env' not found in vars.yaml
```

### Execution Mode (sandbox / live)

Executions now require a `mode` field which must be either `sandbox` or `live`. This is enforced during configuration validation to avoid accidental production runs.

- `sandbox`: Safe mode intended for testing and dry-runs.
- `live`: Real/production mode intended for operations that affect live systems.

Behavior differences:
- When using the interactive menu (`pipeline` without args), selecting a `live` execution will prompt for an interactive captcha confirmation (three attempts, two-minute timeout) to reduce accidental live runs.
- When running from the CLI (`pipeline run <key>`), `live` executions are allowed but will display a prominent warning; CLI runs intentionally bypass the interactive captcha to support automation and CI workflows.

CLI override policy:
- The CLI uses the `mode` defined in the execution entry. To prevent accidental promotion of a `sandbox` execution to `live`, overriding a `sandbox` execution to run as live requires the explicit `--force-live` flag:

```bash
# Run an execution that is marked sandbox as live (explicit override)
pipeline run my-exec --force-live
```

- When `--force-live` is used the CLI will print a strong WARNING and an audit entry is appended to `.sync_temp/logs/override_audit.log` containing timestamp, user, cwd and the full command arguments. This provides an audit trail for intentional overrides.

If `mode` is missing or has an invalid value the config loader will reject the configuration with a clear error message indicating the offending execution entry.

## Debugging

Pipeline supports a small debug facility that you can enable at multiple scopes. The effective debug setting for a running step follows this precedence (highest → lowest):

- CLI / Execution: `--debug` or `execution.debug` (highest)
- Job-level: `job.debug`
- Step-level: `step.debug`

Notes:
- If you pass `--debug` on the CLI or set `execution.debug: true` in your execution entry, debug is enabled globally and job/step cannot disable it.
- `job.debug` overrides `step.debug` for all steps in that job.
- The executor toggles debug mode for each step execution so existing debug prints (guarded by the executor's debug flag) follow the effective per-step value.

Examples:

Enable globally via execution YAML:

```yaml
executions:
  - name: "Dev run"
    key: "dev"
    pipeline: "my-pipeline.yaml"
    hosts: ["localhost"]
    debug: true  # enable debug for the entire execution (CLI has same effect)
```

Enable per-job (overrides step-level):

```yaml
jobs:
  - name: "build"
    debug: true
    steps:
      - name: "compile"
        commands: ["make all"]
        debug: false  # ignored because job.debug true
```

Enable single step only:

```yaml
steps:
  - name: "special-step"
    debug: true
    commands: ["./diagnose.sh"]
```

CLI usage (overrides job/step):

```bash
pipeline run dev --debug
```

Use this to get extra diagnostic output (the executor prints debug messages only when enabled).

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
    when:                           # Alternative DSL (evaluated after conditions)
      - contains: "ttl="
        action: "continue"
```

### Step Configuration Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `name` | string | - | Unique step identifier |
| `mode` | string | - | Execution mode override: `"local"` or `"remote"` (default: inherit from job) |
| `type` | string | - | Step type: `command`, `file_transfer`, `script` |
| `commands` | []string | - | Commands to execute (for `command` type) - takes priority over `command` |
| `command` | string | - | Single command to execute (for `command` type) - for backward compatibility |
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
| `when` | []WhenEntry | - | Condition DSL (`contains`/`equals`/`regex`, `all`/`any`) |
| `expect` | []Expect | - | Interactive prompt responses |

### Command Format Support

### Expect (interactive prompts)

Pipeline supports handling interactive prompts via the `expect` array on a step. Each entry contains:

- `prompt`: a regular expression (Go RE2) to match against stdout/stderr
- `response`: the bytes to write to stdin when the prompt matches (include `\n` to send Enter)

Notes:
- Prompts are compiled with Go's RE2 engine (`regexp.Compile`). RE2 is fast and safe but does not support some PCRE features (lookaround).
- The executor supports prompt matches that span multiple reads (prompts split across chunks).
- If your response should include an Enter, append `\n` (for example: `"yes\n"`).

Example:

```yaml
steps:
  - name: "confirm-deploy"
    type: "command"
    commands: ["./deploy.sh"]
    expect:
      - prompt: "Are you sure\\?"   # regex (escape ?)
        response: "yes\n"
```

Edge cases:
- Avoid PCRE-only constructs (lookahead/lookbehind) — rewrite using RE2-compatible patterns.
- If an invalid regex is provided the executor will surface an error when starting the step.


Pipeline supports two command formats for backward compatibility:

#### Commands Array (Recommended)
```yaml
steps:
  - name: "build-app"
    type: "command"
    commands: ["npm install", "npm run build"]
```

#### Single Command (Legacy)
```yaml
steps:
  - name: "build-app"
    type: "command"
    command: "npm install && npm run build"
```

#### Priority System
When both formats are present, `commands` array takes priority over `command` string:

```yaml
steps:
  - name: "build-app"
    type: "command"
    commands: ["npm install", "npm run build"]  # This will be used
    command: "echo 'ignored'"                    # This is ignored
```

This ensures backward compatibility while encouraging the use of the more flexible `commands` array format.

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

**Structured Output Support**: If the command output is valid JSON, individual fields are automatically extracted and made available as direct variables, while the full JSON remains accessible via the save_output key:

```yaml
steps:
  - name: "get-metadata"
    type: "command"
    commands: ["curl -s https://api.example.com/metadata"]
    save_output: "metadata"

  - name: "use-fields"
    type: "command"
    commands: ["echo 'Version: {{version}} from {{metadata.source}}'"]
```

In this example, if the API returns `{"version": "1.2.3", "source": "production"}`:
- `{{version}}` and `{{source}}` are available directly from the parsed fields
- `{{metadata}}` contains the full JSON string: `{"version": "1.2.3", "source": "production"}`

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

When you need to upload or download multiple files with different targets, use the `files` array within a `file_transfer` step. Each entry can have its own `source`, optional `destination`. If `files` exists, it takes precedence over `source`/`sources`.

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
- Renders file contents using the same `{{var}}` interpolation logic when `template: "enabled"` is set at the step level.
- Uploads rendered content from memory (no temporary intermediate remote file required) using the in-memory uploader. This minimizes local disk churn and avoids leaving partially rendered files on the remote host.
- Default file permission: when a per-file `perm` is not specified, the executor uses `0755` as the default mode for written/uploaded files.
- **Automatic variable storage**: For single files, rendered content is automatically saved to pipeline context variables without requiring `save_output` configuration.
- **Memory-only operation**: Single file operations avoid temporary file creation entirely - content is rendered and stored directly in memory.
- Variable naming: Auto-generated from step name (spaces/hyphens converted to underscores), also accessible via original step name.
- Tolerant rendering: if the executor cannot stage a rendered file (for example, due to a local write error when preparing staged copies), it will fall back to using the original file bytes and record a `render_warnings` entry in the step's saved output (if `save_output` is set).
- `save_output` for `write_file` with multiple files stores a concise JSON summary with fields: `exit_code`, `reason`, `duration_seconds`, and `files_written`. If render fallbacks occurred, a `render_warnings` array is also included.
- `save_output` for `write_file` with single file stores the rendered content directly to the context variable (in-memory, no disk usage).
- `execute_after_write` executes the uploaded file immediately after writing (single file only, executes first file if multiple).

Example (automatic variable storage - no save_output needed):

```yaml
- name: "load-script"
  type: "write_file"
  template: "enabled"
  files:
    - source: "scripts/deploy.sh"
      destination: "/tmp/deploy.sh"

# Rendered content automatically available in variable "load_script" and "load-script"
# Use in subsequent steps:
- name: "execute-script"
  type: "command"
  commands: ["echo '{{load_script}}' | bash"]
```

Example (explicit save_output for custom variable name):

```yaml
- name: "deploy-config"
  type: "write_file"
  template: "enabled"
  files:
    - source: "config/app.conf"
      destination: "/etc/myapp/app.conf"
  save_output: "my_custom_config"

# Rendered content available in variable "my_custom_config"
```

Example (multiple files - requires save_output for summary):

```yaml
- name: "deploy-site"
  type: "write_file"
  template: "enabled"
  files:
    - source: "site/index.html"
      destination: "/var/www/index.html"
    - source: "site/config.js"
      destination: "/var/www/config.js"
  save_output: "deploy_summary"
```

Example (write and execute script directly):

```yaml
- name: "deploy-script"
  type: "write_file"
  template: "enabled"
  files:
    - source: "scripts/deploy.sh"
      destination: "/tmp/deploy.sh"
  execute_after_write: true
```

Notes:
- Use `files` for per-file destination and `perm` control. If `perm` is omitted the step applies `0755` by default.
- This step is useful when you need rendered configuration files deployed directly (for example service unit files, config templates) and want the rendering step consolidated in the pipeline executor rather than using separate local preprocessing.


Behavior:
- Per-file `destination` overrides step-level `destination`. If neither is set for an entry, the step will error.
- Glob patterns in `source` are expanded locally for upload operations; each match result is transferred while preserving the relative path under the destination directory.
- If multiple files are specified, `destination` must be a directory (ending with `/` or already a directory) — otherwise the step will error.
- Backward compatibility: if `files` is not provided, `source` / `sources` will work as before.

## File transfer and migration notes (rsync_file removed)

The legacy `rsync_file` step has been removed. Use `file_transfer` (and where appropriate `write_file`) instead.

Migration notes
- If you previously used `rsync_file`, migrate to `file_transfer`. There is no one-to-one mapping for every rsync flag, but the typical YAML changes look like this:

Old (`rsync_file`):

```yaml
- name: "sync-assets"
  type: "rsync_file"
  source: "dist/"
  destination: "/var/www/app/"
  exclude: ["*.tmp"]
```

New (`file_transfer`):

```yaml
- name: "sync-assets"
  type: "file_transfer"
  source: "dist/"
  destination: "/var/www/app/"
  exclude: ["*.tmp"]     # executor will send these as ignores to the agent
```

If you used templating to modify file contents, prefer the `write_file` step which renders templates and uploads rendered bytes (see the `write_file` section above).

`ignores` (gitignore-style)
- The `file_transfer` step (and the remote agent) accept `ignores` which follow gitignore semantics via `go-gitignore`.
- Negation patterns are supported (prefix with `!`) and later patterns override earlier ones (last-match-wins).

Example:

```yaml
- name: "upload-logs"
  type: "file_transfer"
  source: "logs/"
  destination: "/var/backups/logs/"
  ignores:
    - "*.log"
    - "!important.log"   # keep important.log even though *.log is excluded
```

Agent & config behavior
- The executor uploads step configuration to the remote path `.sync_temp/config.json`. The agent reads this file from `.sync_temp/config.json` (or from the executable directory when running inside `.sync_temp`).
- When a `file_transfer` runs and the agent binary is missing on the remote, the executor will attempt to build/deploy `pipeline-agent` programmatically. The executor looks for `sub_app/agent` in the project tree (or near the executable) when building. If you install a bundled binary without the `sub_app/agent` source, the executor will not be able to build the agent.
- Working directory normalization: the executor normalizes `working_dir` before writing the config so the agent doesn't `chdir` into duplicated parent paths when `.sync_temp` is nested.

Delete / force semantics
- By default, files matched by `ignores` are not deleted by the executor. The `force` delete semantics do not override `ignores` (ignored files are preserved) to avoid accidental data loss.
- If you require different behavior (for example, an explicit destructive sync), prefer adding an explicit include list or a dedicated step that performs manual deletion after review.

Templating
- `file_transfer` focuses on binary-safe transfer. For content rendering use `write_file` (see above). `write_file` supports common placeholder forms like `{{name}}`, `{{ name }}`, `{{.name}}`, and `{{ .name }}` when `template: "enabled"` is set.

If you want, I can add a short migration guide file under `docs/` with examples for common rsync flags and their equivalents (include/exclude/ignores, preserve perms via `perm`, symlink handling notes). 

## Running Pipelines

```bash
# List available executions
pipeline list

# Run specific execution
pipeline run dev
pipeline run prod

# Override hosts via CLI
You can override the execution's configured hosts from the CLI using `--hosts` (comma-separated or repeated flag):

```bash
# Run 'my-exec' but target host1 and host2 instead of the configured hosts
pipeline run my-exec --hosts host1,host2
``` 


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

**Runtime Variables**: Variables created via `save_output` can contain:
- Simple strings (e.g., command output, file content)
- Structured data (JSON objects/arrays) where individual fields become accessible as direct variables
- The full JSON string is also saved under the `save_output` key for reference

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

**Structured Data Access**: When `save_output` captures JSON data, individual fields are accessible as direct variables, and the full JSON remains available:

```yaml
steps:
  - name: "get-info"
    type: "command"
    commands: ["echo '{\"folder\": \"myapp\", \"version\": \"1.0\"}'"]
    save_output: "app_info"

  - name: "use-fields"
    type: "command"
    commands: ["mkdir -p {{folder}} && echo 'Created {{folder}} v{{version}}'"]

  - name: "show-full-json"
    type: "command"
    commands: ["echo 'Full data: {{app_info}}'"]
```

In this example, `{{folder}}` and `{{version}}` are available directly from the structured output, while `{{app_info}}` contains the complete JSON string.

#### Fields Supporting Variable Interpolation

Variable interpolation with format `{{VAR_NAME}}` is supported in:

| Field | Type | Description |
|-------|------|-------------|
| `commands` | []string | Commands to execute |
| `file` | string | Script file path (for `script` type) |
| `source` | string | Source path (for `file_transfer` type) |
| `destination` | string | Destination path (for `file_transfer` type) |
| `working_dir` | string | Working directory for step |
| `save_output` | string | Variable name for saving output (supports interpolation) |
| `conditions[].pattern` | string | Regex pattern for conditional matching |
| `when[].contains` | string | Substring match value (supports interpolation) |
| `when[].equals` | string | Trimmed equality match value (supports interpolation) |
| `when[].regex` | string | Regex match value (supports interpolation) |
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

### When Condition DSL

In addition to legacy `conditions`, steps can use `when` entries:

- Leaf operators: `contains`, `equals`, `regex`
- Group operators: `all` (AND), `any` (OR)
- Actions: `continue`, `drop`, `goto_step`, `goto_job`, `fail`

Evaluation order is:
1. `conditions` (legacy)
2. `when` (DSL)
3. `else_action` (if nothing matched)

Example (`all` / AND):

```yaml
steps:
  - name: "check-output"
    type: "command"
    commands: ["echo 'alpha:42'"]
    when:
      - all:
          - contains: "alpha"
          - regex: ":[0-9]+"
        action: "goto_step"
        step: "next"
```

Example (`any` / OR):

```yaml
steps:
  - name: "check-output"
    type: "command"
    commands: ["echo 'just alpha'"]
    when:
      - any:
          - contains: "missing"
          - equals: "just alpha"
        action: "goto_step"
        step: "next"
```

Reference examples in repository:
- `pipelines/when-groups-all.yaml`
- `pipelines/when-groups-any.yaml`

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