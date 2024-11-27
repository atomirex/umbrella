package razor

import (
	"time"
)

type MessageQueueWhat comparable

type MessageQueueItem[MessageQueueWhat, PayloadType any] struct {
	at      time.Time
	what    MessageQueueWhat
	payload *PayloadType
}

type MessageQueue[What MessageQueueWhat, PayloadType any] struct {
	heap     []MessageQueueItem[What, PayloadType]
	size     uint16
	capacity uint16
}

func NewMessageQueue[MessageQueueWhat comparable, PayloadType any](capacity uint16) *MessageQueue[MessageQueueWhat, PayloadType] {
	return &MessageQueue[MessageQueueWhat, PayloadType]{
		heap:     make([]MessageQueueItem[MessageQueueWhat, PayloadType], capacity),
		size:     0,
		capacity: capacity,
	}
}

func (mq *MessageQueue[MessageQueueWhat, PayloadType]) CurrentSize() uint16 {
	return mq.size
}

func (mq *MessageQueue[MessageQueueWhat, PayloadType]) Capacity() uint16 {
	return mq.capacity
}

func (mq *MessageQueue[MessageQueueWhat, PayloadType]) TryPush(at time.Time, what MessageQueueWhat, payload *PayloadType) bool {
	Assert(mq.size < mq.capacity)

	if mq.size >= mq.capacity {
		return false
	}

	mq.heap[mq.size] = MessageQueueItem[MessageQueueWhat, PayloadType]{at: at, what: what, payload: payload}
	mq.size++

	mq.heapifyUp(mq.size - 1)
	return true
}

func (mq *MessageQueue[MessageQueueWhat, PayloadType]) TryPop() Optional[MessageQueueItem[MessageQueueWhat, PayloadType]] {
	if mq.size == 0 {
		return NilOptional[MessageQueueItem[MessageQueueWhat, PayloadType]]()
	}

	item := mq.heap[0]
	mq.size--
	mq.heap[0] = mq.heap[mq.size]
	mq.heapifyDown(0)

	return NewOptional(item)
}

func (mq *MessageQueue[MessageQueueWhat, PayloadType]) TryPopAtTime(at time.Time) Optional[MessageQueueItem[MessageQueueWhat, PayloadType]] {
	if mq.size == 0 {
		return NilOptional[MessageQueueItem[MessageQueueWhat, PayloadType]]()
	}

	item := mq.heap[0]
	if at.After(item.at) {
		mq.size--
		mq.heap[0] = mq.heap[mq.size]
		mq.heapifyDown(0)

		return NewOptional(item)
	} else {
		return NilOptional[MessageQueueItem[MessageQueueWhat, PayloadType]]()
	}
}

func (mq *MessageQueue[MessageQueueWhat, PayloadType]) TryPeek() Optional[MessageQueueItem[MessageQueueWhat, PayloadType]] {
	if mq.size == 0 {
		return NilOptional[MessageQueueItem[MessageQueueWhat, PayloadType]]()
	}

	item := mq.heap[0]
	return NewOptional(item)
}

func (mq *MessageQueue[MessageQueueWhat, PayloadType]) Clear() {
	// Delete all buffers
	for i := uint16(0); i < mq.size; i++ {
		if mq.heap[i].payload != nil {
			// delete heap[i].payload;
			mq.heap[i].payload = nil
		}
	}

	// Set size to 0
	mq.size = 0
}

func (mq *MessageQueue[WHAT, PayloadType]) RemoveMessagesOfWhat(what WHAT) {
	for i := uint16(0); i < mq.size; {
		if mq.heap[i].what == what {
			// Delete the payload!!
			item := mq.heap[i]
			if item.payload != nil {
				// delete item.payload;
				mq.heap[i].payload = nil
			}

			// Replace the item with the last item
			mq.size--
			if mq.size > 0 {
				mq.heap[i] = mq.heap[mq.size]
				mq.heapifyDown(i) // Restore the heap property
			}
		} else {
			i++ // Only move to the next item if no removal happened
		}
	}
}

func (mq *MessageQueue[MessageQueueWhat, PayloadType]) heapifyUp(index uint16) {
	for index > 0 {
		parent := (index - 1) / 2
		if mq.heap[index].at.Before(mq.heap[parent].at) {
			Swap(&mq.heap[index], &mq.heap[parent])
			index = parent
		} else {
			return
		}
	}

}

func (mq *MessageQueue[MessageQueueWhat, PayloadType]) heapifyDown(index uint16) {
	for {
		left := 2*index + 1
		right := 2*index + 2
		smallest := index

		if left < mq.size && mq.heap[left].at.Before(mq.heap[smallest].at) {
			smallest = left
		}

		if right < mq.size && mq.heap[right].at.Before(mq.heap[smallest].at) {
			smallest = right
		}

		if smallest == index {
			// Completed!
			return
		}

		Swap(&mq.heap[index], &mq.heap[smallest])
		index = smallest
	}
}
