[![📦 SLSA go releaser](https://github.com/Lanrenbang/envwarp/actions/workflows/slsa-build.yml/badge.svg)](https://github.com/Lanrenbang/envwarp/actions/workflows/slsa-build.yml)
[![SLSA 3](https://slsa.dev/images/gh-badge-level3.svg)](https://slsa.dev)

# envwarp

A lightweight, zero-dependency utility for environment variable substitution in template files, with support for secret injection and command chaining. Designed for containerized environments.

---

## Features

- **Dynamic Templating**: Recursively process `.template` files in a directory or a single file.
- **Isolated Environments**: Use a dedicated `.env` file for templating variables without polluting the main process environment.
- **Secret Injection**: Securely inject secrets from files (e.g., Docker secrets, systemd credentials) into environment variables.
- **Command Chaining**: Execute a subsequent application using `syscall.Exec`, replacing the `envwarp` process entirely.
- **Health Checks**: A built-in subcommand to check `http` or `unix` socket endpoint connectivity, perfect for `distroless` images.

## Installation

#### From Releases

Pre-built binaries for various architectures will be available on the project's GitHub Releases page.

#### From Source

Ensure you have Go installed. Clone the repository and run the build command:

```sh
CGO_ENABLED=0 go build -o envwarp -trimpath -ldflags="-s -w" .
```

## Usage

`envwarp` operates primarily through environment variables and command-line flags.

### Basic Templating

- `ENVWARP_TEMPLATE`: Path to the source template file or directory.
- `ENVWARP_CONFDIR`: Path to the output directory.

If `ENVWARP_TEMPLATE` is a directory, `envwarp` will process all files ending in `.template` within it. The `.template` suffix will be removed from the output filenames.

```sh
# Example: Process all templates in /etc/templates and write them to /etc/nginx/conf.d
export ENVWARP_TEMPLATE=/etc/templates
export ENVWARP_CONFDIR=/etc/nginx/conf.d
./envwarp
```

### Executing a Command

- `ENVWARP_EXECUTION`: The command to execute after templates are processed.

`envwarp` will use `syscall.Exec` to replace itself with the new process.

```sh
# Example: After processing templates, start nginx.
export ENVWARP_TEMPLATE=/etc/templates
export ENVWARP_CONFDIR=/etc/nginx/conf.d
export ENVWARP_EXECUTION="nginx -g 'daemon off;'"
./envwarp
```

### Using a Custom Environment File

Use the `-e` or `--env` flag to specify a file containing environment variables for templating only. This prevents these variables from being passed to the process specified by `ENVWARP_EXECUTION`.

```sh
# variables in custom.env are only used for templating
./envwarp -e custom.env
```

> **Note on Container Usage:**
> - It is recommended to use a custom filename (e.g., `project.env`) instead of `.env` to avoid conflicts with container tools like Docker or Podman.
> - When using this in a container, you must mount the file as a volume. Avoid using Docker's `env_file` directive for this purpose, as that would make the variables persistent in the container's environment, defeating the purpose of isolation.
> - An example file named `.env.warp.example` is provided in the repository for reference.

### Secret Management

To inject a secret from a file, set an environment variable's value with the `file.` prefix followed by the path to the secret file. `envwarp` will read the first line of the file and use it as the variable's value.

- **Rule**: `VAR_NAME=file./path/to/secret`
- **Exception**: If the variable name ends with `_FILE` (e.g., `DB_PASSWORD_FILE`), this rule is ignored to maintain compatibility with applications that handle this pattern themselves.

```sh
# Given a secret file at /run/secrets/db_password containing "my-secret-pw"
export DB_PASSWORD="file./run/secrets/db_password"

# During templating, ${DB_PASSWORD} will be replaced with "my-secret-pw".
```

### Health Checking

The `check` subcommand provides a lightweight connectivity test, ideal for container health checks.

- **Priority**: Command-line argument > `ENVWARP_CHECKURL` environment variable.

```sh
# Check an HTTP endpoint
./envwarp check http://localhost:8080/health

# Check a UNIX socket
./envwarp check unix:///var/run/docker.sock

# Use the environment variable as a fallback
export ENVWARP_CHECKURL="http://localhost:9000"
./envwarp check
```
> **Note**: The health checker only supports `http` and `unix` protocols. `https` is not supported to ensure a minimal binary size.

### Version

To print the version of the application, use the `-v` or `--version` flag.

```sh
./envwarp -v
```

---

## Acknowledgements

This project relies on the excellent work of the following open-source modules:

- [envsubst](https://github.com/a8m/envsubst)
- [godotenv](https://github.com/joho/godotenv)
