package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"pipeline/internal/pipeline/types"
	"pipeline/internal/util"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

var printer = util.Default

// fixSSHKeyPermissions sets SSH private key file and directory permissions
// SSH requires private keys to have 0600 permissions (owner read/write only)
// and directories should be accessible (0700 for security by default, but configurable)
func fixSSHKeyPermissions(keyPath string, dirPerm os.FileMode) error {
	if keyPath == "" {
		return nil
	}

	printer.Printf("🔍 Processing SSH key path: %s\n", keyPath)

	// Expand ~ to home directory if present
	if strings.HasPrefix(keyPath, "~") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %v", err)
		}
		keyPath = filepath.Join(homeDir, keyPath[1:])
		printer.Printf("🏠 Expanded ~ to home: %s\n", keyPath)
	}

	// Convert to absolute path
	absKeyPath, err := filepath.Abs(keyPath)
	if err != nil {
		return fmt.Errorf("failed to get absolute path for %s: %v", keyPath, err)
	}
	keyPath = absKeyPath
	printer.Printf("📁 Absolute path: %s\n", keyPath)

	// Check if file exists
	info, err := os.Stat(keyPath)
	if err != nil {
		if os.IsNotExist(err) {
			printer.Printf("❌ SSH key file does not exist: %s\n", keyPath)
			return fmt.Errorf("SSH key file does not exist: %s", keyPath)
		}
		printer.Printf("❌ Cannot access SSH key file: %v\n", err)
		return fmt.Errorf("cannot access SSH key file: %v", err)
	}

	currentPerm := info.Mode().Perm()
	requiredPerm := os.FileMode(0600)

	printer.Printf("🔒 Current permissions: %o, required: %o\n", currentPerm, requiredPerm)

	// Set permissions to 0600
	err = os.Chmod(keyPath, requiredPerm)
	if err != nil {
		return fmt.Errorf("failed to set SSH key permissions for %s: %v", keyPath, err)
	}

	// Verify the permission change
	info, err = os.Stat(keyPath)
	if err != nil {
		printer.Printf("⚠️  Warning: cannot verify permission change: %v\n", err)
	} else {
		newPerm := info.Mode().Perm()
		printer.Printf("✅ SSH key permissions set: %s (%o → %o)\n", keyPath, currentPerm, newPerm)
	}

	// Also fix parent directory permissions to the specified permission
	parentDir := filepath.Dir(keyPath)
	dirInfo, err := os.Stat(parentDir)
	if err == nil {
		currentDirPerm := dirInfo.Mode().Perm()
		if currentDirPerm != dirPerm {
			printer.Printf("📂 Fixing directory permissions: %s (%o → %o)\n", parentDir, currentDirPerm, dirPerm)
			err = os.Chmod(parentDir, dirPerm)
			if err != nil {
				printer.Printf("⚠️  Warning: failed to set directory permissions for %s: %v\n", parentDir, err)
			} else {
				printer.Printf("✅ Directory permissions set: %s\n", parentDir)
			}
		} else {
			printer.Printf("✅ Directory permissions already correct: %s\n", parentDir)
		}
	} else {
		printer.Printf("⚠️  Warning: cannot access parent directory %s: %v\n", parentDir, err)
	}

	return nil
}

const ConfigFileName = "pipeline.yaml"

type Config struct {
	ResetCache     bool                   `yaml:"reset_cache"`
	SyncCollection SyncCollection         `yaml:"sync_collection"`
	Var            map[string]interface{} `yaml:"var,omitempty"`
	ProjectName    string                 `yaml:"project_name"`
	LocalPath      string                 `yaml:"localPath"`
	// Devsync removed: keep backwards compatibility by allowing other fields
	// to live at top-level. Use `DirectAccess` and `Var` for pipeline config.
	DirectAccess DirectAccess `yaml:"direct_access"`
}

type SyncCollection struct {
	Src   string   `yaml:"src"`
	Files []string `yaml:"files"`
}

// Devsync type removed. Pipeline config should not include devsync-specific
// fields. If users still have old configs, they should migrate to the
// pipeline structure (see README).

type Auth struct {
	Username   string `yaml:"username"`
	PrivateKey string `yaml:"privateKey"`
	Password   string `yaml:"password,omitempty"`
	Host       string `yaml:"host"`
	Port       string `yaml:"port"`
	LocalPath  string `yaml:"localPath,omitempty"`
	RemotePath string `yaml:"remotePath"`
}

type Script struct {
	Local  ScriptSection `yaml:"local"`
	Remote ScriptSection `yaml:"remote"`
}

