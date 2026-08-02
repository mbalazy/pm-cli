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
	// Landing statuses = where the executor parks a verify-green sub
	// (done_status / done_status_independent). Ask for these instead of
	// comparing a status to the literal "merged".
	GetLandingStatuses(slug string) []TaskStatus
	GetAllLandingStatuses() []TaskStatus
	ProjectPrefix(slug string) string
	ResolveProject(input string) (string, error)

	// Projects - write. All three self-lock; never call them while holding
	// LockProject. MutateProject is the one to use for a partial-field edit
	// (it re-reads the project fresh inside the critical section).
	CreateProject(slug string, p *Project) error
	UpdateProject(slug string, p *Project) error
	MutateProject(slug string, fn func(*Project) error) (*Project, error)

	// Tasks - read
	GetTasks(projectSlug string) ([]*Task, error)
	GetAllTasks() ([]*Task, error)
	NextTaskID(slug string) string
	NextChildID(slug, parentID string) string
	FindTask(projectSlug, query string) (*Task, error)
	FindTaskExact(projectSlug, taskID string) (*Task, error)

	// Tasks - write
	AddTask(projectSlug string, t *Task) error
	WriteTask(t *Task) error
	MoveTask(t *Task, newStatus TaskStatus) error
	DeleteTask(t *Task) error

	// Concurrency: exclusive cross-process lock for read-modify-write cycles
	// on the project's task files (see Store.LockProject). Hold around
	// FindTask -> mutate -> WriteTask; read the task FRESH inside.
	LockProject(slug string) (func(), error)
}
