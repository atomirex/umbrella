package razor

import (
	"fmt"
	"time"
)

// Returns if the payload should be destroyed, which in golang doesn't do anything
type MessageHandlerCallback[MessageQueueWhat comparable, PayloadType any] func(what MessageQueueWhat, payload *PayloadType) bool

type MessageHandler[MessageQueueWhat comparable, PayloadType any] struct {
	label string

	queue    *MessageQueue[MessageQueueWhat, PayloadType]
	callback MessageHandlerCallback[MessageQueueWhat, PayloadType]
	lock     *LockAndNotify

	aborted bool

	logger *Logger
}

func NewMessageHandler[MessageQueueWhat comparable, PayloadType any](logger *Logger, label string, queueLength uint16, callback MessageHandlerCallback[MessageQueueWhat, PayloadType]) *MessageHandler[MessageQueueWhat, PayloadType] {
	return &MessageHandler[MessageQueueWhat, PayloadType]{
		label:    label,
		queue:    NewMessageQueue[MessageQueueWhat, PayloadType](queueLength),
		callback: callback,
		lock:     NewLockAndNotify(),
		aborted:  false,
		logger:   logger,
	}
}

func (mh *MessageHandler[MessageQueueWhat, PayloadType]) Size() uint16 {
	return mh.queue.CurrentSize()
}

func (mh *MessageHandler[MessageQueueWhat, PayloadType]) Capacity() uint16 {
	return mh.queue.Capacity()
}

func (mh *MessageHandler[MessageQueueWhat, PayloadType]) Send(what MessageQueueWhat, payload *PayloadType) bool {
	mh.logger.Trace(mh.label, fmt.Sprintf("Send: %v", what))
	mh.lock.Enter()

	if mh.aborted {
		mh.logger.Info(mh.label, "Send called on aborted messagehandler")
		mh.lock.Leave()

		return false
	} else {
		result := mh.queue.TryPush(time.Now(), what, payload)

		mh.lock.Leave()

		mh.logger.Assert(mh.label, "Handler queue too short in send", !result)

		mh.lock.Notify()

		return result
	}
}

func (mh *MessageHandler[MessageQueueWhat, PayloadType]) Timeout(what MessageQueueWhat, payload *PayloadType, delay time.Duration) bool {
	mh.logger.Trace(mh.label, fmt.Sprintf("Timeout: %v %s", what, delay))
	mh.lock.Enter()

	if mh.aborted {
		mh.logger.Info(mh.label, "Timeout called on aborted messagehandler")
		mh.lock.Leave()
		return false
	} else {
		result := mh.queue.TryPush(time.Now().Add(delay), what, payload)
		mh.lock.Leave()

		mh.logger.Assert(mh.label, "Handler queue too short in timout", !result)

		mh.lock.Notify()

		return result
	}
}

func (mh *MessageHandler[MessageQueueWhat, PayloadType]) Cancel(what MessageQueueWhat) {
	mh.logger.Trace(mh.label, fmt.Sprintf("Cancel: %v", what))
	mh.lock.Enter()

	if mh.aborted {
		mh.logger.Info(mh.label, "Cancel called on aborted messagehandler")
		mh.lock.Leave()
	} else {
		mh.queue.RemoveMessagesOfWhat(what)

		mh.lock.Leave()
		mh.lock.Notify()
	}
}

func (mh *MessageHandler[MessageQueueWhat, PayloadType]) CancelAll() {
	mh.logger.Trace(mh.label, "CancelAll")
	mh.lock.Enter()

	if mh.aborted {
		mh.logger.Info(mh.label, "CancelAll called on aborted messagehandler (will happen once every time)")
		mh.lock.Leave()
	} else {
		mh.queue.Clear()

		mh.lock.Leave()
		mh.lock.Notify()
	}
}

func (mh *MessageHandler[MessageQueueWhat, PayloadType]) DoWork(now time.Time) {
	max_messages_per_update := 10

	for max_messages_per_update > 0 {
		// See if we have any work to do, and if so to do it
		mh.lock.Enter()
		itemOptional := mh.queue.TryPopAtTime(now)
		mh.lock.Leave()

		if itemOptional.IsSet {
			// Do the work
			item := itemOptional.Value

			mh.logger.Trace(mh.label, fmt.Sprintf("WorkEvent: %v", item.what))
			// completed := make(chan bool, 0)

			// go func() {
			destroyBuffer := mh.callback(item.what, item.payload)
			if destroyBuffer {
				// Not really needed in golang
			}
			// 	completed <- true
			// }()

			// select {
			// case <-time.After(200 * time.Millisecond):
			// 	pstr := fmt.Sprintf("Handler %s took too long processing work item %v", mh.label, item.what)
			// 	panic(pstr)
			// case <-completed:
			// }
			mh.logger.Trace(mh.label, fmt.Sprintf("WorkEventCompleted: %v", item.what))

			max_messages_per_update--
		} else {
			return
		}
	}
}

func (mh *MessageHandler[MessageQueueWhat, PayloadType]) NextWorkAt() time.Time {
	mh.lock.Enter()
	itemOptional := mh.queue.TryPeek()
	mh.lock.Leave()

	if itemOptional.IsSet {
		return itemOptional.Value.at
	} else {
		// Sleep until some work arrives
		return EndOfTime
	}
}

func (mh *MessageHandler[MessageQueueWhat, PayloadType]) Loop(oncleanup func()) {
	go func() {
		work := func() {
			for {
				if mh.aborted {
					return
				}

				now := time.Now()
				waitUntil := now.Add(5 * time.Second)

				mh.DoWork(now)

				nextWork := mh.NextWorkAt()

				if nextWork.Before(waitUntil) {
					waitUntil = nextWork
				}

				if now.Before(waitUntil) {
					mh.lock.Wait(waitUntil.Sub(now))
				} else {
					// This is because we must call wait regularly to stop the waker in lock from blocking!
					mh.lock.Wait(5 * time.Millisecond)
				}
			}
		}

		work()
		if oncleanup != nil {
			oncleanup()
		}
	}()
}

// Clears all work and stops any loops
// Does not block waiting though
func (mh *MessageHandler[MessageQueueWhat, PayloadType]) Abort() {
	mh.logger.Trace(mh.label, "Abort")
	mh.lock.Enter()
	mh.aborted = true
	mh.lock.Leave()

	mh.CancelAll()
	mh.logger.Trace(mh.label, "Abort returned")
}
