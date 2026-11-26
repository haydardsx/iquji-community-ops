package application

type TaskAlreadyActiveError struct {
	ActiveTaskID string
}

func (e *TaskAlreadyActiveError) Error() string {
	return "TASK_ALREADY_ACTIVE"
}
