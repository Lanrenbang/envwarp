package main

import (
	"bufio"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/a8m/envsubst"
	"github.com/joho/godotenv"
)

// version is set at build time
var version string

// stringSlice is a custom flag type to support multiple env files
type stringSlice []string

func (i *stringSlice) String() string {
	return strings.Join(*i, ", ")
}

func (i *stringSlice) Set(value string) error {
	*i = append(*i, value)
	return nil
}

const (
	filePrefix = "file."
)

func main() {
	log.SetPrefix("[envwarp] ")
	log.SetFlags(0)

	// --- Flag definitions ---
	var envFiles stringSlice
	checkCmd := flag.NewFlagSet("check", flag.ExitOnError)

	// Top-level flags
	versionFlag := flag.Bool("v", false, "print version and exit")
	flag.BoolVar(versionFlag, "version", false, "print version and exit") // Long form for version

	// Custom var for repeated -e/--env flags
	flag.Var(&envFiles, "e", "path to a custom environment file (can be specified multiple times)")
	flag.Var(&envFiles, "env", "path to a custom environment file (can be specified multiple times)")

	// Handle subcommands first, as they have their own logic
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "check":
			checkCmd.Parse(os.Args[2:])
			address := checkCmd.Arg(0)
			if address == "" {
				address = os.Getenv("ENVWARP_CHECKURL")
			}
			if address == "" {
				log.Fatal("Error: address must be provided as an argument or via ENVWARP_CHECKURL environment variable.")
			}
			runHealthCheck(address)
			// runHealthCheck will os.Exit
		}
	}

	// Parse top-level flags for main logic
	flag.Parse()

	if *versionFlag {
		if version == "" {
			fmt.Println("v0.0.0-dev")
		} else {
			fmt.Println(version)
		}
		os.Exit(0)
	}

	// --- Main logic starts here ---
	var originalEnv []string
	if len(envFiles) > 0 {
		log.Printf("Loading custom environment files: %s", envFiles.String())
		originalEnv = os.Environ()

		// Outer loop: process each file sequentially.
		for _, file := range envFiles {
			// Inner loop: process each file multiple times to resolve nested variables within the same file.
			for i := 0; i < 5; i++ { // Limit to 5 passes to prevent infinite loops.
				changedCounter := 0

				content, err := envsubst.ReadFile(file)
				if err != nil {
					log.Fatalf("Error reading/substituting env file %s: %v", file, err)
				}

				envMap, err := godotenv.Unmarshal(string(content))
				if err != nil {
					log.Fatalf("Error unmarshaling env file %s: %v", file, err)
				}

				for key, value := range envMap {
					oldValue := os.Getenv(key)
					if oldValue != value {
						changedCounter++
					}
					if err := os.Setenv(key, value); err != nil {
						log.Fatalf("Error setting env var %s from file %s: %v", key, file, err)
					}
				}

				if changedCounter == 0 {
					break // File is stable, move to the next file.
				}
			}
		}
	}

	// Process secrets after loading env vars
	if err := processSecrets(); err != nil {
		log.Fatalf("Error: Failed to process secrets: %v", err)
	}

	// Get required env vars
	templatePath := os.Getenv("ENVWARP_TEMPLATE")
	confDir := os.Getenv("ENVWARP_CONFDIR")

	if templatePath == "" || confDir == "" {
		log.Fatal("Error: ENVWARP_TEMPLATE and ENVWARP_CONFDIR environment variables must be set.")
	}

	// Process templates
	if err := processTemplates(templatePath, confDir); err != nil {
		log.Fatalf("Error: Failed to process templates: %v", err)
	}

	log.Println("All templates processed successfully.")

	// Execute next command if specified
	executionCmd := os.Getenv("ENVWARP_EXECUTION")
	if executionCmd != "" {
		executeCommand(executionCmd, originalEnv)
	}
}

// processSecrets iterates over environment variables and replaces secret references.
func processSecrets() error {
	for _, env := range os.Environ() {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) != 2 {
			continue
		}
		name, value := parts[0], parts[1]

		if strings.HasSuffix(name, "_FILE") {
			continue
		}

		if strings.HasPrefix(value, filePrefix) {
			secretPath := strings.TrimPrefix(value, filePrefix)
			if _, err := os.Stat(secretPath); err == nil {
				file, err := os.Open(secretPath)
				if err != nil {
					return fmt.Errorf("failed to open secret file %s: %w", secretPath, err)
				}
				defer file.Close()

				scanner := bufio.NewScanner(file)
				if scanner.Scan() {
					secretValue := scanner.Text()
					if err := os.Setenv(name, secretValue); err != nil {
						return fmt.Errorf("failed to set env var %s from secret file: %w", name, err)
					}
					log.Printf("Loaded secret for %s from %s", name, secretPath)
				}
				if err := scanner.Err(); err != nil {
					return fmt.Errorf("failed to read secret file %s: %w", secretPath, err)
				}
			}
		}
	}
	return nil
}

