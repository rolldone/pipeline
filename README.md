# Pipeline

A standalone CLI tool for executing automated pipeline workflows with interactive menu support.

## Installation

```bash
go install github.com/rolldone/pipeline@latest
```

Or download the binary from releases.

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

## Usage

```bash
pipeline [command]
```

When run without arguments, launches an interactive menu for selecting operations.

## Commands

### Interactive Menu (Default)

Run `pipeline` without arguments to launch the interactive menu:

```bash
pipeline
```

Menu options include:
- **▶️ Pipeline Executions** - Run configured pipeline workflows
- **🔗 SSH Connections** - Direct SSH access to configured servers
- **📝 Create Pipeline** - Generate new pipeline templates
- **📋 List Pipelines** - Show available executions
- **ℹ️ Show Info** - Display debugging information
- **🔄 Reload Config** - Refresh configuration
- **🚪 Exit** - Close menu

### pipeline init

Initialize default pipeline configuration in the current directory.

```bash
pipeline init
```

Creates `pipeline.yaml` and `pipelines/` directory for a new pipeline project.

### pipeline run [execution_key]

Run a pipeline execution by its key.

```bash
pipeline run my-execution
```

Options:
- `--var key=value`: Override variables

### pipeline list

List all available pipeline executions.

```bash
pipeline list
```

### pipeline create [name]

Create a new pipeline template.

```bash
pipeline create my-pipeline
```

### pipeline info

Display pipeline information for debugging.

```bash
pipeline info
```

### pipeline menu

Launch the interactive menu explicitly (same as running `pipeline` without arguments).

```bash
pipeline menu
```

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

## Pipeline Format

Pipelines are defined in YAML files. See `job-sample.yaml` for a complete example.

## Examples

1. **Initialize project and launch menu:**
```bash
pipeline init
pipeline  # Launches interactive menu
```

2. **Create a new pipeline:**
```bash
pipeline create ci-pipeline
```

3. **List available executions:**
```bash
pipeline list
```

4. **Run a pipeline with variable override:**
```bash
pipeline run prod-deploy --var env=production
```

5. **Use custom config:**
```bash
pipeline --config staging.yaml run staging-deploy
```

6. **Direct SSH access via menu:**
```bash
pipeline  # Select SSH connection from menu
```

## Features

- **Interactive Menu**: Navigate pipeline executions and SSH connections with arrow keys
- **Template Variables**: Reuse configuration values with `=var.key` syntax
- **SSH Integration**: Direct server access through configured SSH commands
- **Pipeline Execution**: Run complex workflows with job dependencies
- **Configuration Management**: Flexible YAML-based configuration system