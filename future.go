package future

import (
	"time"
	"errors"
	"fmt"
	"sync"
)

type ResultStatus int

const (
	PENDING ResultStatus = iota
	RUNNING
	SUCCESS
	ERRORED
	TIMEDOUT
	CANCELED
)

var noResultErr = errors.New("result not available yet")

type Value interface{}
type Fn func() (Value, error)

type Cancelable interface {
	Canceled() bool
	Cancel()
}

type Future interface {
	Get() *Result
	GetWithTimeout(time.Duration) *Result
	GetStatus() ResultStatus

	Then(func(Value) (Value, error)) Future
}

type Result struct {
	status ResultStatus
	latency time.Duration
	value Value
	err error
}

type impl struct{
	fn Fn
	result *Result

	started bool
	completed bool

	done chan struct{}

	resLock sync.Mutex // should this be a pointer?
	// Callbacks
}

func NewFuture(fn Fn) Future {
	return newSimpleFuture(fn)
}

func newSimpleFuture(fn Fn) *impl {
	done := make(chan struct{}, 1)
	return &impl{
		fn:     fn,
		done:   done,
	}
}

func (f *impl) GetStatus() ResultStatus {
	if f.result == nil {
		return PENDING
	}
	if f.started {
		return RUNNING
	}
	return f.result.status
}

func (f *impl) Get() *Result{
	if f.result != nil {
		return f.result
	}
	f.do()
	<-f.done
	return f.result
}

func (f*impl) GetWithTimeout(timeout time.Duration) *Result {
	var endTime time.Time
	f.do()
	select {
	case <-f.done:
		break
	case endTime = <-time.After(timeout):
		f.handleTimeout(timeout)
		break
	}
	return f.result
}

func (f*impl) Then(next func(value Value) (Value, error)) Future {
	nextFuture := newSimpleFuture(
		func() (Value, error) {
			if f.result == nil {
				return nil, errors.New("parent future result does not exist")
			}
			if f.result.status != SUCCESS {
				// TODO: Convert results to string
				return nil, fmt.Errorf("parent future did not complete successfully. parent results: %d", f.result.status)
			}
			return next(f.result.value)
		},
	)
	nextFuture.do()
	return nextFuture
}

func (f *impl) handleDone(value Value, err error, duration time.Duration) {
	status := SUCCESS
	if err != nil {
		status = ERRORED
	}
	result := Result{
		status:  status,
		latency: duration,
		value:   value,
		err:     err,
	}
	f.setResult(&result)
	f.done <- struct{}{}
}

func (f *impl) handleTimeout(timeout time.Duration) {
	result := Result{
		status:  TIMEDOUT,
		latency: timeout,
		value:   nil,
		err:     errors.New("future fn timeout"), // TODO: should a timeout be an error
	}
	f.setResult(&result)
}

func (f * impl) do() {
	f.started = true
	go func() {
		start := time.Now()
		val, err := f.fn()
		f.handleDone(val, err, time.Since(start))
	}()
}

func (f *impl) setResult(r *Result) {
	f.resLock.Lock()
	defer f.resLock.Unlock()
	if f.result == nil { // set result only if it has not already been set
		f.result = r
	}
}

type CancelableFuture struct {
	Future

	cancel <-chan struct{}
}