type ScriptSection struct {
	OnReady  string   `yaml:"on_ready"`
	OnStop   string   `yaml:"on_stop"`
	Commands []string `yaml:"commands,omitempty"`
}

type TriggerPermission struct {
	UnlinkFolder bool `yaml:"unlink_folder"`
	Unlink       bool `yaml:"unlink"`
	Change       bool `yaml:"change"`
	Add          bool `yaml:"add"`
}

type DirectAccess struct {
	ConfigFile  string                   `yaml:"config_file"`
	PipelineDir string                   `yaml:"pipeline_dir"`
	SSHConfigs  []map[string]interface{} `yaml:"ssh_configs"`
	SSHCommands []SSHCommand             `yaml:"ssh_commands"`
	Executions  []types.Execution        `yaml:"executions"`
}

type SSHCommand struct {
	AccessName string `yaml:"access_name"`
	Command    string `yaml:"command"`
}

// ValidateConfig validates the configuration for required fields and file paths
func ValidateConfig(cfg *Config) error {
	var validationErrors []string

	// Validate required string fields
	if strings.TrimSpace(cfg.ProjectName) == "" {
		validationErrors = append(validationErrors, "project_name cannot be empty")
	}

	// NOTE: devsync-specific auth checks removed. Use DirectAccess.SSHConfigs
	// and DirectAccess.SSHCommands for pipeline SSH setup. If you have old
	// devsync-formatted configs, migrate them to the new structure.

	// Validate local path exists
	// if strings.TrimSpace(cfg.LocalPath) != "" {
	// 	if _, err := os.Stat(cfg.LocalPath); os.IsNotExist(err) {
	// 		validationErrors = append(validationErrors, fmt.Sprintf("local path does not exist: %s", cfg.LocalPath))
	// 	}
	// }

	// Validate sync collection path
	if strings.TrimSpace(cfg.SyncCollection.Src) != "" {
		// Create directory if it doesn't exist
		if err := os.MkdirAll(cfg.SyncCollection.Src, 0755); err != nil {
			validationErrors = append(validationErrors, fmt.Sprintf("cannot create sync collection directory: %s", cfg.SyncCollection.Src))
		}
	}

	// Validate SSH configs
	for i, sshConfig := range cfg.DirectAccess.SSHConfigs {
		if host, ok := sshConfig["Host"].(string); !ok || strings.TrimSpace(host) == "" {
			validationErrors = append(validationErrors, fmt.Sprintf("SSH config %d: Host cannot be empty", i+1))
		}

		if hostName, ok := sshConfig["HostName"].(string); !ok || strings.TrimSpace(hostName) == "" {
			validationErrors = append(validationErrors, fmt.Sprintf("SSH config %d: HostName cannot be empty", i+1))
		}

		if user, ok := sshConfig["User"].(string); !ok || strings.TrimSpace(user) == "" {
			validationErrors = append(validationErrors, fmt.Sprintf("SSH config %d: User cannot be empty", i+1))
		}

		// Validate SSH port (skip validation for references starting with =)
		if port, ok := sshConfig["Port"].(string); ok && strings.TrimSpace(port) != "" && !strings.HasPrefix(port, "=") {
			if p, err := strconv.Atoi(port); err != nil || p <= 0 || p > 65535 {
				validationErrors = append(validationErrors, fmt.Sprintf("SSH config %d: Port must be a valid number between 1-65535", i+1))
			}
		}

		// Validate identity file exists (if specified and not a reference)
		if identityFile, ok := sshConfig["IdentityFile"].(string); ok && strings.TrimSpace(identityFile) != "" && !strings.HasPrefix(identityFile, "=") {
			if _, err := os.Stat(identityFile); os.IsNotExist(err) {
				validationErrors = append(validationErrors, fmt.Sprintf("SSH config %d: Identity file does not exist: %s", i+1, identityFile))
			}
		}

		// Validate HostName (skip validation for references starting with =)
		if hostName, ok := sshConfig["HostName"].(string); ok && !strings.HasPrefix(hostName, "=") && strings.TrimSpace(hostName) == "" {
			validationErrors = append(validationErrors, fmt.Sprintf("SSH config %d: HostName cannot be empty", i+1))
		}

		// Validate User (skip validation for references starting with =)
		if user, ok := sshConfig["User"].(string); ok && !strings.HasPrefix(user, "=") && strings.TrimSpace(user) == "" {
			validationErrors = append(validationErrors, fmt.Sprintf("SSH config %d: User cannot be empty", i+1))
		}
	}

	// Validate SSH commands
	for i, sshCmd := range cfg.DirectAccess.SSHCommands {
		if strings.TrimSpace(sshCmd.AccessName) == "" {
			validationErrors = append(validationErrors, fmt.Sprintf("SSH command %d: access_name cannot be empty", i+1))
		}

		if strings.TrimSpace(sshCmd.Command) == "" {
			validationErrors = append(validationErrors, fmt.Sprintf("SSH command %d: command cannot be empty", i+1))
		}
	}

	// Validate executions: require mode and valid values
	for i, exec := range cfg.DirectAccess.Executions {
		if strings.TrimSpace(exec.Name) == "" {
			validationErrors = append(validationErrors, fmt.Sprintf("Execution %d: name cannot be empty", i+1))
		}
		if strings.TrimSpace(exec.Key) == "" {
			validationErrors = append(validationErrors, fmt.Sprintf("Execution %d: key cannot be empty", i+1))
		}
		if strings.TrimSpace(exec.Pipeline) == "" {
			validationErrors = append(validationErrors, fmt.Sprintf("Execution %d: pipeline cannot be empty", i+1))
		}

		// Mode is required and must be either sandbox or live
		mode := strings.ToLower(strings.TrimSpace(exec.Mode))
		if mode == "" {
			validationErrors = append(validationErrors, fmt.Sprintf("Execution '%s' (index %d): missing required field 'mode' (must be 'sandbox' or 'live')", exec.Key, i+1))
		} else if mode != "sandbox" && mode != "live" {
			validationErrors = append(validationErrors, fmt.Sprintf("Execution '%s' (index %d): invalid mode '%s' (must be 'sandbox' or 'live')", exec.Key, i+1, exec.Mode))
		}
	}

	// No global devsync settings to validate in pipeline mode.

	// If there are validation errors, return them
	if len(validationErrors) > 0 {
		return fmt.Errorf("configuration validation failed:\n%s", strings.Join(validationErrors, "\n"))
	}

	return nil
}

