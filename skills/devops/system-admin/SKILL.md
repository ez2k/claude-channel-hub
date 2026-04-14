---
name: system-admin
description: Server and system administration tasks — check disk/memory/CPU, manage Docker containers, analyze logs, diagnose network issues. Use when the user asks about server status, system health, Docker, or infrastructure.
version: 1.0.0
author: Claude Channel Hub
license: MIT
metadata:
  tags: [DevOps, Server, Docker, System, Monitoring, Logs, Network, Administration]
---

# System Administration

Server health checks, Docker management, log analysis, and network diagnostics via shell commands.

## When to Use

- "Check server status"
- "How much disk space is left?"
- "Is the Docker container running?"
- "Show me the logs for X"
- "Why is the server slow?"
- "Check memory / CPU usage"
- "Network connectivity issue"

## System Health Checks

### Disk Usage

```bash
# Overview of all mounted filesystems
df -h

# Disk usage of current directory, sorted by size
du -sh * | sort -rh | head -20

# Find large files (>100MB)
find / -size +100M -type f 2>/dev/null | xargs ls -lh | sort -k5 -rh | head -20

# Inode usage (important for many small files)
df -i
```

### Memory

```bash
# Memory overview
free -h

# Detailed memory info
cat /proc/meminfo | grep -E "MemTotal|MemFree|MemAvailable|SwapTotal|SwapFree"

# Top memory consumers
ps aux --sort=-%mem | head -15

# Memory usage by process name
ps aux | awk '{print $4, $11}' | sort -rn | head -10
```

### CPU

```bash
# CPU info
nproc
cat /proc/cpuinfo | grep "model name" | head -1

# Current CPU usage (snapshot)
top -bn1 | grep "Cpu(s)"

# Top CPU consumers
ps aux --sort=-%cpu | head -15

# Load average (1m, 5m, 15m)
uptime

# Detailed per-core stats
mpstat -P ALL 1 3 2>/dev/null || vmstat 1 3
```

### System Overview

```bash
# Uptime and load
uptime

# OS and kernel info
uname -a
cat /etc/os-release

# Logged-in users
who

# Running processes count
ps aux | wc -l

# Open file descriptors
lsof | wc -l 2>/dev/null
```

## Docker Management

### Container Status

```bash
# All containers (running + stopped)
docker ps -a

# Running containers with resource usage
docker stats --no-stream

# Container details
docker inspect <container_name_or_id>

# Container logs (last 100 lines)
docker logs --tail 100 <container>

# Follow logs in real time
docker logs -f <container>

# Logs with timestamps
docker logs --timestamps --tail 50 <container>
```

### Container Operations

```bash
# Start / stop / restart
docker start <container>
docker stop <container>
docker restart <container>

# Execute command in running container
docker exec -it <container> bash
docker exec -it <container> sh  # if bash not available

# Copy files from/to container
docker cp <container>:/path/to/file ./local/
docker cp ./local/file <container>:/path/to/

# Remove stopped containers
docker container prune -f
```

### Images and Volumes

```bash
# List images
docker images

# Image disk usage
docker system df

# Remove unused images
docker image prune -f

# List volumes
docker volume ls

# Remove unused volumes
docker volume prune -f

# Full cleanup (dangling images, stopped containers, unused networks)
docker system prune -f
```

### Docker Compose

```bash
# Status of compose services
docker compose ps

# Logs for all services
docker compose logs --tail 50

# Logs for specific service
docker compose logs --tail 100 <service>

# Restart a service
docker compose restart <service>

# Rebuild and restart
docker compose up -d --build <service>

# Stop all services
docker compose down
```

## Log Analysis

### System Logs

```bash
# Recent system logs (systemd)
journalctl -n 100
journalctl -f  # Follow

# Logs for specific service
journalctl -u nginx -n 50
journalctl -u docker -n 50

# Logs since last boot
journalctl -b

# Error and critical logs only
journalctl -p err -n 50

# Traditional log files
tail -f /var/log/syslog
tail -f /var/log/auth.log
tail -n 100 /var/log/nginx/error.log
```

### Log Filtering

```bash
# Search for errors in log file
grep -i "error\|critical\|fatal" /var/log/syslog | tail -20

# Show lines around a match (context)
grep -A 3 -B 3 "ERROR" /var/log/app.log | tail -50

# Count occurrences
grep -c "ERROR" /var/log/app.log

# Logs from last hour (using journalctl)
journalctl --since "1 hour ago"

# Logs between timestamps
journalctl --since "2024-01-15 10:00:00" --until "2024-01-15 11:00:00"
```

## Network Diagnostics

### Connectivity

```bash
# Basic ping
ping -c 4 google.com
ping -c 4 8.8.8.8

# Traceroute
traceroute google.com
tracepath google.com  # alternative

# DNS resolution
nslookup google.com
dig google.com
host google.com

# Check if port is open
nc -zv <host> <port>
telnet <host> <port>
```

### Ports and Connections

```bash
# Listening ports
ss -tlnp
netstat -tlnp  # if ss not available

# All connections
ss -anp

# Connections to specific port
ss -tlnp | grep :80
ss -tlnp | grep :443

# Who is using a specific port
lsof -i :<port>
fuser <port>/tcp
```

### Bandwidth and Interfaces

```bash
# Network interface info
ip addr show
ifconfig  # older systems

# Network interface statistics
ip -s link show

# Routing table
ip route show
netstat -rn

# DNS servers configured
cat /resolv.conf
systemd-resolve --status 2>/dev/null | grep "DNS Servers"
```

## Process Management

```bash
# Find process by name
pgrep -la <name>
ps aux | grep <name>

# Kill process
kill <pid>
kill -9 <pid>  # force kill
pkill <name>

# Process tree
pstree -p

# Open files by process
lsof -p <pid>

# Systemd service status
systemctl status <service>
systemctl restart <service>
systemctl start <service>
systemctl stop <service>
```

## Quick Diagnostic Script

Run this for a full system snapshot:

```bash
echo "=== UPTIME ===" && uptime
echo "=== DISK ===" && df -h
echo "=== MEMORY ===" && free -h
echo "=== CPU TOP 5 ===" && ps aux --sort=-%cpu | head -6
echo "=== MEM TOP 5 ===" && ps aux --sort=-%mem | head -6
echo "=== DOCKER ===" && docker ps 2>/dev/null || echo "Docker not running"
echo "=== LISTENING PORTS ===" && ss -tlnp 2>/dev/null | head -20
```

## Notes

- Always use `sudo` when commands require elevated privileges
- `ss` is preferred over deprecated `netstat`
- For production servers, prefer read-only diagnostic commands first
- When disk is full: check `/var/log`, `/tmp`, and Docker volumes first
- High load average: check CPU-bound processes with `top` or `htop`
