package storage

// TaskStore is the interface for all storage operations.
// Store is the concrete local-filesystem implementation.
type TaskStore interface {
	// Paths
	RootDir() string
	Init() error
	ProjectDir(slug string) string
	ProjectYAML(slug string) string

	// Projects - read
	ListProjects() ([]string, error)
	ListActiveProjects() ([]string, error)
	GetProject(slug string) (*Project, error)
	GetProjectStatuses(slug string) []TaskStatus
	GetAllStatuses() []TaskStatus
	ProjectPrefix(slug string) string
	ResolveProject(input string) (string, error)

	// Projects - write
	CreateProject(slug string, p *Project) error
	UpdateProject(slug string, p *Project) error

	// Tasks - read
	GetTasks(projectSlug string) ([]*Task, error)
	GetAllTasks() ([]*Task, error)
	NextTaskID(slug string) string
	FindTask(projectSlug, query string) (*Task, error)

	// Tasks - write
	AddTask(projectSlug string, t *Task) error
	WriteTask(t *Task) error
	MoveTask(t *Task, newStatus TaskStatus) error
	DeleteTask(t *Task) error
}
