package tasks

import (
	"github.com/seungpyoson/waggle/internal/brokerstate"
	"github.com/seungpyoson/waggle/internal/brokerstate/statetest"
	"testing"
	"time"
)

type testStore struct {
	owner *brokerstate.Owner
	db    statetest.SQL
}

func newTestStore(t *testing.T) *testStore {
	o := statetest.New(t, Schema())
	return &testStore{owner: o, db: statetest.SQL{Owner: o}}
}
func (s *testStore) apply(f func(*Store) error) error {
	return statetest.Write(s.owner, func(tx *brokerstate.WriteTx) error { return f(NewStore(tx)) })
}
func (s *testStore) Create(params CreateParams) (v *Task, err error) {
	err = s.apply(func(store *Store) error { v, err = store.Create(params); return err })
	return
}
func (s *testStore) Get(id int64) (v *Task, err error) {
	err = s.apply(func(store *Store) error { v, err = store.Get(id); return err })
	return
}
func (s *testStore) Claim(worker string, filter ClaimFilter) (v *Task, err error) {
	err = s.apply(func(store *Store) error { v, err = store.Claim(worker, filter); return err })
	return
}
func (s *testStore) Complete(id int64, claimToken, result string) error {
	return s.apply(func(store *Store) error { return store.Complete(id, claimToken, result) })
}
func (s *testStore) Fail(id int64, claimToken, reason string) error {
	return s.apply(func(store *Store) error { return store.Fail(id, claimToken, reason) })
}
func (s *testStore) Cancel(id int64) error {
	return s.apply(func(store *Store) error { return store.Cancel(id) })
}
func (s *testStore) Heartbeat(id int64, claimToken string) error {
	return s.apply(func(store *Store) error { return store.Heartbeat(id, claimToken) })
}
func (s *testStore) List(filter ListFilter) (v []*Task, err error) {
	err = s.apply(func(store *Store) error { v, err = store.List(filter); return err })
	return
}
func (s *testStore) CancelExpiredTTL() (v int, err error) {
	err = s.apply(func(store *Store) error { v, err = store.CancelExpiredTTL(); return err })
	return
}
func (s *testStore) QueueHealth(staleThreshold time.Duration) (v *QueueHealth, err error) {
	err = s.apply(func(store *Store) error { v, err = store.QueueHealth(staleThreshold); return err })
	return
}
func (s *testStore) RequeueAllClaimed() (v int, err error) {
	err = s.apply(func(store *Store) error { v, err = store.RequeueAllClaimed(); return err })
	return
}
func (s *testStore) RequeueByOwner(owner string) (v int, err error) {
	err = s.apply(func(store *Store) error { v, err = store.RequeueByOwner(owner); return err })
	return
}
func (s *testStore) CountByState() (v map[string]int, err error) {
	err = s.apply(func(store *Store) error { v, err = store.CountByState(); return err })
	return
}
func (s *testStore) Update(id int64, params UpdateParams) error {
	return s.apply(func(store *Store) error { return store.Update(id, params) })
}
func (s *testStore) RequeueExpiredLeases() (v int, err error) {
	err = s.apply(func(store *Store) error { v, err = store.RequeueExpiredLeases(); return err })
	return
}
func (s *testStore) ValidateDeps(ids []int64, id int64) error {
	return s.apply(func(store *Store) error { return ValidateDeps(store, ids, id) })
}
func (s *testStore) ResolveDeps(id int64) (ids []int64, err error) {
	err = s.apply(func(store *Store) error { ids, err = ResolveDeps(store, id); return err })
	return
}
func (s *testStore) FailDependents(id int64) (ids []int64, err error) {
	err = s.apply(func(store *Store) error { ids, err = FailDependents(store, id); return err })
	return
}
