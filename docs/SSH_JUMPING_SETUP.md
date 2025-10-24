# SSH Jumping Feature Setup Guide

## Overview

SSH Jumping memungkinkan koneksi ke server internal melalui intermediate host (jump host) atau chaining user di server yang sama. Fitur ini berguna untuk:

- Akses server internal tanpa public IP
- Multi-hop connections untuk security
- Local user chaining (contoh: donny → root@localhost)

## Prerequisites

### 1. SSH Server Setup
Pastikan SSH server terinstall dan configured dengan baik di semua host:

```bash
# Ubuntu/Debian
sudo apt update
sudo apt install openssh-server

# CentOS/RHEL
sudo yum install openssh-server
sudo systemctl start sshd
```

### 2. SSH Key Authentication
Setup SSH keys untuk autentikasi tanpa password:

```bash
# Generate SSH key pair
ssh-keygen -t rsa -b 4096 -C "your-email@example.com"

# Copy public key ke server
ssh-copy-id user@hostname

# Test koneksi
ssh user@hostname
```

### 3. User Permissions
- Jump user harus punya akses SSH ke target user
- Target user (root) harus restricted ke localhost only
- Configure `/etc/ssh/sshd_config`:

```bash
# Allow only key authentication
PasswordAuthentication no
PermitRootLogin no

# Restrict root to localhost (optional)
# Match User root
#     AllowUsers root@localhost
```

## Configuration Examples

### Scenario 1: Local User Chaining (donny → root@localhost)

```yaml
# pipeline.yaml
direct_access:
  ssh_configs:
    - Host: server_donny
      HostName: your-server.com
      User: donny
      Port: 22
      IdentityFile: ~/.ssh/donny_key

    - Host: server_root
      HostName: localhost
      User: root
      Port: 22
      IdentityFile: ~/.ssh/root_key
      JumpHost: server_donny  # Custom field untuk jumping
```

### Scenario 2: Traditional Bastion Host

```yaml
direct_access:
  ssh_configs:
    - Host: bastion
      HostName: bastion.example.com
      User: bastion_user
      IdentityFile: ~/.ssh/bastion_key

    - Host: internal_server
      HostName: internal.example.com
      User: internal_user
      IdentityFile: ~/.ssh/internal_key
      JumpHost: bastion
```

### Scenario 3: Multiple Hops

```yaml
direct_access:
  ssh_configs:
    - Host: jump1
      HostName: jump1.example.com
      User: jump1_user
      IdentityFile: ~/.ssh/jump1_key

    - Host: jump2
      HostName: jump2.internal.com
      User: jump2_user
      IdentityFile: ~/.ssh/jump2_key
      JumpHost: jump1

    - Host: target
      HostName: target.internal.com
      User: target_user
      IdentityFile: ~/.ssh/target_key
      JumpHost: jump2
```

## Pipeline Usage

### Basic Usage

```yaml
# pipeline.yaml
executions:
  - name: deploy_via_jump
    hosts:
      - server_root  # Akan menggunakan jumping via server_donny
    steps:
      - type: command
        commands:
          - whoami
          - pwd
      - type: write_file
        source: config.txt
        destination: /etc/config.txt
```

### Advanced Usage dengan Variables

```yaml
# pipeline.yaml
var:
  jump_user: donny
  target_user: root

executions:
  - name: dynamic_jump
    hosts:
      - server_root
    vars:
      jump_user: "{{jump_user}}"
      target_user: "{{target_user}}"
    steps:
      - type: command
        commands:
          - echo "Connected as {{target_user}} via {{jump_user}}"
```

## Security Considerations

### 1. SSH Key Management
- Gunakan passphrase pada private keys
- Store keys securely (encrypted storage)
- Rotate keys periodically
- Limit key usage dengan `command` restriction di authorized_keys

### 2. Access Control
- Jump user hanya untuk transit, bukan akses penuh
- Monitor SSH access logs
- Implement fail2ban untuk brute force protection
- Use SSH certificates instead of keys untuk large deployments

### 3. Network Security
- Restrict SSH access by IP (firewall rules)
- Use VPN for additional security layer
- Implement session recording/auditing

## Setup Steps

### Step 1: Prepare SSH Keys
```bash
# For donny user
ssh-keygen -t rsa -b 4096 -f ~/.ssh/donny_key
ssh-copy-id -i ~/.ssh/donny_key donny@your-server.com

# For root user (from donny session)
ssh donny@your-server.com
sudo ssh-keygen -t rsa -b 4096 -f /root/.ssh/root_key
sudo cat /root/.ssh/root_key.pub >> /root/.ssh/authorized_keys
# Copy private key back to local machine
```

### Step 2: Configure SSH Config
Update `pipeline.yaml` dengan ssh_configs seperti contoh di atas.

### Step 3: Test Connection
```bash
# Test direct connection
ssh -i ~/.ssh/donny_key donny@your-server.com

# Test jump connection (manual)
ssh -i ~/.ssh/donny_key -o ProxyCommand="ssh -i ~/.ssh/donny_key -W %h:%p donny@your-server.com" root@localhost
```

### Step 4: Test Pipeline
Jalankan pipeline dengan host yang menggunakan jumping:

```bash
make-sync run pipeline.yaml
```

## Troubleshooting

### Common Issues

1. **Permission Denied**
   - Check SSH key permissions: `chmod 600 ~/.ssh/private_key`
   - Verify public key in authorized_keys
   - Check user permissions on target server

2. **Connection Timeout**
   - Verify hostname/IP accessibility
   - Check firewall rules
   - Test intermediate connections step by step

3. **Key Authentication Fails**
   - Use `ssh -v` for verbose output
   - Check SSH agent: `ssh-add -l`
   - Verify key format and passphrase

4. **Jump Connection Issues**
   - Test jump host connection first
   - Verify jump user can access target
   - Check SSH config syntax

### Debug Commands

```bash
# Verbose SSH connection
ssh -v -i ~/.ssh/key user@host

# Test tunnel manually
ssh -L 2222:localhost:22 jump_user@jump_host
ssh -p 2222 target_user@localhost

# Check SSH service status
sudo systemctl status sshd
sudo journalctl -u sshd -f
```

## Implementation Notes

### Current Status
- SSH jumping belum diimplementasikan dalam kode
- `ProxyCommand` field ada tapi tidak digunakan
- `JumpHost` field perlu ditambahkan

### Required Changes
1. Add `JumpHost` field validation
2. Implement connection chaining logic
3. Update SSH client untuk handle tunnels
4. Add timeout handling untuk multi-hop
5. Implement reconnect logic per hop

### Future Enhancements
- Support untuk ProxyJump (standar OpenSSH)
- GUI untuk setup jumping config
- Connection pooling untuk performance
- Integration dengan SSH config system (~/.ssh/config)