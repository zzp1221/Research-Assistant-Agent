package scheduler

import "research-assistant-backend/internal/models"

func shouldBlockOnFailure(task *models.TaskNode) bool {
	_ = task
	return true
}