// LoadAndValidateConfig loads and validates the configuration
func LoadAndValidateConfig() (*Config, error) {
	if !ConfigExists() {
		return nil, errors.New("pipeline.yaml not found. Please run 'pipeline init' first")
	}

	// Read raw config file
	data, err := os.ReadFile(ConfigFileName)
	if err != nil {
		return nil, fmt.Errorf("error reading config file: %v", err)
	}

	// Load .env if exists (same dir as config file)
	cfgDir := filepath.Dir(ConfigFileName)
	envMap, _ := loadDotEnvIfExists(cfgDir)

	// Interpolate ${VAR} using OS env first, then .env values
	rendered := interpolateEnv(string(data), envMap)

	// First unmarshal into a temporary config to access cfg.Var for var-based refs
	var tmpCfg Config
	if err := yaml.Unmarshal([]byte(rendered), &tmpCfg); err != nil {
		return nil, fmt.Errorf("error parsing config file (initial unmarshal): %v", err)
	}

	// Create renderer backed by tmpCfg to resolve =var.* inside var map
	renderer := NewAdvancedTemplateRenderer(&tmpCfg)

	// Resolve var entries iteratively (to handle transitive =var.* references)
	if err := renderVarMap(renderer, tmpCfg.Var); err != nil {
		return nil, fmt.Errorf("error resolving var entries: %v", err)
	}

	// Now parse YAML into node tree and render =... expressions using renderer
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(rendered), &root); err != nil {
		return nil, fmt.Errorf("error parsing config file to node: %v", err)
	}

	if err := walkAndRenderNode(&root, renderer); err != nil {
		return nil, fmt.Errorf("error rendering config nodes: %v", err)
	}

	// Encode node back to YAML bytes
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&root); err != nil {
		return nil, fmt.Errorf("error encoding rendered yaml: %v", err)
	}
	enc.Close()

	// Final unmarshal into cfg
	var cfg Config
	if err := yaml.Unmarshal(buf.Bytes(), &cfg); err != nil {
		return nil, fmt.Errorf("error parsing config file (final unmarshal): %v", err)
	}

	// Validate the configuration
	if err := ValidateConfig(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// RenderTemplateVariables renders template variables in the configuration
// Replaces references like =username, =host, =port, etc. with actual values
func RenderTemplateVariables(cfg *Config) error {
	// Read the current config file as raw text
	data, err := os.ReadFile(ConfigFileName)
	if err != nil {
		return fmt.Errorf("error reading config file for template rendering: %v", err)
	}

	// Convert to string for processing
	configText := string(data)

	// Create a map of template variables using var system
	templateVars := map[string]string{
		"localPath":    cfg.LocalPath,
		"project_name": cfg.ProjectName,
	}

	// Add auth variables from var system if available
	if cfg.Var != nil {
		if authVars, hasAuth := cfg.Var["auth"]; hasAuth {
			if authMap, ok := authVars.(map[interface{}]interface{}); ok {
				if username, exists := authMap["username"]; exists {
					templateVars["username"] = fmt.Sprintf("%v", username)
				}
				if host, exists := authMap["host"]; exists {
					templateVars["host"] = fmt.Sprintf("%v", host)
				}
				if port, exists := authMap["port"]; exists {
					templateVars["port"] = fmt.Sprintf("%v", port)
				}
				if privateKey, exists := authMap["privateKey"]; exists {
					templateVars["privateKey"] = fmt.Sprintf("%v", privateKey)
				}
				if password, exists := authMap["password"]; exists {
					templateVars["password"] = fmt.Sprintf("%v", password)
				}
				if remotePath, exists := authMap["remotePath"]; exists {
					templateVars["remotePath"] = fmt.Sprintf("%v", remotePath)
				}
			}
		}
	}

	// Replace all template variables
	for key, value := range templateVars {
		// Replace =key with actual value
		pattern := fmt.Sprintf("=%s", key)
		configText = strings.ReplaceAll(configText, pattern, value)
	}

	// Write back the rendered config
	err = os.WriteFile(ConfigFileName, []byte(configText), 0644)
	if err != nil {
		return fmt.Errorf("error writing rendered config: %v", err)
	}

	return nil
}

// LoadAndRenderConfig loads config, validates it, and renders template variables in memory
func LoadAndRenderConfig() (*Config, error) {
	// First load and validate
	cfg, err := LoadAndValidateConfig()
	if err != nil {
		return nil, err
	}

	// Then render template variables in memory (advanced)
	renderedCfg, err := RenderTemplateVariablesInMemory(cfg)
	if err != nil {
		return nil, fmt.Errorf("template rendering failed: %v", err)
	}

	basPath, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("error getting current working directory: %v", err)
	}
	realPath, err := filepath.EvalSymlinks(basPath)
	if err != nil {
		printer.Println("Error:", err)
		os.Exit(1)
	}
	absWatchPath, err := filepath.Abs(realPath)
	if err != nil {
		return nil, fmt.Errorf("error resolving absolute path: %v", err)
	}
	// Ensure LocalPath is set to the working directory for pipeline operations
	renderedCfg.LocalPath = absWatchPath

	return renderedCfg, nil
}

