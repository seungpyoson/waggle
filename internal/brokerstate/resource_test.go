package brokerstate

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"
)

type closeFunc func() error

func (f closeFunc) Close() error { return f() }

func TestResourceRequiresCurrentOwnerAndExpiresWithLifetime(t *testing.T) {
	o, _ := newOwner(t)
	other, _ := newOwner(t)
	var resource *Resource
	var expired Operation
	var closes atomic.Int32
	if err := o.Do(t.Context(), func(lifetime *Operation) error {
		expired = *lifetime
		var err error
		resource, err = lifetime.Open(t.Context(), func() (io.Closer, error) { return closeFunc(func() error { closes.Add(1); return nil }), nil })
		if err != nil {
			return err
		}
		if err := other.Do(t.Context(), func(foreign *Operation) error {
			return resource.Use(t.Context(), foreign, func() error { t.Error("foreign owner reached provider"); return nil })
		}); !errors.Is(err, ErrExpiredCapability) {
			t.Fatalf("foreign capability: %v", err)
		}
		return resource.Use(t.Context(), lifetime, func() error { return nil })
	}); err != nil {
		t.Fatal(err)
	}
	if closes.Load() != 1 {
		t.Fatal("owner did not close its resource")
	}
	copyOfResource := *resource
	if err := o.Do(t.Context(), func(op *Operation) error {
		return copyOfResource.Use(t.Context(), op, func() error { t.Error("closed resource reached provider"); return nil })
	}); !errors.Is(err, ErrExpiredCapability) {
		t.Fatal(err)
	}
	for _, op := range []*Operation{new(Operation), &expired} {
		if _, err := op.Open(t.Context(), func() (io.Closer, error) { t.Error("unowned factory ran"); return nil, nil }); !errors.Is(err, ErrExpiredCapability) {
			t.Fatal(err)
		}
	}
	if err := copyOfResource.Close(); err != nil || closes.Load() != 1 {
		t.Fatal("copy repeated native cleanup", err)
	}
}

func TestResourceFencesBeforeOpeningAndUsingProvider(t *testing.T) {
	o, _ := newOwner(t)
	if err := o.Do(t.Context(), func(op *Operation) error {
		r, err := op.Open(t.Context(), func() (io.Closer, error) { return closeFunc(func() error { return nil }), nil })
		if err != nil {
			return err
		}
		// Simulate a generation change on the real connection. Restore it only
		// so this isolated fixture can perform its ordinary shutdown afterward.
		if _, err := o.state.db.Exec("UPDATE broker_owner SET generation=generation+1"); err != nil {
			return err
		}
		defer o.state.db.Exec("UPDATE broker_owner SET generation=generation-1")
		if err := r.Use(t.Context(), op, func() error { t.Error("fenced native effect ran"); return nil }); !errors.Is(err, ErrFenced) {
			t.Fatal(err)
		}
		if _, err := op.Open(t.Context(), func() (io.Closer, error) { t.Error("fenced native factory ran"); return nil, nil }); !errors.Is(err, ErrFenced) {
			t.Fatal(err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestResourceCloseRemainsAdmittedUntilJoined(t *testing.T) {
	o, _ := newOwner(t)
	closing, join := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- o.Do(context.Background(), func(op *Operation) error {
			_, err := op.Open(context.Background(), func() (io.Closer, error) {
				return closeFunc(func() error { close(closing); <-join; return nil }), nil
			})
			return err
		})
	}()
	<-closing
	ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
	defer cancel()
	if err := o.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Error(err)
	}
	select {
	case <-o.state.drained:
		t.Error("ownership drained while provider close was executing")
	default:
	}
	var released int
	if err := o.state.db.QueryRow("SELECT released_at IS NOT NULL FROM broker_owner").Scan(&released); err != nil || released != 0 {
		t.Error("ownership released", err)
	}
	close(join)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := o.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
}
