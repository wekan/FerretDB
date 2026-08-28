package oplog

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNotificationsBroadcastAndRenew(t *testing.T) {
	t.Parallel()

	b := &backend{changed: make(chan struct{})}
	first := b.Notifications()
	secondWaiter := b.Notifications()

	b.notify()

	select {
	case <-first:
	default:
		assert.Fail(t, "first waiter was not notified")
	}
	select {
	case <-secondWaiter:
	default:
		assert.Fail(t, "second waiter was not notified")
	}

	next := b.Notifications()
	select {
	case <-next:
		assert.Fail(t, "new notification generation is already closed")
	default:
	}
}
