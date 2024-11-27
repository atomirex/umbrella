package razor

import (
	"sync"
	"time"
)

type LockAndNotify struct {
	mutex sync.Mutex

	waker   chan struct{}
	waiting chan bool
	waiter  chan struct{}
}

func NewLockAndNotify() *LockAndNotify {
	waker := make(chan struct{}, 16)
	waiting := make(chan bool, 1)
	waiter := make(chan struct{}, 1)

	go func() {
		iswaiting := false
		for {
			select {
			case is := <-waiting:
				iswaiting = is
			case <-waker:
				if iswaiting {
					iswaiting = false
					waiter <- struct{}{}
				}
			}
		}
	}()

	return &LockAndNotify{
		waker:   waker,
		waiting: waiting,
		waiter:  waiter,
	}
}

func (l *LockAndNotify) Enter() {
	l.mutex.Lock()
}

func (l *LockAndNotify) Leave() {
	l.mutex.Unlock()
}

// This is really dangerous if we've aborted!
func (l *LockAndNotify) Notify() {
	l.waker <- struct{}{}
}

func (l *LockAndNotify) Wait(delay time.Duration) {
	l.waiting <- true

	select {
	case <-time.After(delay):
	case <-l.waiter:
	}

	l.waiting <- false
}
