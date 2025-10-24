package syncdata

// Config represents the configuration structure needed by syncdata
type Config struct {
	LocalPath string `yaml:"localPath"`
	Devsync   struct {
		Auth struct {
			Username   string `yaml:"username"`
			PrivateKey string `yaml:"privateKey"`
			Password   string `yaml:"password"`
			Host       string `yaml:"host"`
			Port       string `yaml:"port"`
			RemotePath string `yaml:"remotePath"`
			LocalPath  string `yaml:"localPath"`
		} `yaml:"auth"`
		Ignores        []string `yaml:"ignores"`
		AgentWatchs    []string `yaml:"agent_watchs"`
		ManualTransfer []string `yaml:"manual_transfer"`
		OSTarget       string   `yaml:"os_target"`
	} `yaml:"devsync"`
}

// SSHClient defines the interface for SSH operations
type SSHClient interface {
	Connect() error
	Close() error
	UploadFile(localPath, remotePath string) error
	DownloadFile(localPath, remotePath string) error
	RunCommand(cmd string) error
	RunCommandWithOutput(cmd string) (string, error)
	RunCommandWithStream(cmd string, showOutput bool) (<-chan string, <-chan error, error)
	SyncFile(localPath, remotePath string) error
}

// Printer defines the interface for printing utilities
type Printer interface {
	Printf(format string, args ...interface{})
	Println(args ...interface{})
}

// LocalConfig manages local configuration for agent operations
type LocalConfig struct {
	// Add any local config fields here if needed in the future
}

// GetAgentBinaryName returns a unique binary name for the agent based on target OS
func (lc *LocalConfig) GetAgentBinaryName(targetOS string) string {
	switch targetOS {
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