// RenderTemplateVariablesInMemory renders template variables without writing to file
func RenderTemplateVariablesInMemory(cfg *Config) (*Config, error) {
	// Create a copy of the config for rendering
	renderedCfg := *cfg

	// Create advanced renderer
	renderer := NewAdvancedTemplateRenderer(&renderedCfg)

	renderCount := 0

	// Render SSH configs
	for i := range renderedCfg.DirectAccess.SSHConfigs {
		sshConfig := &renderedCfg.DirectAccess.SSHConfigs[i]

		// Render each field that might contain template variables
		if hostName, ok := (*sshConfig)["HostName"].(string); ok && strings.HasPrefix(hostName, "=") {
			oldValue := hostName
			(*sshConfig)["HostName"] = renderer.RenderComplexTemplates(hostName)
			printer.Printf("🔧 Rendered SSH config[%d].HostName: %s → %s\n", i, oldValue, (*sshConfig)["HostName"])
			renderCount++
		}
		if user, ok := (*sshConfig)["User"].(string); ok && strings.HasPrefix(user, "=") {
			oldValue := user
			(*sshConfig)["User"] = renderer.RenderComplexTemplates(user)
			printer.Printf("🔧 Rendered SSH config[%d].User: %s → %s\n", i, oldValue, (*sshConfig)["User"])
			renderCount++
		}
		if port, ok := (*sshConfig)["Port"].(string); ok && strings.HasPrefix(port, "=") {
			oldValue := port
			(*sshConfig)["Port"] = renderer.RenderComplexTemplates(port)
			printer.Printf("🔧 Rendered SSH config[%d].Port: %s → %s\n", i, oldValue, (*sshConfig)["Port"])
			renderCount++
		}
		if identityFile, ok := (*sshConfig)["IdentityFile"].(string); ok && strings.HasPrefix(identityFile, "=") {
			oldValue := identityFile
			(*sshConfig)["IdentityFile"] = renderer.RenderComplexTemplates(identityFile)
			printer.Printf("🔧 Rendered SSH config[%d].IdentityFile: %s → %s\n", i, oldValue, (*sshConfig)["IdentityFile"])
			renderCount++

			// Fix SSH key permissions after rendering
			finalIdentityFile := (*sshConfig)["IdentityFile"].(string)
			if strings.TrimSpace(finalIdentityFile) != "" && !strings.HasPrefix(finalIdentityFile, "=") {
				printer.Printf("🔑 Fixing permissions for SSH config[%d] IdentityFile: %s\n", i, finalIdentityFile)
				if err := fixSSHKeyPermissions(finalIdentityFile, 0700); err != nil {
					printer.Printf("⚠️  Failed to fix SSH key permissions for %s: %v\n", finalIdentityFile, err)
				}
			}
		}
		if remoteCommand, ok := (*sshConfig)["RemoteCommand"].(string); ok && strings.Contains(remoteCommand, "=") {
			oldValue := remoteCommand
			(*sshConfig)["RemoteCommand"] = renderer.RenderComplexTemplates(remoteCommand)
			printer.Printf("🔧 Rendered SSH config[%d].RemoteCommand: %s → %s\n", i, oldValue, (*sshConfig)["RemoteCommand"])
			renderCount++
		}
		if proxyCommand, ok := (*sshConfig)["ProxyCommand"].(string); ok && strings.Contains(proxyCommand, "=") {
			oldValue := proxyCommand
			(*sshConfig)["ProxyCommand"] = renderer.RenderComplexTemplates(proxyCommand)
			printer.Printf("🔧 Rendered SSH config[%d].ProxyCommand: %s → %s\n", i, oldValue, (*sshConfig)["ProxyCommand"])
			renderCount++
		}
	}

	// Devsync-specific auth rendering removed. For pipeline configs, SSH
	// related templating is handled through DirectAccess.SSHConfigs and
	// DirectAccess.SSHCommands (rendered above).

	// Render SSH commands
	for i := range renderedCfg.DirectAccess.SSHCommands {
		sshCmd := &renderedCfg.DirectAccess.SSHCommands[i]

		// Render access_name and command
		if strings.Contains(sshCmd.AccessName, "=") {
			oldValue := sshCmd.AccessName
			sshCmd.AccessName = renderer.RenderComplexTemplates(sshCmd.AccessName)
			printer.Printf("🔧 Rendered SSH command[%d].AccessName: %s → %s\n", i, oldValue, sshCmd.AccessName)
			renderCount++
		}
		if strings.Contains(sshCmd.Command, "=") {
			oldValue := sshCmd.Command
			sshCmd.Command = renderer.RenderComplexTemplates(sshCmd.Command)
			printer.Printf("🔧 Rendered SSH command[%d].Command: %s → %s\n", i, oldValue, sshCmd.Command)
			renderCount++
		}
	}

	if renderCount > 0 {
		printer.Printf("✅ Template rendering completed: %d references resolved\n", renderCount)
	} else {
		// Only show "no template references" message if this is NOT a pipeline context
		// Pipeline executions use a different variable system (vars.yaml, execution.variables)
		// so template references are not relevant for them
		if len(renderedCfg.DirectAccess.Executions) == 0 {
			printer.Println("ℹ️  No template references found in configuration")
		}
	}

	// Fix SSH key permissions for all IdentityFile entries in SSH configs after rendering
	for i, sshConfig := range renderedCfg.DirectAccess.SSHConfigs {
		if identityFile, ok := sshConfig["IdentityFile"].(string); ok && strings.TrimSpace(identityFile) != "" && !strings.HasPrefix(identityFile, "=") {
			printer.Printf("🔑 Fixing permissions for SSH config[%d] IdentityFile: %s\n", i, identityFile)
			if err := fixSSHKeyPermissions(identityFile, 0700); err != nil {
				printer.Printf("⚠️  Failed to fix SSH key permissions for %s: %v\n", identityFile, err)
			}
		}
	}

	return &renderedCfg, nil
} // AdvancedTemplateRenderer handles complex template rendering with nested properties
type AdvancedTemplateRenderer struct {
	config *Config
}

