package types

// Execution represents a predefined execution profile
type Execution struct {
	Name      string                 `yaml:"name"`
	Key       string                 `yaml:"key"`
	Pipeline  string                 `yaml:"pipeline"`
	Jobs      []string               `yaml:"jobs,omitempty"`
	Var       string                 `yaml:"var"`                 // Reference to vars.yaml key (existing system)
	Variables map[string]interface{} `yaml:"variables,omitempty"` // Direct variables definition (new feature)
	Hosts     []string               `yaml:"hosts"`
}

// Pipeline represents a pipeline configuration
type Pipeline struct {
	Name             string            `yaml:"name"`
	Jobs             []Job             `yaml:"jobs"`
	StrictVariables  bool              `yaml:"strict_variables,omitempty"` // error on undefined variables if true
	ContextVariables map[string]string `yaml:"-"`                          // runtime context variables (not serialized)
	LogOutput        *bool             `yaml:"log_output,omitempty"`       // optional: enable logging for entire pipeline
}

// Job represents a job within a pipeline
type Job struct {
	Name      string   `yaml:"name"`
	DependsOn []string `yaml:"depends_on,omitempty"`
	Mode      string   `yaml:"mode,omitempty"` // "local" or "remote" (default: "remote")
	Steps     []Step   `yaml:"steps"`
	LogOutput *bool    `yaml:"log_output,omitempty"` // optional: enable logging for this job
}

// Step represents a step within a job
type Step struct {
	Name        string      `yaml:"name"`
	Type        string      `yaml:"type,omitempty"`         // "command" (default), "file_transfer", "script"
	Commands    []string    `yaml:"commands,omitempty"`     // for command type
	File        string      `yaml:"file,omitempty"`         // for script/file_transfer
	Source      string      `yaml:"source,omitempty"`       // for file_transfer (deprecated when using `files`)
	Sources     []string    `yaml:"sources,omitempty"`      // optional: multiple sources for file_transfer
	Destination string      `yaml:"destination,omitempty"`  // for file_transfer
	Files       []FileEntry `yaml:"files,omitempty"`        // per-file entries: {source,destination,template}
	Direction   string      `yaml:"direction,omitempty"`    // "upload" (default) or "download" for file_transfer
	Template    string      `yaml:"template,omitempty"`     // "enabled" to render {{variables}} in file content
	Conditions  []Condition `yaml:"conditions,omitempty"`   // conditional execution based on output
	Expect      []Expect    `yaml:"expect,omitempty"`       // interactive prompt responses
	WorkingDir  string      `yaml:"working_dir,omitempty"`  // override working directory for this step
	Timeout     int         `yaml:"timeout,omitempty"`      // timeout in seconds (default 0 = unlimited)
	IdleTimeout int         `yaml:"idle_timeout,omitempty"` // idle timeout in seconds (default 600 = 10 minutes)
	SaveOutput  string      `yaml:"save_output,omitempty"`  // save command output to context variable
	Silent      bool        `yaml:"silent,omitempty"`       // suppress real-time output display
	ElseAction  string      `yaml:"else_action,omitempty"`  // action if no conditions match: "continue", "drop", "goto_step", "goto_job", "fail"
	ElseStep    string      `yaml:"else_step,omitempty"`    // target step name for else goto_step
	ElseJob     string      `yaml:"else_job,omitempty"`     // target job name for else goto_job
	LogOutput   *bool       `yaml:"log_output,omitempty"`   // optional: enable logging for this step
}

// FileEntry represents a per-file transfer instruction inside a file_transfer step
type FileEntry struct {
	Source      string `yaml:"source"`
	Destination string `yaml:"destination,omitempty"`
	Template    string `yaml:"template,omitempty"` // optional per-file override
}

// Condition represents a conditional check on command output
type Condition struct {
	Pattern string `yaml:"pattern"`        // regex pattern to match in output
	Action  string `yaml:"action"`         // "continue", "drop", "goto_step", "goto_job", "fail"
	Step    string `yaml:"step,omitempty"` // target step name for goto_step
	Job     string `yaml:"job,omitempty"`  // target job name for goto_job
}

// Expect represents an expected prompt and response
type Expect struct {
	Prompt   string `yaml:"prompt"`   // expected prompt text
	Response string `yaml:"response"` // response to send
}

// Vars represents variables for an environment
type Vars map[string]interface{}
