# Greedy Eye — Ansible Role

Deploy Greedy Eye backend with PostgreSQL, Atlas migrations, and Traefik path-based routing.

## Requirements

- Docker + Docker Compose v2
- Traefik with external `proxy` network
- Psina auth service (ForwardAuth via `/verify`)

## Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `greedy_eye_version` | `latest` | Docker image tag |
| `greedy_eye_domain` | `eye.darkfox.info` | Shared platform domain |
| `greedy_eye_db_password` | `greedy_eye_password` | PostgreSQL password |
| `greedy_eye_traefik_middlewares` | `tailscale-only` | Additional Traefik middlewares (appended after `psina-auth`) |
| `greedy_eye_log_level` | `INFO` | Application log level |

See `defaults/main.yml` for full list.

## ForwardAuth

This role automatically configures Traefik ForwardAuth middleware (`psina-auth`) pointing to `http://psina:8080/verify`. Headers `X-User-Id` and `X-User-Email` are forwarded to the backend for lazy user provisioning.

## Usage

### Standalone

```yaml
- role: greedy_eye
  vars:
    greedy_eye_domain: "eye.myapp.com"
    greedy_eye_db_password: "{{ vault_eye_db_password }}"
```

### As part of platform stack

```yaml
- role: psina
  vars:
    psina_platform_domain: "{{ platform_domain }}"
- role: greedy_eye
  vars:
    greedy_eye_domain: "{{ platform_domain }}"
- role: greedy_eye_fe
  vars:
    greedy_eye_fe_platform_domain: "{{ platform_domain }}"
```

## Operations

```bash
# Deploy
ansible-playbook site.yml --tags eye

# Backup
ansible-playbook site.yml --tags eye -e operation=backup

# Destroy
ansible-playbook site.yml --tags eye -e operation=destroy
```