// NewAdvancedTemplateRenderer creates a new template renderer
func NewAdvancedTemplateRenderer(cfg *Config) *AdvancedTemplateRenderer {
	return &AdvancedTemplateRenderer{config: cfg}
}

// RenderComplexTemplates renders complex template expressions like =field.nested.value
func (r *AdvancedTemplateRenderer) RenderComplexTemplates(text string) string {
	// Find all template expressions starting with =
	re := regexp.MustCompile(`=([a-zA-Z_][a-zA-Z0-9_.[\]]*)`)
	return re.ReplaceAllStringFunc(text, func(match string) string {
		// Remove the = prefix to get the path
		path := strings.TrimPrefix(match, "=")

		// Resolve the nested property
		value, err := r.resolveNestedProperty(path)
		if err != nil {
			// Return original if resolution fails
			return match
		}

		return value
	})
}

// resolveNestedProperty resolves nested properties like "devsync.os_target" or "var.one.two"
func (r *AdvancedTemplateRenderer) resolveNestedProperty(path string) (string, error) {
	parts := strings.Split(path, ".")

	// Start with the root config
	current := reflect.ValueOf(r.config)

	// Handle special case for 'var' field
	if parts[0] == "var" && len(parts) > 1 {
		return r.resolveVarProperty(parts[1:])
	}

	// Handle array access like ssh_configs[0]
	if strings.Contains(parts[0], "[") {
		return r.resolveArrayProperty(parts[0])
	}

	// Navigate through nested properties
	for _, part := range parts {
		if strings.Contains(part, "[") {
			// Handle array access in nested property
			currentIndex := len(parts) - 1
			for i, p := range parts {
				if p == part {
					currentIndex = i
					break
				}
			}
			arrayPath := strings.Join(parts[:currentIndex+1], ".")
			return r.resolveNestedArrayProperty(arrayPath)
		}

		// Get the field value
		current = r.getFieldValue(current, part)
		if !current.IsValid() {
			return "", fmt.Errorf("property not found: %s", part)
		}
	}

	// Convert final value to string
	return r.valueToString(current), nil
}