// processTemplates finds and processes all templates.
func processTemplates(templatePath, confDir string) error {
	// Ensure output directory exists
	if err := os.MkdirAll(confDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory '%s': %w", confDir, err)
	}

	fi, err := os.Stat(templatePath)
	if err != nil {
		return fmt.Errorf("cannot stat ENVWARP_TEMPLATE path '%s': %w", templatePath, err)
	}

	if !fi.IsDir() {
		return processSingleFile(templatePath, confDir)
	}

	return filepath.WalkDir(templatePath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".template") {
			return processSingleFile(path, confDir)
		}
		return nil
	})
}

// processSingleFile substitutes env vars into a single template file.
func processSingleFile(filePath, confDir string) error {
	log.Printf("Processing template: %s", filePath)

	content, err := envsubst.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to substitute vars in %s: %w", filePath, err)
	}

	// Determine output path
	fileName := filepath.Base(filePath)
	outFileName := strings.TrimSuffix(fileName, ".template")
	outPath := filepath.Join(confDir, outFileName)

	if err := os.WriteFile(outPath, content, 0644); err != nil {
		return fmt.Errorf("failed to write to %s: %w", outPath, err)
	}

	log.Printf("Successfully written to: %s", outPath)
	return nil
}

// executeCommand replaces the current process with the specified command.
func executeCommand(command string, customEnv []string) {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		log.Fatal("Error: ENVWARP_EXECUTION is empty.")
	}
	cmdPath, err := exec.LookPath(parts[0])
	if err != nil {
		log.Fatalf("Error: Command not found in PATH: %s", parts[0])
	}

	log.Printf("Executing command: %s", command)

	// If customEnv is nil, it means we used the default environment.
	// syscall.Exec will inherit it automatically.
	// If customEnv is not nil, we must pass it explicitly.
	env := os.Environ()
	if customEnv != nil {
		env = customEnv
	}

	if err := syscall.Exec(cmdPath, parts, env); err != nil {
		log.Fatalf("Error: Failed to execute command: %v", err)
	}
}

// runHealthCheck executes a health check and exits based on the result.
func runHealthCheck(address string) {
	const timeout = 5 * time.Second
	log.Printf("Starting health check for: %s", address)

	switch {
	case strings.HasPrefix(address, "https://"):
		log.Printf("Error: HTTPS health checks are not supported in this build to reduce binary size.")
		os.Exit(1)

	case strings.HasPrefix(address, "http://"):
		target := strings.TrimPrefix(address, "http://")
		host, path := target, "/"
		if idx := strings.Index(target, "/"); idx != -1 {
			host = target[:idx]
			path = target[idx:]
		}

		conn, err := net.DialTimeout("tcp", host, timeout)
		if err != nil {
			log.Printf("HTTP check failed: %v", err)
			os.Exit(1)
		}
		defer conn.Close()

		_ = conn.SetDeadline(time.Now().Add(timeout))

		req := fmt.Sprintf("HEAD %s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", path, host)
		if _, err := conn.Write([]byte(req)); err != nil {
			log.Printf("HTTP check failed on write: %v", err)
			os.Exit(1)
		}

		reader := bufio.NewReader(conn)
		statusLine, err := reader.ReadString('\n')
		if err != nil {
			log.Printf("HTTP check failed on read: %v", err)
			os.Exit(1)
		}

		parts := strings.SplitN(strings.TrimSpace(statusLine), " ", 3)
		if len(parts) < 2 || !strings.HasPrefix(parts[0], "HTTP/") {
			log.Printf("HTTP check failed, invalid status line: %q", statusLine)
			os.Exit(1)
		}

		code, err := strconv.Atoi(parts[1])
		if err != nil {
			log.Printf("HTTP check failed, invalid status code: %q", parts[1])
			os.Exit(1)
		}

		if code < 500 {
			log.Printf("HTTP check successful, service is online. Status code: %d", code)
			os.Exit(0)
		} else {
			log.Printf("HTTP check failed, server error. Status code: %d", code)
			os.Exit(1)
		}

	case strings.HasPrefix(address, "unix://"), strings.HasPrefix(address, "unix/"):
		socketPath := strings.TrimPrefix(address, "unix://")
		socketPath = strings.TrimPrefix(socketPath, "unix/")

		conn, err := net.DialTimeout("unix", socketPath, timeout)
		if err != nil {
			log.Printf("UNIX socket check failed: %v", err)
			os.Exit(1)
		}
		conn.Close()
		log.Println("UNIX socket check successful.")
		os.Exit(0)

	default:
		log.Printf("Error: Unsupported address format for check: %s", address)
		os.Exit(1)
	}
}
