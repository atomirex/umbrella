package razor

import (
	"os"
	"os/signal"
	"time"
)

// HORRIFYING HACK
var EndOfTime = time.Unix((1<<62)-1, 0)

type Optional[T any] struct {
	Value T
	IsSet bool
}

func NewOptional[T any](value T) Optional[T] {
	return Optional[T]{Value: value, IsSet: true}
}

func NilOptional[T any]() Optional[T] {
	return Optional[T]{IsSet: false}
}

func Swap[T any](a, b *T) {
	*a, *b = *b, *a
}

func Assert(cond bool) {
	if !cond {
		panic("Assertion failure")
	}
}

func WaitForOsInterruptSignal() {
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	<-stop
}
