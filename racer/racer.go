package racer

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/wocaolideTwistzz/conc/pool"
)

type Task[T any] func(ctx context.Context) (T, error)

var (
	ErrCloseByUser   = errors.New("close by user")
	ErrTasksFinished = errors.New("all tasks finished")
)

type Racer[T any] struct {
	ctx    context.Context
	cancel context.CancelCauseFunc

	primary   Task[T]
	fallbacks []Task[T]
	timeWait  time.Duration
	onClose   func(T)

	taskCh chan Task[T]
	retCh  chan T

	alreadyCallNext       bool
	alreadyStartFallbacks bool
	mu                    sync.Mutex

	pool *pool.ContextPool
}

func New[T any](ctx context.Context, primary Task[T], fallbacks ...Task[T]) *Racer[T] {
	ctx, cancel := context.WithCancelCause(ctx)
	r := &Racer[T]{
		ctx:    ctx,
		cancel: cancel,

		primary:   primary,
		fallbacks: fallbacks,

		taskCh: make(chan Task[T], len(fallbacks)+1),
		retCh:  make(chan T, len(fallbacks)+1),
		pool:   pool.New().WithContext(ctx),
	}
	go r.run()
	return r
}

func (r *Racer[T]) WithTimeWait(d time.Duration) *Racer[T] {
	r.timeWait = d
	return r
}

func (r *Racer[T]) WithMaxGoroutines(n int) *Racer[T] {
	r.pool.WithMaxGoroutines(n)
	return r
}

func (r *Racer[T]) WithOnClose(fn func(T)) *Racer[T] {
	r.onClose = fn
	return r
}

func (r *Racer[T]) Next() (T, error) {
	if ret, ok := r.poll(); ok {
		return ret, nil
	}

	result, ok := <-r.retCh
	if !ok {
		<-r.ctx.Done()
		return result, context.Cause(r.ctx)
	}
	return result, nil
}

func (r *Racer[T]) Close() error {
	r.cancel(ErrCloseByUser)

	go func() {
		for ret := range r.retCh {
			if r.onClose != nil {
				r.onClose(ret)
			}
		}
	}()
	return nil
}

func (r *Racer[T]) CloseWait() error {
	r.cancel(ErrCloseByUser)

	for ret := range r.retCh {
		if r.onClose != nil {
			r.onClose(ret)
		}
	}
	return nil
}

func (r *Racer[T]) poll() (ret T, ok bool) {
	if r.alreadyStartFallbacks {
		return
	}

	r.mu.Lock()
	if r.alreadyStartFallbacks {
		r.mu.Unlock()
		return
	}

	if r.alreadyCallNext {
		r.startFallbacks()
		r.mu.Unlock()
		return
	}

	r.alreadyCallNext = true
	r.mu.Unlock()

	var (
		timer *time.Timer
		errCh = make(chan struct{}, 1)
	)

	if r.timeWait > 0 {
		timer = time.NewTimer(r.timeWait)
		defer timer.Stop()
	} else {
		timer = &time.Timer{C: nil}
	}

	r.pool.Go(func(ctx context.Context) error {
		ret, err := r.primary(r.ctx)
		if err == nil {
			r.retCh <- ret
		} else {
			errCh <- struct{}{}
		}
		return nil
	})

	select {
	case <-timer.C:
	case <-errCh:
	case ret, ok := <-r.retCh:
		return ret, ok
	}
	r.mu.Lock()
	if !r.alreadyStartFallbacks {
		r.startFallbacks()
	}
	r.mu.Unlock()
	return
}

func (r *Racer[T]) startFallbacks() {
	for _, task := range r.fallbacks {
		r.taskCh <- task
	}
	close(r.taskCh)
	r.alreadyStartFallbacks = true
}

func (r *Racer[T]) run() {
	for {
		select {
		case <-r.ctx.Done():
			r.pool.Wait()
			close(r.retCh)
			return
		case task, ok := <-r.taskCh:
			if ok {
				r.pool.Go(func(ctx context.Context) error {
					ret, err := task(ctx)
					if err == nil {
						r.retCh <- ret
					}
					return nil
				})
			} else {
				r.pool.Wait()
				close(r.retCh)
				r.cancel(ErrTasksFinished)
				return
			}
		}
	}
}
