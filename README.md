# Pipeline

A standalone CLI tool for executing automated pipeline workflows.

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

## Usage

```bash
pipeline [command]
```

## Commands

### pipeline init

Initialize default pipeline configuration in the current directory.

```bash
pipeline init
```

Creates `pipeline.yaml` and `pipelines/` directory for a new pipeline project.

### pipeline run [execution_key]

Run a pipeline execution by its key.

```bash
pipeline pipeline run my-execution
```

Options:
- `--var key=value`: Override variables

### pipeline list

List all available pipeline executions.

```bash
pipeline pipeline list
```

### pipeline create [name]

Create a new pipeline template.

```bash
pipeline pipeline create my-pipeline
```

### pipeline info

Display pipeline information for debugging.

```bash
pipeline info
```

## Configuration

Pipeline uses a `make-sync.yaml` configuration file. You can specify a custom config file:

```bash
pipeline --config custom.yaml pipeline list
```

### Config Structure

```yaml
project_name: my-project
localPath: .

devsync:
  os_target: linux
  auth:
    username: user
    host: example.com
    port: 22
    remotePath: /home/user
    privateKey: ~/.ssh/id_rsa
  ignores: []
  agent_watchs: []
  manual_transfer: []
  script: {}
  trigger_permission: {}

direct_access:
  pipeline_dir: ./pipelines
  executions:
    - name: "Deploy to Production"
      key: "prod-deploy"
      pipeline: "deploy.yaml"
      hosts: ["prod-server"]
  ssh_configs: []
```

## Pipeline Format

Pipelines are defined in YAML files. See `job-sample.yaml` for a complete example.

## Examples

1. Create a new pipeline:
```bash
pipeline pipeline create ci-pipeline
```

2. List available executions:
```bash
pipeline pipeline list
```

3. Run a pipeline with variable override:
```bash
pipeline pipeline run prod-deploy --var env=production
```

4. Use custom config:
```bash
pipeline --config staging.yaml pipeline run staging-deploy
```