// resolveVarProperty resolves var.* properties like var.one.two
func (r *AdvancedTemplateRenderer) resolveVarProperty(parts []string) (string, error) {
	if r.config.Var == nil {
		return "", fmt.Errorf("var section not found in config")
	}

	current := r.config.Var

	for i, part := range parts {
		if value, exists := current[part]; exists {
			if i == len(parts)-1 {
				// Last part, return the value
				switch v := value.(type) {
				case string:
					return v, nil
				case int:
					return strconv.Itoa(v), nil
				case float64:
					return strconv.FormatFloat(v, 'f', -1, 64), nil
				case bool:
					return strconv.FormatBool(v), nil
				default:
					return fmt.Sprintf("%v", v), nil
				}
			} else {
				// Not the last part, continue navigating
				if nextMap, ok := value.(map[string]interface{}); ok {
					current = nextMap
				} else {
					return "", fmt.Errorf("cannot navigate into non-map value at %s", strings.Join(parts[:i+1], "."))
				}
			}
		} else {
			return "", fmt.Errorf("var property not found: %s", strings.Join(parts[:i+1], "."))
		}
	}

	return "", fmt.Errorf("unexpected end of var resolution")
}

// resolveArrayProperty resolves array properties like "ssh_configs[0]"
func (r *AdvancedTemplateRenderer) resolveArrayProperty(path string) (string, error) {
	re := regexp.MustCompile(`^([a-zA-Z_][a-zA-Z0-9_]*)\[(\d+)\]$`)
	matches := re.FindStringSubmatch(path)

	if len(matches) != 3 {
		return "", fmt.Errorf("invalid array syntax: %s", path)
	}

	arrayName := matches[1]
	index, _ := strconv.Atoi(matches[2])

	// Get the array field
	arrayValue := r.getFieldValue(reflect.ValueOf(r.config), arrayName)
	if !arrayValue.IsValid() || arrayValue.Kind() != reflect.Slice {
		return "", fmt.Errorf("array not found: %s", arrayName)
	}

	// Check bounds
	if index < 0 || index >= arrayValue.Len() {
		return "", fmt.Errorf("array index out of bounds: %s[%d]", arrayName, index)
	}

	// Get the element
	element := arrayValue.Index(index)
	return r.valueToString(element), nil
}

// resolveNestedArrayProperty resolves nested array properties like "direct_access.ssh_configs[0].host"
func (r *AdvancedTemplateRenderer) resolveNestedArrayProperty(path string) (string, error) {
	// This is a simplified implementation - in production you'd want more robust parsing
	parts := strings.Split(path, ".")
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid nested array path: %s", path)
	}

	// For now, return the original path if it's too complex
	return "=" + path, nil
}

// getFieldValue gets a field value from a struct using reflection
func (r *AdvancedTemplateRenderer) getFieldValue(v reflect.Value, fieldName string) reflect.Value {
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	if v.Kind() != reflect.Struct {
		return reflect.Value{}
	}

	// Try with title case first (for exported fields)
	titleField := strings.ToUpper(fieldName[:1]) + fieldName[1:]
	field := v.FieldByName(titleField)
	if field.IsValid() {
		return field
	}

	// Try with exact case
	field = v.FieldByName(fieldName)
	return field
}

