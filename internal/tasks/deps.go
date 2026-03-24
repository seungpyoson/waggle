package tasks

import (
	"encoding/json"
	"fmt"
	"time"
)

// ValidateDeps validates that dependencies exist and don't create cycles
// selfID is the task being updated (0 for new tasks)
func ValidateDeps(s *Store, dependsOn []int64, selfID int64) error {
	// Verify all dependencies exist
	for _, depID := range dependsOn {
		_, err := s.Get(depID)
		if err != nil {
			return fmt.Errorf("dependency task %d not found", depID)
		}
	}
	
	// Skip cycle check for new tasks (selfID == 0)
	if selfID == 0 {
		return nil
	}
	
	// Check for cycles using DFS
	visited := make(map[int64]bool)
	for _, depID := range dependsOn {
		if hasCycle(s, depID, selfID, visited) {
			return fmt.Errorf("dependency cycle detected")
		}
	}
	
	return nil
}

// hasCycle performs DFS to detect if target appears in the dependency chain of start
func hasCycle(s *Store, start, target int64, visited map[int64]bool) bool {
	if start == target {
		return true
	}
	
	if visited[start] {
		return false
	}
	visited[start] = true
	
	// Get the task and check its dependencies
	task, err := s.Get(start)
	if err != nil {
		return false
	}
	
	for _, depID := range task.DependsOn {
		if hasCycle(s, depID, target, visited) {
			return true
		}
	}
	
	return false
}

// ResolveDeps finds and unblocks tasks that were waiting on completedID
// Returns the list of task IDs that were unblocked
func ResolveDeps(s *Store, completedID int64) ([]int64, error) {
	// Find all blocked tasks that depend on completedID
	// Use json_each() to properly query the JSON array
	query := `
		SELECT DISTINCT t.id, t.depends_on
		FROM tasks t, json_each(t.depends_on) j
		WHERE t.state = 'pending' AND t.blocked = 1 AND j.value = ?
	`

	rows, err := s.db.Query(query, completedID)
	if err != nil {
		return nil, err
	}

	// Collect all candidates first (avoid nested queries)
	type candidate struct {
		taskID    int64
		dependsOn []int64
	}
	var candidates []candidate

	for rows.Next() {
		var taskID int64
		var dependsOnJSON string

		if err := rows.Scan(&taskID, &dependsOnJSON); err != nil {
			rows.Close()
			return nil, err
		}

		// Parse the depends_on array
		var dependsOn []int64
		if err := json.Unmarshal([]byte(dependsOnJSON), &dependsOn); err != nil {
			rows.Close()
			return nil, err
		}

		candidates = append(candidates, candidate{taskID: taskID, dependsOn: dependsOn})
	}
	rows.Close()

	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Now check each candidate
	var unblocked []int64
	now := time.Now().UTC().Format(time.RFC3339)

	for _, c := range candidates {
		// Check if all dependencies are completed
		allCompleted := true
		for _, depID := range c.dependsOn {
			dep, err := s.Get(depID)
			if err != nil {
				return nil, err
			}
			if dep.State != StateCompleted {
				allCompleted = false
				break
			}
		}

		// If all dependencies are completed, unblock the task
		if allCompleted {
			_, err := s.db.Exec(`
				UPDATE tasks
				SET blocked = 0, updated_at = ?
				WHERE id = ?
			`, now, c.taskID)
			if err != nil {
				return nil, err
			}
			unblocked = append(unblocked, c.taskID)
		}
	}

	return unblocked, nil
}

// FailDependents finds and fails all tasks that depend on failedID
// Returns the list of task IDs that were failed
func FailDependents(s *Store, failedID int64) ([]int64, error) {
	// Find all blocked tasks that depend on failedID
	// Use json_each() to properly query the JSON array
	query := `
		SELECT DISTINCT t.id
		FROM tasks t, json_each(t.depends_on) j
		WHERE t.state = 'pending' AND t.blocked = 1 AND j.value = ?
	`

	rows, err := s.db.Query(query, failedID)
	if err != nil {
		return nil, err
	}

	// Collect all task IDs first (avoid nested queries)
	var taskIDs []int64
	for rows.Next() {
		var taskID int64
		if err := rows.Scan(&taskID); err != nil {
			rows.Close()
			return nil, err
		}
		taskIDs = append(taskIDs, taskID)
	}
	rows.Close()

	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Now update all the tasks
	now := time.Now().UTC().Format(time.RFC3339)
	for _, taskID := range taskIDs {
		_, err := s.db.Exec(`
			UPDATE tasks
			SET state = 'failed', failure_reason = 'dependency_failed', updated_at = ?
			WHERE id = ?
		`, now, taskID)
		if err != nil {
			return nil, err
		}
	}

	return taskIDs, nil
}

