package tasks

import "testing"

// TestDeps_AddDependency verifies that tasks with dependencies start blocked
func TestDeps_AddDependency(t *testing.T) {
	s := newTestStore(t)
	
	// Create a dependency task
	dep, err := s.Create(CreateParams{Payload: `{"dep":true}`})
	if err != nil {
		t.Fatal(err)
	}
	
	// Create a task that depends on it
	child, err := s.Create(CreateParams{
		Payload:   `{"child":true}`,
		DependsOn: []int64{dep.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	
	// Verify child is blocked
	if !child.Blocked {
		t.Error("child should be blocked when it has dependencies")
	}
	if child.State != StatePending {
		t.Errorf("state = %q, want %q", child.State, StatePending)
	}
	if len(child.DependsOn) != 1 || child.DependsOn[0] != dep.ID {
		t.Errorf("depends_on = %v, want [%d]", child.DependsOn, dep.ID)
	}
}

// TestDeps_DirectCycle verifies that direct cycles are detected
func TestDeps_DirectCycle(t *testing.T) {
	s := newTestStore(t)
	
	a, err := s.Create(CreateParams{Payload: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	
	// B depends on A — fine
	b, err := s.Create(CreateParams{
		Payload:   `{}`,
		DependsOn: []int64{a.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	
	// Try to make A depend on B — should fail (cycle: A -> B -> A)
	err = ValidateDeps(s, []int64{b.ID}, a.ID)
	if err == nil {
		t.Fatal("expected cycle detection error")
	}
}

// TestDeps_IndirectCycle verifies that indirect cycles are detected
func TestDeps_IndirectCycle(t *testing.T) {
	s := newTestStore(t)
	
	a, err := s.Create(CreateParams{Payload: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	
	b, err := s.Create(CreateParams{
		Payload:   `{}`,
		DependsOn: []int64{a.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	
	c, err := s.Create(CreateParams{
		Payload:   `{}`,
		DependsOn: []int64{b.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	
	// Try to make A depend on C — should fail (cycle: A -> B -> C -> A)
	err = ValidateDeps(s, []int64{c.ID}, a.ID)
	if err == nil {
		t.Fatal("expected cycle detection error for indirect cycle")
	}
}

// TestDeps_BlockedTaskNotClaimed verifies that blocked tasks cannot be claimed
func TestDeps_BlockedTaskNotClaimed(t *testing.T) {
	s := newTestStore(t)
	
	dep, err := s.Create(CreateParams{Payload: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	
	child, err := s.Create(CreateParams{
		Payload:   `{}`,
		DependsOn: []int64{dep.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	
	// Verify child is blocked
	if !child.Blocked {
		t.Fatal("child should be blocked")
	}
	
	// Try to claim — should get the dep, not the child
	claimed, err := s.Claim("worker", ClaimFilter{})
	if err != nil {
		t.Fatal(err)
	}
	
	if claimed.ID == child.ID {
		t.Error("blocked task should not be claimable")
	}
	if claimed.ID != dep.ID {
		t.Errorf("claimed task ID = %d, want %d (the dependency)", claimed.ID, dep.ID)
	}
}

// TestDeps_UnblockOnComplete verifies that completing a dependency unblocks waiting tasks
func TestDeps_UnblockOnComplete(t *testing.T) {
	s := newTestStore(t)
	
	dep, err := s.Create(CreateParams{Payload: `{"dep":true}`})
	if err != nil {
		t.Fatal(err)
	}
	
	child, err := s.Create(CreateParams{
		Payload:   `{"child":true}`,
		DependsOn: []int64{dep.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	
	// Verify child is blocked
	got, err := s.Get(child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Blocked {
		t.Fatal("child should be blocked")
	}
	
	// Claim and complete the dependency
	claimed, err := s.Claim("worker", ClaimFilter{})
	if err != nil {
		t.Fatal(err)
	}

	err = s.Complete(claimed.ID, claimed.ClaimToken, `{}`)
	if err != nil {
		t.Fatal(err)
	}

	// Resolve dependencies
	unblocked, err := ResolveDeps(s, dep.ID)
	if err != nil {
		t.Fatal(err)
	}

	if len(unblocked) != 1 || unblocked[0] != child.ID {
		t.Errorf("unblocked = %v, want [%d]", unblocked, child.ID)
	}

	// Verify child is now unblocked
	got, err = s.Get(child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Blocked {
		t.Error("child should be unblocked after dependency completes")
	}
}

// TestDeps_UnblockOnFail verifies that failing a dependency fails waiting tasks
func TestDeps_UnblockOnFail(t *testing.T) {
	s := newTestStore(t)

	dep, err := s.Create(CreateParams{Payload: `{}`})
	if err != nil {
		t.Fatal(err)
	}

	child, err := s.Create(CreateParams{
		Payload:   `{}`,
		DependsOn: []int64{dep.ID},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Claim and fail the dependency
	claimed, err := s.Claim("worker", ClaimFilter{})
	if err != nil {
		t.Fatal(err)
	}

	err = s.Fail(claimed.ID, claimed.ClaimToken, "error")
	if err != nil {
		t.Fatal(err)
	}

	// Fail dependents
	failed, err := FailDependents(s, dep.ID)
	if err != nil {
		t.Fatal(err)
	}

	if len(failed) != 1 || failed[0] != child.ID {
		t.Errorf("failed = %v, want [%d]", failed, child.ID)
	}

	// Verify child is now failed
	got, err := s.Get(child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateFailed {
		t.Errorf("state = %q, want %q", got.State, StateFailed)
	}
	if got.FailureReason != "dependency_failed" {
		t.Errorf("failure_reason = %q, want %q", got.FailureReason, "dependency_failed")
	}
}

// TestDeps_UnblockOnCancel verifies that canceling a dependency fails waiting tasks
func TestDeps_UnblockOnCancel(t *testing.T) {
	s := newTestStore(t)

	dep, err := s.Create(CreateParams{Payload: `{}`})
	if err != nil {
		t.Fatal(err)
	}

	child, err := s.Create(CreateParams{
		Payload:   `{}`,
		DependsOn: []int64{dep.ID},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Cancel the dependency
	err = s.Cancel(dep.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Fail dependents
	failed, err := FailDependents(s, dep.ID)
	if err != nil {
		t.Fatal(err)
	}

	if len(failed) != 1 || failed[0] != child.ID {
		t.Errorf("failed = %v, want [%d]", failed, child.ID)
	}

	// Verify child is now failed
	got, err := s.Get(child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateFailed {
		t.Errorf("state = %q, want %q", got.State, StateFailed)
	}
	if got.FailureReason != "dependency_failed" {
		t.Errorf("failure_reason = %q, want %q", got.FailureReason, "dependency_failed")
	}
}