// valueToString converts a reflect.Value to string
func (r *AdvancedTemplateRenderer) valueToString(v reflect.Value) string {
	if !v.IsValid() {
		return ""
	}

	switch v.Kind() {
	case reflect.String:
		return v.String()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(v.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(v.Uint(), 10)
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(v.Float(), 'f', -1, 64)
	case reflect.Bool:
		return strconv.FormatBool(v.Bool())
	default:
		// For complex types, convert to string representation
		if v.CanInterface() {
			return fmt.Sprintf("%v", v.Interface())
		}
		return ""
	}
}

// RenderTemplateVariablesAdvanced renders template variables with advanced nested property support
func RenderTemplateVariablesAdvanced(cfg *Config) error {
	// Read the current config file as raw text
	data, err := os.ReadFile(ConfigFileName)
	if err != nil {
		return fmt.Errorf("error reading config file for template rendering: %v", err)
	}

	// Create advanced renderer
	renderer := NewAdvancedTemplateRenderer(cfg)

	// Render complex templates
	configText := string(data)
	configText = renderer.RenderComplexTemplates(configText)

	// Write back the rendered config
	err = os.WriteFile(ConfigFileName, []byte(configText), 0644)
	if err != nil {
		return fmt.Errorf("error writing rendered config: %v", err)
	}

	return nil
}

func ConfigExists() bool {
	_, err := os.Stat(ConfigFileName)
	return !os.IsNotExist(err)
}

func GetConfigPath() string {
	cwd, _ := os.Getwd()
	return filepath.Join(cwd, ConfigFileName)
}

// loadDotEnvIfExists attempts to load a .env file from the directory of config
// and returns a map of key->value. If no .env exists or parsing fails, an empty map is returned.
func loadDotEnvIfExists(dir string) (map[string]string, error) {
	envPath := filepath.Join(dir, ".env")
	if _, err := os.Stat(envPath); os.IsNotExist(err) {
		return map[string]string{}, nil
	}

	m, err := godotenv.Read(envPath)
	if err != nil {
		printer.Printf("⚠️  Failed to parse .env at %s: %v\n", envPath, err)
		return map[string]string{}, err
	}
	return m, nil
}

// interpolateEnv replaces ${VAR} occurrences in the input text. Precedence: OS env > envMap.
// Missing variables are replaced with empty string and a warning is emitted.
func interpolateEnv(input string, envMap map[string]string) string {
	lookup := func(varName string) string {
		if v := os.Getenv(varName); v != "" {
			return v
		}
		if v, ok := envMap[varName]; ok {
			return v
		}
		// warn about missing but return empty
		printer.Printf("⚠️  Environment variable %s not set; using empty string\n", varName)
		return ""
	}

	// Use os.Expand to replace ${VAR} and $VAR
	return os.Expand(input, func(name string) string {
		// os.Expand passes the variable name without braces
		return lookup(name)
	})
}

// renderVarMap resolves string entries inside vars map using the provided renderer.
// It performs iterative passes to resolve transitive =var.* references and detects cycles.
func renderVarMap(renderer *AdvancedTemplateRenderer, vars map[string]interface{}) error {
	if vars == nil {
		return nil
	}

	const maxPasses = 10

	for pass := 0; pass < maxPasses; pass++ {
		changed := false

		for k, v := range vars {
			switch val := v.(type) {
			case string:
				// Only render when it contains = (RenderComplexTemplates will ignore otherwise)
				if strings.Contains(val, "=") {
					rendered := renderer.RenderComplexTemplates(val)
					if rendered != val {
						vars[k] = rendered
						changed = true
					}
				}
			case map[string]interface{}:
				// recurse into nested maps
				if err := renderVarMap(renderer, val); err != nil {
					return err
				}
			}
		}

		if !changed {
			return nil
		}
	}

	return fmt.Errorf("max passes reached while resolving var entries (possible cycle)")
}

// walkAndRenderNode traverses a YAML node tree and renders scalar nodes that contain
// =... template references using the provided renderer.
func walkAndRenderNode(node *yaml.Node, renderer *AdvancedTemplateRenderer) error {
	if node == nil {
		return nil
	}

	// If this is a scalar node, try to render it
	if node.Kind == yaml.ScalarNode {
		if strings.Contains(node.Value, "=") {
			newVal := renderer.RenderComplexTemplates(node.Value)
			node.Value = newVal
			node.Tag = "!!str"
		}
		return nil
	}

	// Recurse on sequence and mapping nodes
	for i := 0; i < len(node.Content); i++ {
		if err := walkAndRenderNode(node.Content[i], renderer); err != nil {
			return err
		}
	}

	return nil
}

// LoadAndRenderConfigWithPath loads and renders config from a specific path
func LoadAndRenderConfigWithPath(configPath string) (*Config, error) {
	// First load and validate
	cfg, err := LoadAndValidateConfigWithPath(configPath)
	if err != nil {
		return nil, err
	}

	// Then render template variables in memory (advanced)
	renderedCfg, err := RenderTemplateVariablesInMemory(cfg)
	if err != nil {
		return nil, fmt.Errorf("template rendering failed: %v", err)
	}

	basPath, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("error getting current working directory: %v", err)
	}
	realPath, err := filepath.EvalSymlinks(basPath)
	if err != nil {
		printer.Println("Error:", err)
		os.Exit(1)
	}
	absWatchPath, err := filepath.Abs(realPath)
	if err != nil {
		return nil, fmt.Errorf("error resolving absolute path: %v", err)
	}
	// Ensure LocalPath is set to the working directory for pipeline operations
	renderedCfg.LocalPath = absWatchPath

	return renderedCfg, nil
}

// LoadAndValidateConfigWithPath loads and validates config from a specific path
func LoadAndValidateConfigWithPath(configPath string) (*Config, error) {
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("%s not found", configPath)
	}

	// Read raw config file
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("error reading config file: %v", err)
	}

	// Load .env if exists (same dir as config file)
	cfgDir := filepath.Dir(configPath)
	envMap, _ := loadDotEnvIfExists(cfgDir)

	// Interpolate ${VAR} using OS env first, then .env values
	rendered := interpolateEnv(string(data), envMap)

	// First unmarshal into a temporary config to access cfg.Var for var-based refs
	var tmpCfg Config
	if err := yaml.Unmarshal([]byte(rendered), &tmpCfg); err != nil {
		return nil, fmt.Errorf("error parsing config file (initial unmarshal): %v", err)
	}

	// Create renderer backed by tmpCfg to resolve =var.* inside var map
	renderer := NewAdvancedTemplateRenderer(&tmpCfg)

	// Resolve var entries iteratively (to handle transitive =var.* references)
	if err := renderVarMap(renderer, tmpCfg.Var); err != nil {
		return nil, fmt.Errorf("error resolving var entries: %v", err)
	}

	// Now parse YAML into node tree and render =... expressions using renderer
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(rendered), &root); err != nil {
		return nil, fmt.Errorf("error parsing config file to node: %v", err)
	}

	if err := walkAndRenderNode(&root, renderer); err != nil {
		return nil, fmt.Errorf("error rendering config nodes: %v", err)
	}

	// Encode node back to YAML bytes
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&root); err != nil {
		return nil, fmt.Errorf("error encoding rendered config: %v", err)
	}

	// Final unmarshal into Config struct
	var cfg Config
	if err := yaml.Unmarshal(buf.Bytes(), &cfg); err != nil {
		return nil, fmt.Errorf("error parsing rendered config: %v", err)
	}

	// Validate the config
	if err := ValidateConfig(&cfg); err != nil {
		return nil, fmt.Errorf("config validation failed: %v", err)
	}

	return &cfg, nil
}

// LoadAndRenderConfigForPipeline loads config for pipeline tool, preferring pipeline.yaml over legacy configs
func LoadAndRenderConfigForPipeline() (*Config, error) {
	// Try pipeline.yaml first
	if _, err := os.Stat("pipeline.yaml"); err == nil {
		return LoadAndRenderConfigWithPath("pipeline.yaml")
	}

	// Fall back to legacy make-sync.yaml for backward compatibility
	return LoadAndRenderConfig()
}

// LocalConfig manages local configuration for agent operations
type LocalConfig struct {
	// Add any local config fields here if needed in the future
}

// GetAgentBinaryName returns a unique binary name for the agent based on target OS
func (lc *LocalConfig) GetAgentBinaryName(targetOS string) string {
	switch strings.ToLower(targetOS) {
	case "windows":
		return "pipeline-agent.exe"
	case "linux", "darwin":
		return "pipeline-agent"
	default:
		return "pipeline-agent"
	}
}

// GetOrCreateLocalConfig creates or returns the local configuration instance
func GetOrCreateLocalConfig() (*LocalConfig, error) {
	// For now, just return a new instance
	// In the future, this could load from a local config file or cache
	return &LocalConfig{}, nil
}
